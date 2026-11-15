package server

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Ivlad003/edgehop/internal/clip"
	"github.com/Ivlad003/edgehop/internal/logx"
	"github.com/Ivlad003/edgehop/internal/proto"
	"github.com/Ivlad003/edgehop/internal/xfer"
)

// clipHub moves content between the computers: the shared clipboard, and
// files a user sends on purpose.
//
// Clipboard. Every screen holds some version of the shared clipboard. When
// the cursor leaves a screen whose clipboard changed, that content becomes
// the newest version. When the cursor enters a screen that holds an older
// version than the screen it came from, the content is sent along. So
// whatever was copied last is there to paste on the screen the cursor is on,
// and it travels only when it is needed, which matters for large files.
//
// Sent files ("drops") go to the folder the receiving computer keeps for
// them and never touch a clipboard.
//
// The server is the hub: content between two clients is relayed through it.
type clipHub struct {
	s     *Server
	local clip.Clipboard // nil when the server's own clipboard is not shared

	mu        sync.Mutex
	global    uint64            // newest clipboard version
	ver       map[string]uint64 // clipboard version each screen holds
	localMark string            // the server's clipboard mark at its last sync
	nextID    uint32            // ids the server gives to bundles
	routes    map[routeKey]route
	dest      map[string]string // where the cursor went when it last left each client
	last      string
	count     int64 // completed transfers

	recvMu sync.Mutex
	recv   xfer.Receiver // bundles addressed to the server itself
}

// routeKey identifies a bundle arriving from a client. Clients number the
// files they send themselves, so the id alone is not unique.
type routeKey struct {
	from string
	id   uint32
}

// route says where a bundle from a client goes.
type route struct {
	from, to string
	ver      uint64 // clipboard version it carries; 0 for sent files
	out      uint32 // the id it travels under from the server onwards
	drop     bool
}

func newClipHub(s *Server) *clipHub {
	return &clipHub{
		s:      s,
		local:  s.cfg.Clipboard,
		ver:    map[string]uint64{},
		routes: map[routeKey]route{},
		dest:   map[string]string{},
		recv:   xfer.Receiver{Dir: s.cfg.ClipDir, DropDir: s.cfg.DropDir, Limit: s.cfg.ClipLimit},
	}
}

func (h *clipHub) status() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

func (h *clipHub) note(what, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	h.mu.Lock()
	h.last = msg
	h.count++
	h.mu.Unlock()
	logx.Infof("%s: %s", what, msg)
}

func (h *clipHub) newID() uint32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	if h.nextID >= 1<<31 { // the upper half belongs to ids chosen by clients
		h.nextID = 1
	}
	return h.nextID
}

// moved is called by the run loop when the cursor moves from one screen to
// another. It must not block: reading a clipboard or sending files can take
// a while.
//
// When the cursor leaves a client, moved must be called before that client
// is told (TLeave): the client answers with TClipState at once, and by then
// the hub has to know where the cursor went.
func (h *clipHub) moved(from, to string) {
	if from == h.s.cfg.Name {
		if h.local != nil {
			go h.fromServer(to)
		}
		return
	}
	h.mu.Lock()
	h.dest[from] = to
	h.mu.Unlock()
}

// fromServer runs when the cursor left the server's own desktop.
func (h *clipHub) fromServer(to string) {
	self := h.s.cfg.Name
	mark, err := h.local.Mark()
	h.mu.Lock()
	if err == nil && mark != h.localMark {
		h.localMark = mark
		h.global++
		h.ver[self] = h.global
	}
	version := h.ver[self]
	need := version > h.ver[to]
	h.mu.Unlock()
	if !need {
		return
	}
	dest := h.s.sessionNamed(to)
	if dest == nil || dest.caps&proto.CapClipboard == 0 {
		return
	}
	// A client opens its data connection right after it connected. If the
	// cursor went over in that very moment, wait for it rather than drop
	// what was copied.
	ch := dest.data.Load()
	for i := 0; ch == nil && i < 60 && !dest.closed.Load(); i++ {
		time.Sleep(50 * time.Millisecond)
		ch = dest.data.Load()
	}
	if ch == nil {
		return
	}
	content, err := h.local.Read()
	if err != nil {
		logx.Debugf("clipboard: cannot read: %v", err)
		return
	}
	if content.Kind == clip.None {
		h.setVersion(to, version) // nothing edgehop can carry (an image, for example)
		return
	}
	id := h.newID()
	dest.dataMu.Lock()
	st, err := xfer.Send(ch.Send, id, content, h.s.cfg.ClipLimit)
	dest.dataMu.Unlock()
	switch {
	case err == nil:
		h.setVersion(to, version)
		h.note("clipboard", "%s: %s -> %s", st, self, to)
	case errors.Is(err, xfer.ErrTooLarge):
		h.setVersion(to, version) // do not try again on every switch
		h.note("clipboard", "not shared with %s: %s is over the size limit of %s", to, xfer.Size(st.Bytes), xfer.Size(h.s.cfg.ClipLimit))
	default:
		logx.Warnf("clipboard: sending to %s failed: %v", to, err)
	}
}

func (h *clipHub) setVersion(name string, version uint64) {
	h.mu.Lock()
	if h.ver[name] < version {
		h.ver[name] = version
	}
	h.mu.Unlock()
}

// clientState handles a client's report, sent when the cursor left it,
// whether its clipboard changed while the cursor was there.
func (h *clipHub) clientState(sess *session, changed bool) {
	self := h.s.cfg.Name
	h.mu.Lock()
	cur := h.dest[sess.name]
	if cur == "" {
		cur = self
	}
	if changed {
		h.global++
		h.ver[sess.name] = h.global
	}
	version := h.ver[sess.name]
	need := cur != sess.name && version > h.ver[cur]
	if need && cur == self && h.local == nil {
		need = false // the server does not share its own clipboard
	}
	var key routeKey
	if need {
		h.nextID++
		key = routeKey{sess.name, h.nextID}
		h.routes[key] = route{from: sess.name, to: cur, ver: version, out: h.nextID}
	}
	h.mu.Unlock()
	if !need {
		return
	}
	if cur != self {
		dest := h.s.sessionNamed(cur)
		if dest == nil || dest.data.Load() == nil || dest.caps&proto.CapClipboard == 0 {
			h.finish(key, false)
			return
		}
	}
	// Ask the client to send its clipboard; the frames come back on its data
	// connection and are routed by fromClient.
	if err := sess.ch.Send(proto.AppendClipSend(nil, key.id)); err != nil {
		h.finish(key, false)
	}
}

func (h *clipHub) finish(key routeKey, ok bool) (route, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rt, known := h.routes[key]
	if !known {
		return rt, false
	}
	delete(h.routes, key)
	if ok && h.ver[rt.to] < rt.ver {
		h.ver[rt.to] = rt.ver
	}
	return rt, true
}

// fromClient routes one frame that arrived on a client's data connection.
func (h *clipHub) fromClient(sess *session, frame []byte) {
	id, ok := xfer.ID(frame)
	if !ok {
		return
	}
	self := h.s.cfg.Name
	key := routeKey{sess.name, id}
	h.mu.Lock()
	rt, known := h.routes[key]
	if !known {
		// Not a clipboard the server asked for - then only files the client
		// sends on its own initiative are accepted.
		dest, isDrop := xfer.DropDest(frame)
		if !isDrop || sess.caps&proto.CapDrop == 0 {
			h.mu.Unlock()
			return
		}
		if dest == "" || dest == sess.name {
			dest = self
		}
		h.nextID++
		rt = route{from: sess.name, to: dest, out: h.nextID, drop: true}
		h.routes[key] = rt
	}
	h.mu.Unlock()

	// From here on the bundle travels under the server's own id, so bundles
	// from different clients can never be confused.
	binary.BigEndian.PutUint32(frame[1:5], rt.out)

	if rt.to == self {
		h.toServer(key, rt, frame)
		return
	}
	// Relay to another client, frame by frame, without touching the disk.
	dest := h.s.sessionNamed(rt.to)
	want := uint32(proto.CapClipboard)
	if rt.drop {
		want = proto.CapDrop
	}
	var err error
	switch {
	case dest == nil:
		err = errors.New("the destination is not connected")
	case dest.caps&want == 0:
		err = errors.New("the destination does not accept this")
	default:
		if ch := dest.data.Load(); ch == nil {
			err = errors.New("the destination has no data connection")
		} else {
			err = ch.Send(frame)
		}
	}
	what := "clipboard"
	if rt.drop {
		what = "files"
	}
	switch {
	case err != nil:
		h.finish(key, false)
		logx.Warnf("%s: %s -> %s stopped: %v", what, rt.from, rt.to, err)
	case xfer.IsEnd(frame):
		if xfer.EndOK(frame) {
			h.finish(key, true)
			h.note(what, "%s -> %s", rt.from, rt.to)
		} else {
			h.finish(key, false)
		}
	}
}

// toServer feeds a frame of a bundle addressed to the server itself.
func (h *clipHub) toServer(key routeKey, rt route, frame []byte) {
	h.recvMu.Lock()
	res, err := h.recv.Frame(frame)
	h.recvMu.Unlock()
	if err != nil {
		h.finish(key, false)
		if errors.Is(err, xfer.ErrTooLarge) {
			h.note("clipboard", "not taken from %s: over the size limit of %s", rt.from, xfer.Size(h.s.cfg.ClipLimit))
		} else {
			logx.Warnf("transfer: receiving from %s failed: %v", rt.from, err)
		}
		return
	}
	if res == nil {
		return
	}
	if res.Drop {
		h.finish(key, true)
		h.note("files", "%s from %s saved to %s", res.Stats, rt.from, h.s.cfg.DropDir)
		return
	}
	if h.local == nil {
		h.finish(key, false)
		return
	}
	if res.Content.Kind == clip.Text {
		err = h.local.WriteText(res.Content.Text)
	} else {
		err = h.local.WriteFiles(res.Content.Files)
	}
	if err != nil {
		h.finish(key, false)
		logx.Warnf("clipboard: cannot set the clipboard: %v", err)
		return
	}
	// What was just written is not a new local copy.
	mark, _ := h.local.Mark()
	h.mu.Lock()
	h.localMark = mark
	h.mu.Unlock()
	h.finish(key, true)
	h.note("clipboard", "%s: %s -> %s", res.Stats, rt.from, rt.to)
}

// senderGone drops what a client was sending when its data connection broke.
func (h *clipHub) senderGone(sess *session) {
	h.mu.Lock()
	var keys []routeKey
	for key := range h.routes {
		if key.from == sess.name {
			keys = append(keys, key)
		}
	}
	h.mu.Unlock()
	for _, key := range keys {
		rt, ok := h.finish(key, false)
		if !ok {
			continue
		}
		if rt.to == h.s.cfg.Name {
			h.recvMu.Lock()
			h.recv.Cancel(rt.out)
			h.recvMu.Unlock()
		} else if dest := h.s.sessionNamed(rt.to); dest != nil {
			if ch := dest.data.Load(); ch != nil {
				ch.Send(xfer.Abort(rt.out))
			}
		}
	}
}

// sendFiles sends files from the server to a client, which saves them in
// its folder for received files. It returns when the transfer is over.
func (h *clipHub) sendFiles(to string, paths []string) error {
	dest := h.s.sessionNamed(to)
	if dest == nil {
		return fmt.Errorf("%q is not connected", to)
	}
	if dest.caps&proto.CapDrop == 0 {
		return fmt.Errorf("%q runs an edgehop version that cannot receive files", to)
	}
	ch := dest.data.Load()
	if ch == nil {
		return fmt.Errorf("%q is not ready to receive files yet", to)
	}
	id := h.newID()
	dest.dataMu.Lock()
	st, err := xfer.SendDrop(ch.Send, id, paths, "")
	dest.dataMu.Unlock()
	if err != nil {
		return err
	}
	h.note("files", "%s sent to %s", st, to)
	return nil
}
