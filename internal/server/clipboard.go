package server

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Ivlad003/edgehop/internal/clip"
	"github.com/Ivlad003/edgehop/internal/logx"
	"github.com/Ivlad003/edgehop/internal/proto"
	"github.com/Ivlad003/edgehop/internal/xfer"
)

// clipHub makes the clipboard follow the cursor.
//
// Every screen holds some version of the shared clipboard. When the cursor
// leaves a screen whose clipboard changed, that content becomes the newest
// version. When the cursor enters a screen that holds an older version than
// the screen it came from, the content is sent along. So whatever was copied
// last is there to paste on the screen the cursor is on - and it travels
// only when it is needed, which matters for large files.
//
// The server is the hub: content between two clients is relayed through it.
type clipHub struct {
	s     *Server
	local clip.Clipboard

	mu        sync.Mutex
	global    uint64            // newest version
	ver       map[string]uint64 // version each screen holds
	localMark string            // the server's clipboard mark at its last sync
	nextID    uint32
	routes    map[uint32]route // bundles in flight from clients
	last      string

	recvMu sync.Mutex
	recv   xfer.Receiver // bundles addressed to the server itself
}

// route says where a bundle requested from a client goes.
type route struct {
	from, to string
	ver      uint64
}

func newClipHub(s *Server) *clipHub {
	return &clipHub{
		s:      s,
		local:  s.cfg.Clipboard,
		ver:    map[string]uint64{},
		routes: map[uint32]route{},
		recv:   xfer.Receiver{Dir: s.cfg.ClipDir, Limit: s.cfg.ClipLimit},
	}
}

func (h *clipHub) status() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

func (h *clipHub) note(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	h.mu.Lock()
	h.last = msg
	h.mu.Unlock()
	logx.Infof("clipboard: %s", msg)
}

// switched is called by the run loop when the cursor moved. It must not
// block: reading a clipboard or sending files can take a while.
func (h *clipHub) switched(from, to string) {
	if from == h.s.cfg.Name {
		go h.fromServer(to)
	}
	// A client reports about its own clipboard with TClipState, in reply to
	// the TLeave it was just sent; see clientState.
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
	if dest == nil {
		return
	}
	ch := dest.data.Load()
	if ch == nil {
		return // this client does not share its clipboard
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
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	h.mu.Unlock()

	dest.dataMu.Lock()
	st, err := xfer.Send(ch.Send, id, content, h.s.cfg.ClipLimit)
	dest.dataMu.Unlock()
	switch {
	case err == nil:
		h.setVersion(to, version)
		h.note("%s: %s -> %s", st, self, to)
	case errors.Is(err, xfer.ErrTooLarge):
		h.setVersion(to, version) // do not try again on every switch
		h.note("not shared with %s: %s is over the size limit of %s", to, xfer.Size(st.Bytes), xfer.Size(h.s.cfg.ClipLimit))
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
	cur := h.s.Focus()
	if cur == "" {
		cur = self
	}
	h.mu.Lock()
	if changed {
		h.global++
		h.ver[sess.name] = h.global
	}
	version := h.ver[sess.name]
	need := cur != sess.name && version > h.ver[cur]
	var id uint32
	if need {
		h.nextID++
		id = h.nextID
		h.routes[id] = route{from: sess.name, to: cur, ver: version}
	}
	h.mu.Unlock()
	if !need {
		return
	}
	if cur != self {
		if dest := h.s.sessionNamed(cur); dest == nil || dest.data.Load() == nil {
			h.finish(id, false)
			return
		}
	}
	// Ask the client to send its clipboard; the frames come back on its data
	// connection and are routed by fromClient.
	if err := sess.ch.Send(proto.AppendClipSend(nil, id)); err != nil {
		h.finish(id, false)
	}
}

func (h *clipHub) finish(id uint32, ok bool) (route, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rt, known := h.routes[id]
	if !known {
		return rt, false
	}
	delete(h.routes, id)
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
	h.mu.Lock()
	rt, known := h.routes[id]
	h.mu.Unlock()
	if !known || rt.from != sess.name {
		return // not a bundle this client was asked for
	}
	if rt.to == h.s.cfg.Name {
		h.toServer(id, rt, frame)
		return
	}
	// Relay to another client, frame by frame, without touching the disk.
	dest := h.s.sessionNamed(rt.to)
	var err error
	if dest == nil {
		err = errors.New("the destination disconnected")
	} else if ch := dest.data.Load(); ch == nil {
		err = errors.New("the destination has no data connection")
	} else {
		err = ch.Send(frame)
	}
	switch {
	case err != nil:
		h.finish(id, false)
		logx.Debugf("clipboard: relay %s -> %s stopped: %v", rt.from, rt.to, err)
	case xfer.IsEnd(frame):
		if ok := len(frame) > 5 && frame[5] == 0; ok {
			h.finish(id, true)
			h.note("%s -> %s", rt.from, rt.to)
		} else {
			h.finish(id, false)
		}
	}
}

// toServer feeds a frame of a bundle addressed to the server itself.
func (h *clipHub) toServer(id uint32, rt route, frame []byte) {
	h.recvMu.Lock()
	res, err := h.recv.Frame(frame)
	h.recvMu.Unlock()
	if err != nil {
		h.finish(id, false)
		if errors.Is(err, xfer.ErrTooLarge) {
			h.note("not taken from %s: over the size limit of %s", rt.from, xfer.Size(h.s.cfg.ClipLimit))
		} else {
			logx.Warnf("clipboard: receiving from %s failed: %v", rt.from, err)
		}
		return
	}
	if res == nil {
		return
	}
	if res.Content.Kind == clip.Text {
		err = h.local.WriteText(res.Content.Text)
	} else {
		err = h.local.WriteFiles(res.Content.Files)
	}
	if err != nil {
		h.finish(id, false)
		logx.Warnf("clipboard: cannot set the clipboard: %v", err)
		return
	}
	// What was just written is not a new local copy.
	mark, _ := h.local.Mark()
	h.mu.Lock()
	h.localMark = mark
	h.mu.Unlock()
	h.finish(id, true)
	h.note("%s: %s -> %s", res.Stats, rt.from, rt.to)
}

// senderGone drops what a client was sending when its data connection broke.
func (h *clipHub) senderGone(sess *session) {
	h.mu.Lock()
	var ids []uint32
	toServer := false
	for id, rt := range h.routes {
		if rt.from == sess.name {
			ids = append(ids, id)
			if rt.to == h.s.cfg.Name {
				toServer = true
			}
		}
	}
	h.mu.Unlock()
	for _, id := range ids {
		if rt, ok := h.finish(id, false); ok && rt.to != h.s.cfg.Name {
			if dest := h.s.sessionNamed(rt.to); dest != nil {
				if ch := dest.data.Load(); ch != nil {
					ch.Send(xfer.Abort(id))
				}
			}
		}
	}
	if toServer {
		h.recvMu.Lock()
		h.recv.Abort()
		h.recvMu.Unlock()
	}
}
