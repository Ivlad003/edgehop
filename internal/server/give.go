package server

import (
	"sync/atomic"
	"time"

	"github.com/Ivlad003/edgehop/internal/input"
	"github.com/Ivlad003/edgehop/internal/logx"
	"github.com/Ivlad003/edgehop/internal/proto"
	"github.com/Ivlad003/edgehop/internal/secure"
)

// Windows of the server itself, shown on a client (remote windows,
// experimental - the other direction of view.go).
//
// The client asks: it opens a connection of its own that starts with
// TViewAsk, and the server answers on it like any other source of windows.
//
// A window can also be sent over on purpose. The server cannot push the
// cursor over an edge during a drag, and on a Mac it cannot even tell which
// window is being dragged - so the request is a key combination: with it
// held, taking the cursor over the edge towards a client makes the window
// the user works in open over there, where the cursor comes in.

const (
	maxGiven  = 6                      // views of this computer's windows at a time
	giveRetry = 400 * time.Millisecond // between two looks for a window during a drag
)

// giveState belongs to the run loop, except for the counter.
type giveState struct {
	open  atomic.Int32
	tried time.Time
}

// sendWindow is called when the cursor is at an edge of the server's own
// desktop. If the key combination for sending a window is held and there is
// a window to send, the cursor crosses and the client is told to show that
// window; sendWindow then returns true. Otherwise nothing has happened.
func (s *Server) sendWindow(ev input.Event) bool {
	if s.cfg.View == nil || s.cfg.PickWindow == nil || s.cfg.WindowKeys == 0 || ev.Mods != s.cfg.WindowKeys {
		return false
	}
	sess := s.byName[s.cfg.Layout.Resolve(s.cfg.Name, ev.Side, s.available)]
	if sess == nil || sess.caps&proto.CapShow == 0 {
		return false
	}
	// During a drag the cursor stays at the edge and reports it again and
	// again; do not look for a window every time.
	now := time.Now()
	if ev.Drag && now.Sub(s.give.tried) < giveRetry {
		return false
	}
	id, w, h := s.cfg.PickWindow()
	if id == 0 {
		s.give.tried = now
		return false
	}
	s.give.tried = time.Time{}
	if ev.Drag && s.cfg.EndDrag != nil {
		s.cfg.EndDrag(ev.Side, ev.Frac) // let go of the window here
	}
	logx.Infof("window sent over to %s", sess.name)
	s.cap.Grab()
	s.enter(sess, ev.Side.Opposite(), ev.Frac)
	s.clipMoved(s.cfg.Name, sess.name)
	sess.send(proto.AppendWindowGo(sess.buf[:0], id, w, h, uint8(ev.Side.Opposite()), ev.Frac))
	return true
}

// inverseKeys turns "this key is sent as that one" around. Where two keys
// are sent as the same one there is no way back, and the key stays as it is.
func inverseKeys(remap map[uint16]uint16) map[uint16]uint16 {
	if len(remap) == 0 {
		return nil
	}
	back := map[uint16]uint16{}
	twice := map[uint16]bool{}
	for from, to := range remap {
		if _, seen := back[to]; seen {
			twice[to] = true
		}
		back[to] = from
	}
	for to := range twice {
		delete(back, to)
	}
	return back
}

// serveView answers a connection on which a client asked for the windows of
// this computer. It returns when the connection is over.
func (s *Server) serveView(ch *secure.Channel, sessionID uint32) {
	defer ch.Close()
	s.idMu.RLock()
	sess := s.byID[sessionID]
	s.idMu.RUnlock()
	if s.cfg.View == nil || sess == nil || !sess.joined.Load() || sess.caps&proto.CapShow == 0 {
		return
	}
	if s.give.open.Add(1) > maxGiven {
		s.give.open.Add(-1)
		logx.Warnf("remote windows: too many windows of this computer are being shown already")
		return
	}
	defer s.give.open.Add(-1)

	// The connection ends with the client's session, and with the server.
	sess.givenMu.Lock()
	if sess.given == nil {
		sess.given = map[*secure.Channel]struct{}{}
	}
	sess.given[ch] = struct{}{}
	sess.givenMu.Unlock()
	defer func() {
		sess.givenMu.Lock()
		delete(sess.given, ch)
		sess.givenMu.Unlock()
	}()
	if sess.closed.Load() {
		return
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-s.done:
			ch.Close()
		case <-done:
		}
	}()
	logx.Debugf("remote windows: %s looks at the windows of this computer", sess.name)
	s.cfg.View.ServeMapped(ch, inverseKeys(sess.remap))
}

// closeGiven ends the views of this computer's windows that a client had
// open; its session is over.
func (sess *session) closeGiven() {
	sess.givenMu.Lock()
	defer sess.givenMu.Unlock()
	for ch := range sess.given {
		ch.Close()
	}
}
