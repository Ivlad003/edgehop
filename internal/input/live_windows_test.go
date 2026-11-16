//go:build windows

package input

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
)

// These tests drive the real Windows input system: they install the
// low-level hooks, feed synthetic keys and mouse movement through Windows and
// check what the hooks report and what Windows itself gets to see. They move
// the cursor, tap Shift and two keys no keyboard has (F23, F24), and for a
// moment take the mouse and keyboard away, so they are opt-in:
//
//	EDGEHOP_LIVE=1 go test ./internal/input/ -run Live -v
//
// They need an interactive desktop.

func requireLiveWindows(t *testing.T) {
	t.Helper()
	if os.Getenv("EDGEHOP_LIVE") != "1" {
		t.Skip("set EDGEHOP_LIVE=1 to test against the real Windows input system (moves the cursor)")
	}
}

// systemSees reports whether Windows itself takes the key for held. A key
// press that a hook swallowed never gets that far.
func systemSees(code uint16) bool {
	scan, _, _ := keys.ToWindows(code)
	vk, _, _ := pMapVirtualKey.Call(uintptr(scan), 1) // scan code -> virtual key
	if vk == 0 {
		return false
	}
	r, _, _ := pGetAsyncKeyState.Call(vk)
	return r&0x8000 != 0
}

func TestLiveWindowsHotkeysAndEdges(t *testing.T) {
	requireLiveWindows(t)
	initProcess()
	orig := cursorPos()
	inj := &winInjector{}
	t.Cleanup(func() {
		inj.ReleaseAll()
		setCursorPos(int(orig.x), int(orig.y))
	})

	ci, err := NewCapture(Options{HideCursor: true})
	if err != nil {
		t.Fatal(err)
	}
	c := ci.(*winCapture)
	defer c.Close()
	time.Sleep(150 * time.Millisecond)
	collect := func() []Event {
		time.Sleep(80 * time.Millisecond)
		return drainEvents(c.events)
	}
	const f23, f24 = keys.F24 - 1, keys.F24
	c.SetHotkeys([]Hotkey{
		{Hotkey: mustHotkey(t, "shift+f23"), RemoteOnly: true},
		{Hotkey: mustHotkey(t, "shift+f24")},
	})
	collect()

	// --- local mode: a hotkey ----------------------------------------------------
	inj.Key(keys.LeftShift, keys.Down)
	inj.Key(f24, keys.Down)
	time.Sleep(40 * time.Millisecond)
	shiftSeen, f24Seen := systemSees(keys.LeftShift), systemSees(f24)
	inj.Key(f24, keys.Down) // auto-repeat
	inj.Key(f24, keys.Up)
	got := collect()
	if len(got) != 1 || got[0] != (Event{Kind: EvHotkey, Code: 1}) {
		t.Fatalf("Shift+F24 in local mode: got %s, want one hotkey event (number 1)", describeEvents(got))
	}
	if !shiftSeen {
		t.Errorf("Shift, pressed before anyone could know it starts a hotkey, did not reach Windows")
	}
	if f24Seen {
		t.Errorf("the key that completes a hotkey reached Windows")
	}
	t.Log("local mode: the hotkey is reported once and its last key is hidden from Windows")

	// The other hotkey only counts in remote mode: here it is ordinary typing.
	inj.Key(f23, keys.Down)
	time.Sleep(40 * time.Millisecond)
	f23Seen := systemSees(f23)
	inj.Key(f23, keys.Up)
	if got := collect(); len(got) != 0 {
		t.Fatalf("a remote-only hotkey in local mode: got %s, want nothing", describeEvents(got))
	}
	if !f23Seen {
		t.Errorf("a remote-only hotkey was swallowed in local mode")
	}

	// --- local mode: the edge event carries the modifier keys and the corner ------
	desk := layout.NewDesktop(Monitors())
	var m layout.Rect // the monitor that forms the right edge of the desktop
	for _, r := range desk.Monitors {
		if m.W == 0 || r.Right() > m.Right() {
			m = r
		}
	}
	b := desk.Bounds
	edgeX, y := m.Right()-1, m.Y+m.H/2
	touchEdge := func() []Event {
		inj.MoveAbs(edgeX-300-b.X, y-b.Y)
		collect()
		inj.MoveAbs(edgeX-b.X, y-b.Y)
		return collect()
	}
	c.SetArmedEdges(layout.Right.Bit())
	got = touchEdge() // Shift is still held
	if len(got) == 0 || got[0].Kind != EvEdge || got[0].Side != layout.Right {
		t.Fatalf("touching the right edge: got %s, want an edge event", describeEvents(got))
	}
	wantCorner := min(m.H/2, m.H-1-m.H/2)
	if e := got[0]; e.Mods != keys.ModShift || e.Span != m.H || e.Corner < wantCorner-1 || e.Corner > wantCorner+1 {
		t.Errorf("edge with Shift held = %+v; want Mods %04b, Span %d, Corner about %d", e, keys.ModShift, m.H, wantCorner)
	}
	inj.Key(keys.LeftShift, keys.Up)
	got = touchEdge()
	if len(got) == 0 || got[0].Kind != EvEdge || got[0].Mods != 0 {
		t.Errorf("edge with no key held: got %+v, want an edge event without modifier keys", got)
	}
	c.SetArmedEdges(0)
	t.Log("local mode: the edge event tells the modifier keys held and the distance to the corner")

	// --- remote mode ---------------------------------------------------------------
	inj.MoveAbs(m.X+123-b.X, m.Y+234-b.Y)
	time.Sleep(60 * time.Millisecond)
	home := cursorPos()
	park := winPoint{int32(metric(smCXScreen) / 2), int32(metric(smCYScreen) / 2)}
	c.Grab(true) // as a hotkey does: remember where the cursor is
	time.Sleep(120 * time.Millisecond)
	if p := cursorPos(); p != park {
		t.Fatalf("in remote mode the cursor is at %d,%d, it should be parked at %d,%d", p.x, p.y, park.x, park.y)
	}
	collect()
	inj.Key(keys.LeftShift, keys.Down)
	sendMouse(12, 7, 0, mouseeventfMove)
	inj.Key(f23, keys.Down)
	inj.Key(f23, keys.Down) // auto-repeat
	inj.Key(f23, keys.Up)
	inj.Key(f24, keys.Down)
	time.Sleep(40 * time.Millisecond)
	shiftSeen = systemSees(keys.LeftShift)
	inj.Key(f24, keys.Up)
	inj.Key(keys.LeftShift, keys.Up)
	got = collect()
	ok := len(got) == 5 &&
		got[0] == (Event{Kind: EvKey, Code: keys.LeftShift, State: keys.Down}) &&
		got[1].Kind == EvMotion && got[1].DX > 0 && got[1].DY > 0 && got[1].Mods == keys.ModShift &&
		got[2] == (Event{Kind: EvHotkey, Code: 0}) &&
		got[3] == (Event{Kind: EvHotkey, Code: 1}) &&
		got[4] == (Event{Kind: EvKey, Code: keys.LeftShift, State: keys.Up})
	if !ok {
		t.Errorf("remote mode: got %s (%+v);\nwant Shift down, a movement with Shift held, hotkey 0, hotkey 1, Shift up", describeEvents(got), got)
	}
	if shiftSeen {
		t.Errorf("a key pressed in remote mode reached Windows")
	}
	t.Log("remote mode: both hotkeys are reported, movement carries the modifier keys, nothing reaches Windows")

	// Back without a warp: the cursor returns to where the hotkey took it from.
	c.Release(0, 0, false)
	time.Sleep(120 * time.Millisecond)
	if p := cursorPos(); p != home {
		t.Errorf("after coming back the cursor is at %d,%d; it was sent away from %d,%d", p.x, p.y, home.x, home.y)
	}
	// After leaving through an edge there is no such place to return to: the
	// cursor stays where it was parked, as it always did.
	c.Grab(false)
	time.Sleep(120 * time.Millisecond)
	c.Release(0, 0, false)
	time.Sleep(120 * time.Millisecond)
	if p := cursorPos(); p != park {
		t.Errorf("after a plain grab and release the cursor is at %d,%d, want the parking place %d,%d", p.x, p.y, park.x, park.y)
	}
	// And local typing works again.
	inj.Key(f23, keys.Down)
	time.Sleep(40 * time.Millisecond)
	f23Seen = systemSees(f23)
	inj.Key(f23, keys.Up)
	if !f23Seen {
		t.Errorf("after returning to local mode keys are still swallowed")
	}
	if got := collect(); len(got) != 0 {
		t.Errorf("local mode reported events after returning: %s", describeEvents(got))
	}
}

// TestLiveWindowsSelfTest runs what `edgehop selftest --grab` runs: in remote
// mode every key, the mouse buttons, the wheel and cursor positioning are
// injected and must come back from the hooks exactly, with nothing reaching
// the desktop.
func TestLiveWindowsSelfTest(t *testing.T) {
	requireLiveWindows(t)
	var out bytes.Buffer
	err := SelfTest(&out, true)
	t.Logf("\n%s", out.String())
	if err != nil {
		t.Fatal(err)
	}
}
