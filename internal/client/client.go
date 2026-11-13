// Package client runs on every computer that is controlled by the server's
// mouse and keyboard. It connects to the server and injects what it receives.
package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ivlad003/edgehop/internal/clip"
	"github.com/Ivlad003/edgehop/internal/input"
	"github.com/Ivlad003/edgehop/internal/logx"
	"github.com/Ivlad003/edgehop/internal/proto"
	"github.com/Ivlad003/edgehop/internal/secure"
)

const (
	dialTimeout   = 3 * time.Second
	readTimeout   = 6 * time.Second // the server pings every second
	retryMin      = time.Second
	retryMax      = 5 * time.Second
	udpHelloFast  = 200 * time.Millisecond
	udpHelloSlow  = time.Second
	sizePollEvery = 2 * time.Second
)

// Config is what the client needs to run.
type Config struct {
	Name   string
	Server string // host:port
	Key    string

	// Clipboard, when set, is shared with the server: text and copied files
	// follow the cursor. ClipDir is where received files are kept; ClipLimit
	// is the largest content accepted, in bytes (0 = any).
	Clipboard clip.Clipboard
	ClipDir   string
	ClipLimit int64
}

// Client is a connection to a server that reconnects forever.
type Client struct {
	cfg Config
	inj input.Injector

	connected atomic.Bool
	active    atomic.Bool
	lastErr   atomic.Value // string: why the last connection attempt failed
	clipNote  atomic.Value // string: the last clipboard transfer
	clipCount atomic.Int64 // completed clipboard transfers
}

// New creates a client.
func New(cfg Config, inj input.Injector) *Client {
	return &Client{cfg: cfg, inj: inj}
}

// Connected reports whether the client currently has a session with the server.
func (c *Client) Connected() bool { return c.connected.Load() }

// LastError returns why the client is not connected, or "" while it is
// connected (or has not tried yet).
func (c *Client) LastError() string {
	s, _ := c.lastErr.Load().(string)
	return s
}

// ClipboardStatus describes the last clipboard transfer, "" if there was none.
func (c *Client) ClipboardStatus() string {
	s, _ := c.clipNote.Load().(string)
	return s
}

// ClipboardTransfers counts the clipboard transfers completed so far.
func (c *Client) ClipboardTransfers() int64 { return c.clipCount.Load() }

// Active reports whether the cursor is currently on this computer.
func (c *Client) Active() bool { return c.active.Load() }

// Run connects to the server and keeps reconnecting until ctx is cancelled.
// Problems (server not started yet, wrong key, name missing from the server's
// layout) are logged once and retried, so the computers can be started and
// reconfigured in any order.
func (c *Client) Run(ctx context.Context) error {
	delay := retryMin
	lastMsg := ""
	for {
		start := time.Now()
		err := c.session(ctx)
		c.inj.ReleaseAll()
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(start) > 10*time.Second {
			delay = retryMin
			lastMsg = ""
		}
		c.lastErr.Store(err.Error())
		if msg := err.Error(); msg != lastMsg {
			logx.Infof("%v - will keep retrying", err)
			lastMsg = msg
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		delay = min(delay*2, retryMax)
	}
}

// state is the per-session cursor state shared by the TCP and UDP readers.
type state struct {
	mu      sync.Mutex
	inj     input.Injector
	active  bool
	lastSeq uint32
}

func (s *state) motion(m proto.Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Positions arrive over UDP and, as a safety net, over TCP. Apply each
	// one at most once and never go back to an older one.
	if !s.active || int32(m.Seq-s.lastSeq) <= 0 {
		return
	}
	s.lastSeq = m.Seq
	s.inj.MoveAbs(int(m.X), int(m.Y))
}

func (c *Client) session(ctx context.Context) error {
	d := net.Dialer{Timeout: dialTimeout, KeepAlive: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", c.cfg.Server)
	if err != nil {
		return fmt.Errorf("cannot reach the server at %s (%v)", c.cfg.Server, shortErr(err))
	}
	// Deferred calls run last-in-first-out: close(stop) must run before
	// wg.Wait so the helper goroutines can finish.
	defer conn.Close()
	var wg sync.WaitGroup
	defer wg.Wait()
	stop := make(chan struct{})
	defer close(stop)

	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	wg.Add(1)
	go func() { // unblock the reader when the program is asked to stop
		defer wg.Done()
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stop:
		}
	}()

	ch, err := secure.ClientHandshake(conn, c.cfg.Key)
	if err != nil {
		return fmt.Errorf("server %s: %w", c.cfg.Server, err)
	}
	w, h := c.inj.Size()
	var caps uint32
	if c.cfg.Clipboard != nil {
		caps |= proto.CapClipboard
	}
	if err := ch.Send(proto.AppendHello(nil, c.cfg.Name, runtime.GOOS, w, h, caps)); err != nil {
		return err
	}
	conn.SetReadDeadline(time.Now().Add(secure.HandshakeTimeout))
	raw, err := ch.Recv()
	if err != nil {
		return fmt.Errorf("server %s closed the connection during setup: %w", c.cfg.Server, err)
	}
	welcome, err := proto.Decode(raw)
	if err != nil {
		return err
	}
	switch welcome.Type {
	case proto.TWelcome:
	case proto.TReject:
		return fmt.Errorf("the server refused this computer: %s", welcome.Reason)
	default:
		return fmt.Errorf("unexpected reply from the server (type %d)", welcome.Type)
	}
	logx.Infof("connected to %q at %s as %q, screen %dx%d", welcome.Name, c.cfg.Server, c.cfg.Name, w, h)
	c.connected.Store(true)
	c.lastErr.Store("")
	defer func() {
		c.connected.Store(false)
		c.active.Store(false)
	}()

	st := &state{inj: c.inj}
	clips := c.openClipboard(ctx, ch, welcome)
	if clips != nil {
		defer clips.close()
		wg.Add(1)
		go func() { defer wg.Done(); clips.receive() }()
		logx.Infof("clipboard: shared (%s)", c.cfg.Clipboard.Describe())
	}
	if udp, err := c.openUDP(conn); err != nil {
		logx.Debugf("UDP unavailable, cursor motion will use TCP: %v", err)
	} else {
		defer udp.Close()
		wg.Add(2)
		go func() { defer wg.Done(); c.udpReader(udp, ch, st) }()
		go func() { defer wg.Done(); c.udpHello(udp, ch, welcome.Session, stop) }()
	}
	// If the desktop size changes (resolution, monitor plugged in), reconnect
	// so the server learns the new size.
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(sizePollEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if nw, nh := c.inj.Size(); nw != w || nh != h {
					logx.Infof("screen size changed to %dx%d, reconnecting", nw, nh)
					conn.Close()
					return
				}
			}
		}
	}()

	var out []byte
	for {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		raw, err := ch.Recv()
		if err != nil {
			return fmt.Errorf("lost the connection to %s (%v)", c.cfg.Server, shortErr(err))
		}
		m, err := proto.Decode(raw)
		if err != nil {
			return err
		}
		switch m.Type {
		case proto.TMotion:
			st.motion(m)
		case proto.TEnter:
			st.mu.Lock()
			st.active = true
			st.lastSeq = m.Seq
			c.inj.MoveAbs(int(m.X), int(m.Y))
			st.mu.Unlock()
			c.active.Store(true)
			logx.Debugf("cursor entered at %d,%d", m.X, m.Y)
		case proto.TLeave:
			st.mu.Lock()
			st.active = false
			c.inj.ReleaseAll()
			st.mu.Unlock()
			c.active.Store(false)
			logx.Debugf("cursor left")
			if clips != nil {
				go clips.cursorLeft()
			}
		case proto.TClipSend:
			if clips != nil {
				go clips.send(m.Bundle)
			}
		case proto.TButton:
			st.mu.Lock()
			if st.active {
				// The click must land where the server's cursor was.
				if int32(m.Seq-st.lastSeq) >= 0 {
					st.lastSeq = m.Seq
					c.inj.MoveAbs(int(m.X), int(m.Y))
				}
				c.inj.Button(m.Button, m.Down)
			}
			st.mu.Unlock()
		case proto.TWheel:
			st.mu.Lock()
			if st.active {
				c.inj.Wheel(int(m.DX), int(m.DY))
			}
			st.mu.Unlock()
		case proto.TKey:
			st.mu.Lock()
			if st.active {
				c.inj.Key(m.Code, m.State)
			}
			st.mu.Unlock()
		case proto.TPing:
			out = proto.AppendPong(out[:0], m.T)
			if err := ch.Send(out); err != nil {
				return err
			}
		}
	}
}

// shortErr strips the "dial tcp 1.2.3.4:5: " style prefixes from network errors.
func shortErr(err error) error {
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err
	}
	return err
}

// openUDP opens a UDP socket towards the server's port. Sending from the
// client first means only the server needs an open port in its firewall.
func (c *Client) openUDP(conn net.Conn) (*net.UDPConn, error) {
	ra, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return nil, errors.New("not a TCP connection")
	}
	return net.DialUDP("udp", nil, &net.UDPAddr{IP: ra.IP, Port: ra.Port, Zone: ra.Zone})
}

func (c *Client) udpReader(udp *net.UDPConn, ch *secure.Channel, st *state) {
	buf := make([]byte, 2048)
	out := make([]byte, 0, 2048)
	for {
		n, err := udp.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Windows reports ICMP errors from earlier sends here; keep going.
			time.Sleep(10 * time.Millisecond)
			continue
		}
		pt, err := ch.OpenDatagram(out, buf[:n])
		if err != nil {
			continue
		}
		m, err := proto.Decode(pt)
		if err != nil || m.Type != proto.TMotion {
			continue
		}
		st.motion(m)
	}
}

// udpHello tells the server our UDP address, quickly at first and then as a
// keep-alive for firewalls and NAT.
func (c *Client) udpHello(udp *net.UDPConn, ch *secure.Channel, session uint32, stop <-chan struct{}) {
	payload := proto.AppendUDPHello(nil)
	var pkt []byte
	for i := 0; ; i++ {
		pkt = ch.SealDatagram(pkt, session, payload)
		udp.Write(pkt)
		wait := udpHelloSlow
		if i < 10 {
			wait = udpHelloFast
		}
		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
	}
}
