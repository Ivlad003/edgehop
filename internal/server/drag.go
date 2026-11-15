package server

import (
	"math"
	"time"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
	"github.com/Ivlad003/edgehop/internal/logx"
	"github.com/Ivlad003/edgehop/internal/proto"
)

// Dragging a window to the server (remote windows, experimental).
//
// While a mouse button is held the cursor never leaves a screen - that would
// tear a drag apart. But a window that is pushed against the edge towards
// the server, and held there, is a request: "I want this window over there".
// The server then asks the client which window is being moved; if there is
// one, the drag is ended, the cursor comes to the server, and the window is
// opened there.
//
// Touching the edge is not enough: Windows uses that for snapping a window
// to half the screen, and a quick fling always overshoots. The window has to
// be held at the edge for a moment and pushed further.

const (
	defaultDragHold = 300 * time.Millisecond // at the edge for this long before pushing counts
	defaultDragPush = 80                     // then pushed this many pixels further
)

// DraggedWindow is a client's window that the user pushed over to the server.
type DraggedWindow struct {
	Peer string // the client's screen name
	ID   uint64 // the window, as the client numbers it
	W, H int    // its size in the client's pixels
	// Side and Frac say where the cursor entered the server's desktop: which
	// edge, and how far along it (0..1).
	Side layout.Side
	Frac float64
}

// dragState belongs to the run loop.
type dragState struct {
	atEdge  bool
	side    layout.Side
	since   time.Time // when the cursor reached that edge
	push    float64   // pixels pushed beyond it after the hold time
	frac    float64   // where along the edge
	asked   bool      // the client was asked during this press
	pending uint32    // the question that is waiting for its answer
}

// dragReply is the client's answer, on its way to the run loop.
type dragReply struct {
	id     uint32
	window uint64
	w, h   int
}

// dragPushed is called for every cursor movement on a client while a mouse
// button is held; nx, ny is where the cursor would go without the screen's
// edges.
func (s *Server) dragPushed(sess *session, nx, ny, maxX, maxY float64) {
	if s.cfg.OnWindowDrag == nil || !s.cfg.ShowsWindows || sess.caps&proto.CapDrag == 0 || s.buttons != 1<<keys.ButtonLeft {
		return
	}
	var side layout.Side
	var over float64
	switch {
	case nx < 0:
		side, over = layout.Left, -nx
	case nx > maxX:
		side, over = layout.Right, nx-maxX
	case ny < 0:
		side, over = layout.Top, -ny
	case ny > maxY:
		side, over = layout.Bottom, ny-maxY
	default:
		s.drag = dragState{asked: s.drag.asked} // away from the edge: start over
		return
	}
	if s.cfg.Layout.Resolve(sess.name, side, s.available) != s.cfg.Name {
		return // that edge does not lead to this computer
	}
	now := time.Now()
	if !s.drag.atEdge || s.drag.side != side {
		s.drag = dragState{atEdge: true, side: side, since: now, asked: s.drag.asked}
		return
	}
	hold, push := s.cfg.DragHold, s.cfg.DragPush
	if hold <= 0 {
		hold = defaultDragHold
	}
	if push <= 0 {
		push = defaultDragPush
	}
	if now.Sub(s.drag.since) < hold {
		return
	}
	s.drag.push += over
	if s.drag.push < push || s.drag.asked {
		return
	}
	s.drag.asked = true // one question per press, whatever the answer
	s.dragSeq++
	s.drag.pending = s.dragSeq
	s.drag.frac = 0.5
	if side == layout.Left || side == layout.Right {
		if maxY > 0 {
			s.drag.frac = math.Max(0, math.Min(ny, maxY)) / maxY
		}
	} else if maxX > 0 {
		s.drag.frac = math.Max(0, math.Min(nx, maxX)) / maxX
	}
	sess.send(proto.AppendDragQuery(sess.buf[:0], s.dragSeq))
}

// dragAnswered handles the client's answer in the run loop.
func (s *Server) dragAnswered(sess *session, r dragReply) {
	if s.focus != sess || r.id == 0 || s.drag.pending != r.id {
		return // the cursor moved on, or an answer to an older question
	}
	s.drag.pending = 0
	if r.window == 0 || s.buttons == 0 {
		return // no window is being moved, or the button was released meanwhile
	}
	side, frac := s.drag.side, s.drag.frac
	s.drag = dragState{}
	logx.Infof("window dragged over from %s", sess.name)
	s.clipMoved(sess.name, s.cfg.Name) // before the client hears about it
	s.leaveRemote()                    // releases the button there: the drag ends at the edge
	s.setFocus(nil)
	s.cap.Release(side.Opposite(), frac, true)
	go s.cfg.OnWindowDrag(DraggedWindow{
		Peer: sess.name, ID: r.window, W: r.w, H: r.h,
		Side: side.Opposite(), Frac: frac,
	})
}
