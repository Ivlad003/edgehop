// Package server runs on the computer the mouse and keyboard are physically
// connected to. It decides which screen currently owns the cursor and
// forwards input to it.
package server

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ivlad003/edgehop/internal/clip"
	"github.com/Ivlad003/edgehop/internal/input"
	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
	"github.com/Ivlad003/edgehop/internal/logx"
	"github.com/Ivlad003/edgehop/internal/proto"
	"github.com/Ivlad003/edgehop/internal/secure"
)

const (
	// entryInset is how far inside a screen the cursor appears when it
	// enters, so that it does not bounce straight back.
	entryInset = 2
	// settleInterval is how often the latest cursor position is re-sent over
	// TCP. Motion normally travels over UDP, where the last packet of a
	// movement may be lost; this guarantees the cursor ends up in the right
	// place, and keeps the cursor moving if UDP is blocked altogether.
	settleInterval = 20 * time.Millisecond
	pingInterval   = time.Second
	pongTimeout    = 6 * time.Second
	readTimeout    = 15 * time.Second
	udpTimeout     = 5 * time.Second
	maxDrain       = 512
)

// ScreenOpts are the per-client options.
type ScreenOpts struct {
	Scale float64
	Remap map[uint16]uint16
}

// Config is what the server needs to run.
type Config struct {
	Name    string
	Listen  string
	Key     string
	Layout  *layout.Layout
	Screens map[string]ScreenOpts
	Hotkey  keys.Hotkey
	// AutoScale adapts the cursor speed to each client's screen: the mouse
	// movement is measured in the server's pixels, and a screen with more
	// pixels would otherwise need proportionally more hand movement. The
	// per-screen Scale option multiplies the automatic factor.
	AutoScale bool

	// Clipboard, when set, is shared with the clients: text and copied files
	// follow the cursor. ClipDir is where files received by this computer are
	// kept; ClipLimit is the largest content accepted, in bytes (0 = any).
	Clipboard clip.Clipboard
	ClipDir   string
	ClipLimit int64

	// DropDir is where files that another computer sends to this one are
	// saved. Empty means this computer does not accept sent files.
	DropDir string
}

// autoScale returns how much faster the cursor should move on a client whose
// desktop is clientH pixels tall. Height is used rather than width because a
// second monitor next to the first doubles the width but not the density.
func autoScale(capture input.Capture, clientH int) float64 {
	_, serverH := capture.Size()
	if serverH <= 0 || clientH <= 0 {
		return 1
	}
	return math.Max(0.5, math.Min(float64(clientH)/float64(serverH), 3))
}

type session struct {
	id     uint32
	name   string
	os     string
	remote string
	w, h   int
	scale  float64
	remap  map[uint16]uint16

	ch      *secure.Channel
	udp     atomic.Pointer[netip.AddrPort]
	udpSeen atomic.Int64 // unix nanoseconds of the last UDP hello
	pong    atomic.Int64 // unix nanoseconds of the last pong
	rtt     atomic.Int64 // nanoseconds
	closed  atomic.Bool
	joined  atomic.Bool // the run loop knows this session: the cursor can go there

	caps   uint32
	data   atomic.Pointer[secure.Channel] // the data connection, once the client opened it
	dataMu sync.Mutex                     // one bundle at a time towards this client

	// Scratch buffers, used only by the run loop.
	buf []byte
	dg  []byte
}

func (s *session) close() {
	if s.closed.CompareAndSwap(false, true) {
		s.ch.Close()
	}
}

// send writes a TCP frame. A failure closes the session; the reader
// goroutine then reports the disconnect.
func (s *session) send(b []byte) {
	if s.closed.Load() {
		return
	}
	if err := s.ch.Send(b); err != nil {
		logx.Debugf("%s: send failed: %v", s.name, err)
		s.close()
	}
}

type ctlMsg struct {
	connect bool
	sess    *session
}

// Server is the input router.
type Server struct {
	cfg Config
	cap input.Capture
	ln  net.Listener
	udp *net.UDPConn

	ctl  chan ctlMsg
	done chan struct{}

	idMu sync.RWMutex
	byID map[uint32]*session

	views viewHub // remote windows (experimental)

	warnMu sync.Mutex
	warned map[string]time.Time

	unknownMu sync.Mutex
	unknown   map[string]UnknownClient

	// Everything below is owned by the run loop.
	byName  map[string]*session
	focus   *session // nil while the cursor is on the server itself
	cx, cy  float64  // cursor position on the focused screen
	sentX   int32
	sentY   int32
	mseq    uint32            // sequence number of the latest cursor position
	settled uint32            // last sequence number confirmed over TCP
	buttons uint32            // buttons held down on the focused screen
	held    map[uint16]uint16 // physical key -> key sent to the focused screen

	focusName atomic.Value // string, for Focus()
	clip      *clipHub     // clipboard and file transfers
}

// New creates a server. Call Listen, then Serve.
func New(cfg Config, capture input.Capture) *Server {
	s := &Server{
		cfg:     cfg,
		cap:     capture,
		ctl:     make(chan ctlMsg, 16),
		done:    make(chan struct{}),
		byID:    map[uint32]*session{},
		warned:  map[string]time.Time{},
		unknown: map[string]UnknownClient{},
		byName:  map[string]*session{},
		held:    map[uint16]uint16{},
	}
	s.focusName.Store("")
	s.clip = newClipHub(s)
	return s
}

// Listen opens the TCP and UDP sockets (same port number for both).
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", s.cfg.Listen, err)
	}
	tcpAddr := ln.Addr().(*net.TCPAddr)
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: tcpAddr.IP, Port: tcpAddr.Port, Zone: tcpAddr.Zone})
	if err != nil {
		ln.Close()
		return fmt.Errorf("cannot open UDP port %d: %w", tcpAddr.Port, err)
	}
	s.ln, s.udp = ln, udp
	return nil
}

// Addr returns the address the server is listening on.
func (s *Server) Addr() *net.TCPAddr { return s.ln.Addr().(*net.TCPAddr) }

// Focus returns the name of the screen that owns the cursor, "" for the
// server itself.
func (s *Server) Focus() string { return s.focusName.Load().(string) }

// Connected returns the names of the connected clients, sorted. A client
// counts from the moment the cursor can move to it.
func (s *Server) Connected() []string {
	s.idMu.RLock()
	defer s.idMu.RUnlock()
	out := make([]string, 0, len(s.byID))
	for _, sess := range s.byID {
		if sess.joined.Load() {
			out = append(out, sess.name)
		}
	}
	sort.Strings(out)
	return out
}

// ClientInfo describes one connected client.
type ClientInfo struct {
	Name  string  `json:"name"`
	OS    string  `json:"os"`
	W     int     `json:"w"`
	H     int     `json:"h"`
	Addr  string  `json:"addr"`
	UDP   bool    `json:"udp"`   // cursor motion travels over UDP
	Files bool    `json:"files"` // files can be sent to it
	View  bool    `json:"view"`  // its windows can be shown here
	RTTMs float64 `json:"rttMs"` // last measured round trip, milliseconds
}

// UnknownClient is a computer that has the right key but is not part of the
// layout yet, so the server had to turn it away.
type UnknownClient struct {
	Name string `json:"name"`
	OS   string `json:"os"`
	Addr string `json:"addr"`
	W    int    `json:"w"`
	H    int    `json:"h"`
	seen time.Time
}

// Unknown returns the computers that recently tried to connect with the
// right key but are missing from the layout, sorted by name. Clients retry
// every few seconds, so an entry disappears soon after its computer stops.
func (s *Server) Unknown() []UnknownClient {
	s.unknownMu.Lock()
	defer s.unknownMu.Unlock()
	out := []UnknownClient{}
	for name, u := range s.unknown {
		if time.Since(u.seen) > 15*time.Second {
			delete(s.unknown, name)
			continue
		}
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Clients returns the connected clients, sorted by name.
func (s *Server) Clients() []ClientInfo {
	s.idMu.RLock()
	defer s.idMu.RUnlock()
	out := make([]ClientInfo, 0, len(s.byID))
	for _, sess := range s.byID {
		if !sess.joined.Load() {
			continue
		}
		host, _, _ := net.SplitHostPort(sess.remote)
		out = append(out, ClientInfo{
			Name: sess.name, OS: sess.os, W: sess.w, H: sess.h, Addr: host,
			UDP:   sess.udp.Load() != nil,
			Files: sess.caps&proto.CapDrop != 0 && sess.data.Load() != nil,
			View:  sess.caps&proto.CapView != 0,
			RTTMs: math.Round(float64(sess.rtt.Load())/1e4) / 100,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// UDPReady reports whether cursor motion to the named client travels over UDP.
func (s *Server) UDPReady(name string) bool {
	s.idMu.RLock()
	defer s.idMu.RUnlock()
	for _, sess := range s.byID {
		if sess.name == name {
			return sess.udp.Load() != nil
		}
	}
	return false
}

// Serve runs until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}
	go s.acceptLoop()
	go s.udpLoop()

	tick := time.NewTicker(settleInterval)
	defer tick.Stop()
	defer s.shutdown()

	s.updateArmed()
	events := s.cap.Events()
	lastPing := time.Now()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-events:
			s.handleEvents(ev, events)
		case m := <-s.ctl:
			s.handleCtl(m)
		case now := <-tick.C:
			s.settle()
			if now.Sub(lastPing) >= pingInterval {
				lastPing = now
				s.pingAll(now)
			}
		}
	}
}

func (s *Server) shutdown() {
	if s.focus != nil {
		s.leaveRemote()
		s.setFocus(nil)
		s.cap.Release(0, 0, false)
	}
	close(s.done)
	s.ln.Close()
	s.udp.Close()
	s.idMu.RLock()
	for _, sess := range s.byID {
		sess.close()
	}
	s.idMu.RUnlock()
}

// ---------------------------------------------------------------------------
// Network side

func (s *Server) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			logx.Warnf("accept: %v", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		go s.handleConn(conn)
	}
}

// warnOnce logs a warning, but not more than once a minute per key: clients
// retry every few seconds and would otherwise flood the log.
func (s *Server) warnOnce(key, format string, args ...any) {
	s.warnMu.Lock()
	last, seen := s.warned[key]
	now := time.Now()
	if seen && now.Sub(last) < time.Minute {
		s.warnMu.Unlock()
		return
	}
	s.warned[key] = now
	s.warnMu.Unlock()
	logx.Warnf(format, args...)
}

func (s *Server) newSessionID() uint32 {
	var b [4]byte
	for {
		rand.Read(b[:])
		id := binary.BigEndian.Uint32(b[:])
		if id == 0 {
			continue
		}
		if _, taken := s.byID[id]; !taken {
			return id
		}
	}
}

func (s *Server) handleConn(conn net.Conn) {
	remote := conn.RemoteAddr().String()
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(10 * time.Second)
	}
	ch, err := secure.ServerHandshake(conn, s.cfg.Key)
	if err != nil {
		logx.Debugf("%s: %v", remote, err)
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Now().Add(secure.HandshakeTimeout))
	host, _, _ := net.SplitHostPort(remote)
	raw, err := ch.Recv()
	if err != nil {
		s.warnOnce("key:"+host, "rejected a connection from %s: its `key` does not match this server's key", host)
		conn.Close()
		return
	}
	hello, err := proto.Decode(raw)
	if err == nil && hello.Type == proto.TDataHello {
		conn.SetReadDeadline(time.Time{})
		s.handleData(ch, hello.Session)
		return
	}
	if err == nil && hello.Type == proto.TViewHello {
		conn.SetReadDeadline(time.Time{})
		s.handleView(ch, hello.Session, hello.Bundle)
		return
	}
	if err != nil || hello.Type != proto.THello {
		conn.Close()
		return
	}
	reject := func(reason string) {
		s.warnOnce("reject:"+host+hello.Name, "rejected %q (%s): %s", hello.Name, host, reason)
		ch.Send(proto.AppendReject(nil, reason))
		conn.Close()
	}
	switch {
	case hello.Version != proto.Version:
		reject(fmt.Sprintf("protocol version %d, the server speaks %d - use the same edgehop version on every computer", hello.Version, proto.Version))
		return
	case hello.Name == s.cfg.Name:
		reject("this name is already used by the server; every computer needs its own `name`")
		return
	case !s.cfg.Layout.Has(hello.Name):
		s.unknownMu.Lock()
		s.unknown[hello.Name] = UnknownClient{Name: hello.Name, OS: hello.OS, Addr: host, W: int(hello.W), H: int(hello.H), seen: time.Now()}
		s.unknownMu.Unlock()
		reject(fmt.Sprintf("screen %q is not in the server's layout; add it to `row` in the server's config", hello.Name))
		return
	case hello.W < 100 || hello.H < 100 || hello.W > 100000 || hello.H > 100000:
		reject(fmt.Sprintf("implausible screen size %dx%d", hello.W, hello.H))
		return
	}

	sess := &session{
		name:   hello.Name,
		os:     hello.OS,
		remote: remote,
		w:      int(hello.W),
		h:      int(hello.H),
		scale:  1,
		caps:   hello.Caps,
		ch:     ch,
		buf:    make([]byte, 0, 256),
		dg:     make([]byte, 0, 256),
	}
	if o, ok := s.cfg.Screens[sess.name]; ok {
		if o.Scale > 0 {
			sess.scale = o.Scale
		}
		sess.remap = o.Remap
	}
	if s.cfg.AutoScale {
		sess.scale *= autoScale(s.cap, sess.h)
	}
	sess.pong.Store(time.Now().UnixNano())

	s.idMu.Lock()
	sess.id = s.newSessionID()
	s.byID[sess.id] = sess
	s.idMu.Unlock()
	defer func() {
		sess.close()
		if d := sess.data.Load(); d != nil {
			d.Close()
		}
		s.idMu.Lock()
		delete(s.byID, sess.id)
		s.idMu.Unlock()
		select {
		case s.ctl <- ctlMsg{connect: false, sess: sess}:
		case <-s.done:
		}
	}()

	var caps uint32
	if s.cfg.Clipboard != nil {
		caps |= proto.CapClipboard
	}
	if s.cfg.DropDir != "" {
		caps |= proto.CapDrop
	}
	if err := ch.Send(proto.AppendWelcome(nil, sess.id, s.cfg.Name, caps)); err != nil {
		return
	}
	select {
	case s.ctl <- ctlMsg{connect: true, sess: sess}:
	case <-s.done:
		return
	}

	for {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		raw, err := ch.Recv()
		if err != nil {
			if !sess.closed.Load() {
				logx.Debugf("%s: connection lost: %v", sess.name, err)
			}
			return
		}
		m, err := proto.Decode(raw)
		if err != nil {
			logx.Debugf("%s: %v", sess.name, err)
			return
		}
		switch m.Type {
		case proto.TPong:
			now := time.Now().UnixNano()
			sess.pong.Store(now)
			sess.rtt.Store(now - int64(m.T))
		case proto.TClipState:
			if s.clip != nil {
				s.clip.clientState(sess, m.Changed)
			}
		}
	}
}

func (s *Server) udpLoop() {
	buf := make([]byte, 2048)
	out := make([]byte, 0, 2048)
	for {
		n, addr, err := s.udp.ReadFromUDPAddrPort(buf)
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue // e.g. ICMP "port unreachable" surfaced on Windows
		}
		id, ok := secure.DatagramSession(buf[:n])
		if !ok {
			continue
		}
		s.idMu.RLock()
		sess := s.byID[id]
		s.idMu.RUnlock()
		if sess == nil {
			continue
		}
		pt, err := sess.ch.OpenDatagram(out, buf[:n])
		if err != nil || len(pt) == 0 || pt[0] != proto.TUDPHello {
			continue
		}
		sess.udpSeen.Store(time.Now().UnixNano())
		if old := sess.udp.Load(); old == nil || *old != addr {
			a := addr
			sess.udp.Store(&a)
			logx.Debugf("%s: cursor motion now uses UDP (%s)", sess.name, addr)
		}
	}
}

// ---------------------------------------------------------------------------
// Run loop: connection changes

func (s *Server) available(name string) bool {
	return name == s.cfg.Name || s.byName[name] != nil
}

func (s *Server) updateArmed() {
	var mask uint8
	for side := layout.Left; side <= layout.Bottom; side++ {
		if s.cfg.Layout.Resolve(s.cfg.Name, side, s.available) != "" {
			mask |= side.Bit()
		}
	}
	s.cap.SetArmedEdges(mask)
}

func (s *Server) setFocus(sess *session) {
	s.focus = sess
	if sess == nil {
		s.focusName.Store("")
	} else {
		s.focusName.Store(sess.name)
	}
}

func (s *Server) handleCtl(m ctlMsg) {
	sess := m.sess
	if m.connect {
		if old := s.byName[sess.name]; old != nil {
			logx.Infof("%s reconnected; dropping its old connection", sess.name)
			if s.focus == old {
				s.dropFocus()
			}
			old.close()
		}
		s.byName[sess.name] = sess
		sess.joined.Store(true)
		s.updateArmed()
		logx.Infof("connected: %s (%s, %dx%d) from %s, cursor speed x%.2f", sess.name, sess.os, sess.w, sess.h, sess.remote, sess.scale)
		return
	}
	if s.byName[sess.name] != sess {
		return // an old connection that was already replaced
	}
	delete(s.byName, sess.name)
	if s.focus == sess {
		logx.Infof("%s disconnected while it had the cursor; back to %s", sess.name, s.cfg.Name)
		s.dropFocus()
	} else {
		logx.Infof("disconnected: %s", sess.name)
	}
	s.updateArmed()
}

// dropFocus returns to the server's own desktop when the focused client can
// no longer be told anything.
func (s *Server) dropFocus() {
	s.setFocus(nil)
	s.buttons = 0
	clear(s.held)
	s.cap.Release(0, 0, false)
}

func (s *Server) pingAll(now time.Time) {
	ns := now.UnixNano()
	for _, sess := range s.byName {
		if ns-sess.pong.Load() > int64(pongTimeout) {
			logx.Infof("%s stopped responding", sess.name)
			sess.close()
			continue
		}
		if sess.udp.Load() != nil && ns-sess.udpSeen.Load() > int64(udpTimeout) {
			sess.udp.Store(nil)
			logx.Debugf("%s: UDP went quiet, cursor motion falls back to TCP", sess.name)
		}
		sess.send(proto.AppendPing(sess.buf[:0], uint64(ns)))
	}
}

// ---------------------------------------------------------------------------
// Run loop: input

// handleEvents processes ev and everything else already queued. Consecutive
// motion events are merged into one, so a slow moment never builds up a
// backlog of stale cursor positions.
func (s *Server) handleEvents(ev input.Event, events <-chan input.Event) {
	for i := 0; i < maxDrain; i++ {
		if ev.Kind == input.EvMotion {
			dx, dy := ev.DX, ev.DY
			var next input.Event
			have := false
		merge:
			for {
				select {
				case n := <-events:
					if n.Kind == input.EvMotion {
						dx += n.DX
						dy += n.DY
						continue
					}
					next, have = n, true
					break merge
				default:
					break merge
				}
			}
			s.motion(dx, dy)
			if have {
				ev = next
				continue
			}
		} else {
			s.handle(ev)
		}
		select {
		case ev = <-events:
		default:
			return
		}
	}
}

func (s *Server) handle(ev input.Event) {
	switch ev.Kind {
	case input.EvEdge:
		if s.focus == nil {
			s.leaveLocal(ev.Side, ev.Frac)
		}
	case input.EvButton:
		if s.focus == nil || ev.Code == 0 || ev.Code > 31 {
			return
		}
		bit := uint32(1) << ev.Code
		if ev.State != 0 {
			s.buttons |= bit
		} else {
			if s.buttons&bit == 0 {
				return // released without having been pressed on this screen
			}
			s.buttons &^= bit
		}
		s.focus.send(proto.AppendButton(s.focus.buf[:0], s.mseq, s.sentX, s.sentY, uint8(ev.Code), ev.State != 0))
		s.settled = s.mseq
	case input.EvWheel:
		if s.focus != nil {
			s.focus.send(proto.AppendWheel(s.focus.buf[:0], ev.WX, ev.WY))
		}
	case input.EvKey:
		s.key(ev.Code, ev.State)
	}
}

func (s *Server) key(code uint16, state uint8) {
	sess := s.focus
	if sess == nil {
		return
	}
	if state == keys.Up {
		sent, ok := s.held[code]
		if !ok {
			return
		}
		delete(s.held, code)
		sess.send(proto.AppendKey(sess.buf[:0], sent, keys.Up))
		return
	}
	sent, isHeld := s.held[code]
	if !isHeld {
		if s.cfg.Hotkey.Match(code, func(c uint16) bool { _, ok := s.held[c]; return ok }) {
			logx.Infof("release hotkey: back to %s", s.cfg.Name)
			s.clipMoved(sess.name, s.cfg.Name)
			s.leaveRemote()
			s.setFocus(nil)
			s.cap.Release(0, 0, false)
			return
		}
		sent = code
		if r, ok := sess.remap[code]; ok {
			sent = r
		}
		s.held[code] = sent
		state = keys.Down
	}
	sess.send(proto.AppendKey(sess.buf[:0], sent, state))
}

// leaveLocal is called when the cursor pushes against an edge of the
// server's own desktop.
func (s *Server) leaveLocal(side layout.Side, frac float64) {
	target := s.cfg.Layout.Resolve(s.cfg.Name, side, s.available)
	sess := s.byName[target]
	if sess == nil {
		return
	}
	s.cap.Grab()
	s.enter(sess, side.Opposite(), frac)
	s.clipMoved(s.cfg.Name, sess.name)
	logx.Debugf("cursor: %s -> %s", s.cfg.Name, sess.name)
}

// enter gives the cursor to a client; it appears on entrySide at frac.
func (s *Server) enter(sess *session, entrySide layout.Side, frac float64) {
	x, y := layout.EntryPoint(sess.w, sess.h, entrySide, frac, entryInset)
	s.setFocus(sess)
	s.cx, s.cy = float64(x), float64(y)
	s.sentX, s.sentY = int32(x), int32(y)
	s.buttons = 0
	clear(s.held)
	s.mseq++
	s.settled = s.mseq
	sess.send(proto.AppendEnter(sess.buf[:0], s.mseq, s.sentX, s.sentY))
}

// leaveRemote tells the focused client that the cursor is leaving, releasing
// whatever is still held down there so nothing gets stuck.
func (s *Server) leaveRemote() {
	sess := s.focus
	if sess == nil {
		return
	}
	for phys, sent := range s.held {
		sess.send(proto.AppendKey(sess.buf[:0], sent, keys.Up))
		delete(s.held, phys)
	}
	for b := uint8(1); b <= 31; b++ {
		if s.buttons&(1<<b) != 0 {
			sess.send(proto.AppendButton(sess.buf[:0], s.mseq, s.sentX, s.sentY, b, false))
		}
	}
	s.buttons = 0
	sess.send(proto.AppendLeave(sess.buf[:0]))
}

func (s *Server) motion(dx, dy float64) {
	sess := s.focus
	if sess == nil {
		return
	}
	nx := s.cx + dx*sess.scale
	ny := s.cy + dy*sess.scale
	maxX, maxY := float64(sess.w-1), float64(sess.h-1)

	// Never switch screens in the middle of a drag.
	if s.buttons == 0 {
		if nx < 0 && s.cross(sess, layout.Left, nx, ny) {
			return
		}
		if nx > maxX && s.cross(sess, layout.Right, nx, ny) {
			return
		}
		if ny < 0 && s.cross(sess, layout.Top, nx, ny) {
			return
		}
		if ny > maxY && s.cross(sess, layout.Bottom, nx, ny) {
			return
		}
	}
	s.cx = math.Max(0, math.Min(nx, maxX))
	s.cy = math.Max(0, math.Min(ny, maxY))
	s.sendMotion()
}

// cross moves the cursor from sess through the given side, if a screen is
// reachable there.
func (s *Server) cross(sess *session, side layout.Side, nx, ny float64) bool {
	target := s.cfg.Layout.Resolve(sess.name, side, s.available)
	if target == "" {
		return false
	}
	maxX, maxY := float64(sess.w-1), float64(sess.h-1)
	frac := 0.5
	if side == layout.Left || side == layout.Right {
		if maxY > 0 {
			frac = math.Max(0, math.Min(ny, maxY)) / maxY
		}
	} else if maxX > 0 {
		frac = math.Max(0, math.Min(nx, maxX)) / maxX
	}
	s.clipMoved(sess.name, target) // before the client hears about it
	s.leaveRemote()
	if target == s.cfg.Name {
		s.setFocus(nil)
		s.cap.Release(side.Opposite(), frac, true)
		logx.Debugf("cursor: %s -> %s", sess.name, s.cfg.Name)
		return true
	}
	next := s.byName[target]
	s.enter(next, side.Opposite(), frac)
	logx.Debugf("cursor: %s -> %s", sess.name, next.name)
	return true
}

func (s *Server) sendMotion() {
	sess := s.focus
	x, y := int32(math.Round(s.cx)), int32(math.Round(s.cy))
	if x == s.sentX && y == s.sentY {
		return
	}
	s.sentX, s.sentY = x, y
	s.mseq++
	b := proto.AppendMotion(sess.buf[:0], s.mseq, x, y)
	if addr := sess.udp.Load(); addr != nil {
		sess.dg = sess.ch.SealDatagram(sess.dg, sess.id, b)
		s.udp.WriteToUDPAddrPort(sess.dg, *addr)
		return
	}
	sess.send(b)
	s.settled = s.mseq
}

// settle re-sends the latest cursor position over TCP if it has only been
// sent over UDP so far.
func (s *Server) settle() {
	sess := s.focus
	if sess == nil || s.settled == s.mseq {
		return
	}
	s.settled = s.mseq
	sess.send(proto.AppendMotion(sess.buf[:0], s.mseq, s.sentX, s.sentY))
}

// clipMoved tells the clipboard hub that the cursor moves between screens.
func (s *Server) clipMoved(from, to string) {
	if s.clip != nil {
		s.clip.moved(from, to)
	}
}

// sessionNamed finds a connected client by name. Unlike byName it may be
// called from any goroutine.
func (s *Server) sessionNamed(name string) *session {
	s.idMu.RLock()
	defer s.idMu.RUnlock()
	for _, sess := range s.byID {
		if sess.name == name {
			return sess
		}
	}
	return nil
}

// SendFiles sends files to a connected client, which saves them in its
// folder for received files. It blocks until the transfer is over.
func (s *Server) SendFiles(to string, paths []string) error {
	return s.clip.sendFiles(to, paths)
}

// ClipboardStatus describes the last clipboard transfer, "" if there was none.
func (s *Server) ClipboardStatus() string {
	if s.clip == nil {
		return ""
	}
	return s.clip.status()
}

// ClipboardTransfers counts the clipboard transfers completed so far.
func (s *Server) ClipboardTransfers() int64 {
	if s.clip == nil {
		return 0
	}
	s.clip.mu.Lock()
	defer s.clip.mu.Unlock()
	return s.clip.count
}

// handleData serves a client's data connection: the channel that carries
// clipboard content, kept apart from the input channel so that a large file
// never delays a key press.
func (s *Server) handleData(ch *secure.Channel, sessionID uint32) {
	defer ch.Close()
	s.idMu.RLock()
	sess := s.byID[sessionID]
	s.idMu.RUnlock()
	if sess == nil || sess.caps&(proto.CapClipboard|proto.CapDrop) == 0 {
		return
	}
	if old := sess.data.Swap(ch); old != nil {
		old.Close()
	}
	defer sess.data.CompareAndSwap(ch, nil)
	logx.Debugf("%s: data connection open", sess.name)
	for {
		frame, err := ch.Recv()
		if err != nil {
			s.clip.senderGone(sess)
			return
		}
		s.clip.fromClient(sess, frame)
	}
}
