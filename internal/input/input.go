// Package input talks to the operating system: it captures the physical
// mouse and keyboard on the server and injects events on clients.
//
// The platform independent parts live in this file; every operating system
// has its own capture_*.go / inject_*.go implementation.
package input

import (
	"sync/atomic"
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
	// EvHotkey: in either mode, one of the hotkeys given to SetHotkeys was
	// pressed; Code is its index in that list.
	EvHotkey
)

// Event is one captured input event.
type Event struct {
	Kind   EventKind
	Side   layout.Side // EvEdge
	Frac   float64     // EvEdge: position along the edge, 0..1
	DX, DY float64     // EvMotion
	WX, WY int32       // EvWheel
	Code   uint16      // EvButton: keys.Button*, EvKey: key code, EvHotkey: index
	State  uint8       // EvButton: 0/1, EvKey: keys.Up/Down/Repeat
	// EvEdge, EvMotion: the modifier keys held at that moment (keys.Mod*).
	Mods uint8
	// EvEdge: how far the cursor is, in pixels along the edge, from the
	// nearest corner of the monitor it is on, and how long that edge is.
	Corner, Span int
	// EvHotkey: a mouse button is held down - the user is dragging something.
	Drag bool
}

// Hotkey is a key combination the capture watches for.
type Hotkey struct {
	keys.Hotkey
	// RemoteOnly limits it to remote mode; in local mode the keys reach the
	// local desktop as usual.
	RemoteOnly bool
}

// Capture watches the physical mouse and keyboard of the server.
//
// It has two modes. In local mode input reaches the local desktop as usual
// and the only events delivered are EvEdge and EvHotkey. In remote mode
// (after Grab) the input is swallowed - the local desktop no longer sees it -
// and delivered as events to be forwarded to another computer.
type Capture interface {
	// Events is the stream of captured events.
	Events() <-chan Event
	// Grab switches to remote mode. home tells that the cursor is sent away
	// from wherever it is (a hotkey), not through an edge: a later Release
	// without a warp then puts it back at that very place.
	Grab(home bool)
	// Release switches back to local mode. If warp is set, the cursor is
	// placed where it enters the desktop: on the given side, at frac.
	Release(side layout.Side, frac float64, warp bool)
	// SetArmedEdges selects which edges produce EvEdge (a mask of Side.Bit()).
	SetArmedEdges(mask uint8)
	// SetHotkeys replaces the key combinations that produce EvHotkey. The key
	// that completes one never reaches any desktop.
	SetHotkeys(list []Hotkey)
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

// Options configure the platform backends.
type Options struct {
	// HideCursor hides the server's cursor while it controls another screen.
	HideCursor bool
	// Width, Height override the detected desktop size when non-zero.
	Width, Height int
	// PointerSpeed and PointerAccel shape cursor motion where edgehop has to
	// move the cursor itself (Linux server).
	PointerSpeed float64
	PointerAccel float64
}

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

// keyboard follows the physical keyboard of the server in both modes: which
// keys are held, and whether a key press completes a hotkey.
//
// The key that completes a hotkey is hidden from the local desktop and from
// the remote computer alike, from its press to its release (auto-repeat
// included), so a hotkey never types anything anywhere. The modifier keys of
// the hotkey are not hidden: they were pressed before anyone could know.
//
// Everything but the hotkey list belongs to the capture's own thread (or is
// guarded by its lock).
type keyboard struct {
	hotkeys atomic.Pointer[[]Hotkey]
	down    [keys.MaxCode]bool // physically held
	far     [keys.MaxCode]bool // ... and pressed in remote mode: the local system never saw it
	eaten   [keys.MaxCode]bool // its press completed a hotkey
}

func (k *keyboard) setHotkeys(list []Hotkey) {
	cp := append([]Hotkey(nil), list...)
	k.hotkeys.Store(&cp)
}

// onKey processes one physical key event, before anything else looks at it.
//
//	hit:     the index of the hotkey this press completed, or -1
//	swallow: hide the event from the local desktop and from the remote side
func (k *keyboard) onKey(remote bool, code uint16, down bool) (hit int, swallow bool) {
	if code == 0 || code >= keys.MaxCode {
		return -1, false
	}
	if !down {
		k.down[code], k.far[code] = false, false
		if k.eaten[code] {
			k.eaten[code] = false
			return -1, true
		}
		return -1, false
	}
	if k.eaten[code] {
		return -1, true // auto-repeat of the key that completed a hotkey
	}
	if k.down[code] {
		return -1, false // auto-repeat
	}
	k.down[code], k.far[code] = true, remote
	list := k.hotkeys.Load()
	if list == nil {
		return -1, false
	}
	// A hotkey also matches with more modifier keys held than it asks for,
	// so "ctrl+2" and "ctrl+alt+2" both match Ctrl+Alt+2: the one that asks
	// for more keys is the one that was meant.
	held := func(c uint16) bool { return c < keys.MaxCode && k.down[c] }
	hit = -1
	for i, h := range *list {
		if h.RemoteOnly && !remote {
			continue
		}
		if h.Match(code, held) && (hit < 0 || len(h.Mods) > len((*list)[hit].Mods)) {
			hit = i
		}
	}
	if hit < 0 {
		return -1, false
	}
	k.eaten[code] = true
	return hit, true
}

// mods returns the modifier keys that are held (keys.Mod*).
func (k *keyboard) mods() uint8 {
	var m uint8
	for i, pair := range keys.ModKeys {
		if k.down[pair[0]] || k.down[pair[1]] {
			m |= 1 << i
		}
	}
	return m
}

// keep is for local mode, where the operating system sees the keyboard too:
// it forgets the modifier keys outside mask, the ones the system says are not
// held. A release can go unnoticed - Windows, for one, does not show it to a
// hook while the lock screen is up - and a modifier believed to be held for
// ever would fire hotkeys by itself. A key pressed in remote mode stays: the
// system never heard of it, so it cannot tell.
func (k *keyboard) keep(mask uint8) {
	for i, pair := range keys.ModKeys {
		if mask&(1<<i) != 0 {
			continue
		}
		for _, c := range pair {
			if !k.far[c] {
				k.down[c] = false
			}
		}
	}
}

// reset forgets every key.
func (k *keyboard) reset() {
	k.down = [keys.MaxCode]bool{}
	k.far = [keys.MaxCode]bool{}
	k.eaten = [keys.MaxCode]bool{}
}

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
func (n *NullCapture) Grab(bool)                          {}
func (n *NullCapture) Release(layout.Side, float64, bool) {}
func (n *NullCapture) SetArmedEdges(uint8)                {}
func (n *NullCapture) SetHotkeys([]Hotkey)                {}
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
