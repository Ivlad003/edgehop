// Package input talks to the operating system: it captures the physical
// mouse and keyboard on the server and injects events on clients.
//
// The platform independent parts live in this file; every operating system
// has its own capture_*.go / inject_*.go implementation.
package input

import (
	"unsafe"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
)

// EventKind tells what a captured Event is.
type EventKind uint8

const (
	// EvEdge: in local mode, the cursor touched an armed edge of the desktop.
	EvEdge EventKind = iota + 1
	// EvMotion: in remote mode, the mouse moved by DX,DY.
	EvMotion
	// EvButton: in remote mode, a mouse button changed (Code, State).
	EvButton
	// EvWheel: in remote mode, the wheel moved by WX,WY (1/120 of a notch;
	// positive WY is "up / away from the user", positive WX is "right").
	EvWheel
	// EvKey: in remote mode, a key changed (Code, State = keys.Up/Down/Repeat).
	EvKey
)

// Event is one captured input event.
type Event struct {
	Kind   EventKind
	Side   layout.Side // EvEdge
	Frac   float64     // EvEdge: position along the edge, 0..1
	DX, DY float64     // EvMotion
	WX, WY int32       // EvWheel
	Code   uint16      // EvButton: keys.Button*, EvKey: key code
	State  uint8       // EvButton: 0/1, EvKey: keys.Up/Down/Repeat
	// EvEdge: the modifier keys held at that moment (keys.Mod*). Drag: the left mouse
	// button is held too - the user is dragging something. An edge is only
	// reported during a drag while modifier keys are held; a plain drag never
	// leaves the screen.
	Mods uint8
	Drag bool
}

// Capture watches the physical mouse and keyboard of the server.
//
// It has two modes. In local mode input reaches the local desktop as usual
// and the only events delivered are EvEdge. In remote mode (after Grab) the
// input is swallowed - the local desktop no longer sees it - and delivered as
// events to be forwarded to another computer.
type Capture interface {
	// Events is the stream of captured events.
	Events() <-chan Event
	// Grab switches to remote mode.
	Grab()
	// Release switches back to local mode. If warp is set, the cursor is
	// placed where it enters the desktop: on the given side, at frac.
	Release(side layout.Side, frac float64, warp bool)
	// SetArmedEdges selects which edges produce EvEdge (a mask of Side.Bit()).
	SetArmedEdges(mask uint8)
	// Size is the size of the whole desktop in pixels.
	Size() (w, h int)
	// Describe returns a short human readable description of the backend.
	Describe() string
	Close()
}

// Injector produces input on a client.
type Injector interface {
	// Size is the size of the whole desktop in pixels.
	Size() (w, h int)
	// MoveAbs moves the cursor to x,y, where 0,0 is the top-left corner of
	// the desktop and Size() is just past the bottom-right corner.
	MoveAbs(x, y int)
	Button(button uint8, down bool)
	// Wheel scrolls by dx,dy in 1/120 of a notch.
	Wheel(dx, dy int)
	Key(code uint16, state uint8)
	// ReleaseAll releases every key and button that is still held down.
	ReleaseAll()
	Describe() string
	Close()
}

// DragEnder is implemented by injectors that can end a drag the user is doing
// with the real mouse: the dragged thing is taken to x,y (desktop pixels, as
// for MoveAbs) and let go there. The real button may stay held; its release
// then means nothing any more.
type DragEnder interface {
	EndDrag(x, y int)
}

// Options configure the platform backends.
type Options struct {
	// Own is for an injector that runs next to a Capture in the same program
	// (a server that lets its own windows be controlled from a client): what
	// it injects is marked, and the capture lets it through untouched instead
	// of taking it for the user's mouse and keyboard.
	Own bool
	// HideCursor hides the server's cursor while it controls another screen.
	HideCursor bool
	// Width, Height override the detected desktop size when non-zero.
	Width, Height int
	// PointerSpeed and PointerAccel shape cursor motion where edgehop has to
	// move the cursor itself (Linux server).
	PointerSpeed float64
	PointerAccel float64
}

// ownMark is what an injector created with Options.Own writes into its
// events ("ehop"), and what the capture looks for.
const ownMark = 0x65686F70

// keyTracker decides what happens to each physical key event on the server.
//
// The difficulty is keys that are held down while the cursor crosses to
// another computer. A key pressed locally must also be released locally,
// otherwise the local desktop believes it is still down; and such a key must
// not appear on the remote side as a release without a press.
type keyTracker struct {
	localDown  [keys.MaxCode]bool // pressed while in local mode, not yet released
	remoteDown [keys.MaxCode]bool // pressed while in remote mode, forwarded
}

// onKey processes one key event.
//
//	forward: send the event (with the returned state) to the remote computer
//	swallow: hide the event from the local desktop
func (t *keyTracker) onKey(remote bool, code uint16, down bool) (forward bool, state uint8, swallow bool) {
	if code == 0 || code >= keys.MaxCode {
		return false, 0, remote
	}
	if !remote {
		t.localDown[code] = down
		if !down {
			t.remoteDown[code] = false
		}
		return false, 0, false
	}
	if down {
		if t.localDown[code] {
			// Still held from before the switch: it belongs to the local desktop.
			return false, 0, true
		}
		state = keys.Down
		if t.remoteDown[code] {
			state = keys.Repeat
		}
		t.remoteDown[code] = true
		return true, state, true
	}
	if t.localDown[code] {
		// Released on the far side: let the local desktop see the release.
		t.localDown[code] = false
		return false, 0, false
	}
	if t.remoteDown[code] {
		t.remoteDown[code] = false
		return true, keys.Up, true
	}
	return false, 0, true
}

// clearRemote forgets the keys forwarded to the remote side. The server has
// already released them there when it switched away.
func (t *keyTracker) clearRemote() { t.remoteDown = [keys.MaxCode]bool{} }

// emit delivers an event without ever blocking the capture thread.
func emit(ch chan Event, ev Event) {
	select {
	case ch <- ev:
	default: // the consumer is stuck; dropping is better than freezing input
	}
}

// ptr converts an address received from the operating system into a pointer.
func ptr(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

// ---------------------------------------------------------------------------
// Null backends: used with --no-input and in tests.

// NullCapture never produces events.
type NullCapture struct {
	W, H int
	ch   chan Event
}

func NewNullCapture(w, h int) *NullCapture {
	return &NullCapture{W: w, H: h, ch: make(chan Event)}
}

func (n *NullCapture) Events() <-chan Event               { return n.ch }
func (n *NullCapture) Grab()                              {}
func (n *NullCapture) Release(layout.Side, float64, bool) {}
func (n *NullCapture) SetArmedEdges(uint8)                {}
func (n *NullCapture) Size() (int, int)                   { return n.W, n.H }
func (n *NullCapture) Describe() string                   { return "none (--no-input)" }
func (n *NullCapture) Close()                             {}

// NullInjector discards everything.
type NullInjector struct{ W, H int }

func (n *NullInjector) Size() (int, int)   { return n.W, n.H }
func (n *NullInjector) MoveAbs(int, int)   {}
func (n *NullInjector) Button(uint8, bool) {}
func (n *NullInjector) Wheel(int, int)     {}
func (n *NullInjector) Key(uint16, uint8)  {}
func (n *NullInjector) ReleaseAll()        {}
func (n *NullInjector) Describe() string   { return "none (--no-input)" }
func (n *NullInjector) Close()             {}
