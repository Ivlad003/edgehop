//go:build darwin

package input

import (
	"math"
	"os"
	"testing"
	"time"

	"github.com/ebitengine/purego"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
)

// This test drives the real macOS event system: it posts synthetic input,
// reads the cursor position back and runs the event tap. It moves the cursor
// and briefly swallows input, and it needs the Accessibility permission, so
// it is opt-in:
//
//	EDGEHOP_LIVE=1 go test ./internal/input/ -run Live -v

func macCursor(t *testing.T) func() (float64, float64) {
	t.Helper()
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	var cgEventCreate func(src uintptr) uintptr
	purego.RegisterLibFunc(&cgEventCreate, cg, "CGEventCreate")
	return func() (float64, float64) {
		ev := cgEventCreate(0)
		defer cfRelease(ev)
		p := cgEventGetLocation(ev)
		return p.X, p.Y
	}
}

func waitCaptured(t *testing.T, what string, ch <-chan Event, want ...Event) {
	t.Helper()
	var got []Event
	timeout := time.After(3 * time.Second)
	for len(got) < len(want) {
		select {
		case e := <-ch:
			got = append(got, e)
		case <-timeout:
			t.Fatalf("%s: timed out; got %+v, want %+v", what, got, want)
		}
	}
	select {
	case e := <-ch:
		got = append(got, e)
	case <-time.After(150 * time.Millisecond):
	}
	if len(got) != len(want) {
		t.Fatalf("%s:\n got %+v\nwant %+v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: event %d:\n got %+v\nwant %+v", what, i, got[i], want[i])
		}
	}
}

func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		case <-time.After(200 * time.Millisecond):
			return out
		}
	}
}

func TestLiveMac(t *testing.T) {
	if os.Getenv("EDGEHOP_LIVE") != "1" {
		t.Skip("set EDGEHOP_LIVE=1 to test against the real macOS event system (moves the cursor)")
	}
	if err := macInit(); err != nil {
		t.Fatal(err)
	}
	cursor := macCursor(t)

	ii, err := NewInjector(Options{})
	if err != nil {
		t.Fatal(err)
	}
	inj := ii.(*macInjector)
	defer inj.Close()
	w, h := inj.Size()
	t.Logf("desktop %dx%d, displays %+v", w, h, Monitors())
	if w < 200 || h < 200 {
		t.Fatalf("implausible desktop size %dx%d", w, h)
	}

	// --- injection: posted mouse events must move the real cursor ---------------
	at := func(x, y int) bool {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			cx, cy := cursor()
			if math.Abs(cx-float64(inj.bounds.X+x)) <= 1 && math.Abs(cy-float64(inj.bounds.Y+y)) <= 1 {
				return true
			}
			time.Sleep(10 * time.Millisecond)
		}
		return false
	}
	inj.MoveAbs(200, 150)
	if !at(200, 150) {
		cx, cy := cursor()
		t.Fatalf("a posted mouse move did not move the cursor (it is at %.0f,%.0f, wanted 200,150): "+
			"synthetic input is not permitted for this process (Accessibility)", cx, cy)
	}
	for _, p := range [][2]int{{0, 0}, {w - 1, h - 1}, {w / 2, h / 3}, {17, h - 5}} {
		inj.MoveAbs(p[0], p[1])
		if !at(p[0], p[1]) {
			cx, cy := cursor()
			t.Errorf("MoveAbs(%d,%d): cursor at %.1f,%.1f", p[0], p[1], cx, cy)
		}
	}
	t.Log("injection: absolute cursor positioning is exact")

	// --- capture ---------------------------------------------------------------
	ci, err := NewCapture(Options{HideCursor: true})
	if err != nil {
		t.Fatalf("the event tap could not be created: %v", err)
	}
	c := ci.(*macCapture)
	defer c.Close()
	time.Sleep(300 * time.Millisecond)
	drain(c.Events())

	// Local mode: input passes, only the screen edge is reported.
	inj.MoveAbs(w/2, h/2)
	inj.Key(keys.A, keys.Down)
	inj.Key(keys.A, keys.Up)
	if got := drain(c.Events()); len(got) != 0 {
		t.Fatalf("local mode reported events: %+v", got)
	}
	if !at(w/2, h/2) {
		t.Fatalf("local mode swallowed a mouse move")
	}
	c.SetArmedEdges(layout.Right.Bit())
	inj.MoveAbs(w-1, h/2)
	got := drain(c.Events())
	if len(got) == 0 || got[0].Kind != EvEdge || got[0].Side != layout.Right {
		t.Fatalf("touching the right edge: got %+v, want an edge event", got)
	}
	if f := got[0].Frac; math.Abs(f-0.5) > 0.02 {
		t.Errorf("edge position = %.3f, want about 0.5", f)
	}
	t.Log("capture: the screen edge is detected")

	// Remote mode: everything is swallowed and reported.
	c.Grab()
	time.Sleep(200 * time.Millisecond)
	parkX, parkY := cursor()
	drain(c.Events())
	// A real mouse reports movement without moving the frozen cursor. Imitate
	// that: events at the cursor's own position that carry only a movement.
	mouseDelta := func(dx, dy int64) {
		ev := cgEventCreateMouseEvent(inj.src, evtMouseMoved, parkX, parkY, 0)
		cgEventSetIntegerValueField(ev, fldDeltaX, dx)
		cgEventSetIntegerValueField(ev, fldDeltaY, dy)
		cgEventPost(cgHIDEventTap, ev)
		cfRelease(ev)
	}
	mouseDelta(10, 5)
	mouseDelta(-8, -4)
	waitCaptured(t, "remote motion", c.Events(),
		Event{Kind: EvMotion, DX: 10, DY: 5},
		Event{Kind: EvMotion, DX: -8, DY: -4})
	// A long push in one direction must arrive in full, with nothing lost and
	// nothing thrown back: 60 steps of 25 = 1500 to the right.
	for i := 0; i < 60; i++ {
		mouseDelta(25, 0)
	}
	var total float64
	backwards := 0
	for _, e := range drain(c.Events()) {
		if e.Kind == EvMotion {
			total += e.DX
			if e.DX < 0 {
				backwards++
			}
		}
	}
	if total != 1500 || backwards != 0 {
		t.Errorf("a 1500 px push arrived as %.0f px with %d backward steps", total, backwards)
	}
	if cx, cy := cursor(); math.Abs(cx-parkX) > 2 || math.Abs(cy-parkY) > 2 {
		t.Errorf("the cursor moved to %.0f,%.0f during remote motion (it was frozen at %.0f,%.0f)", cx, cy, parkX, parkY)
	}
	t.Log("capture: remote motion arrives in full and the cursor stays frozen")

	// Every key edgehop can send to a Mac, one at a time.
	tested := 0
	for _, code := range keys.Codes() {
		if _, ok := keys.ToMac(code); !ok || code == keys.CapsLock {
			continue
		}
		inj.Key(code, keys.Down)
		inj.Key(code, keys.Up)
		tested++
		var evs []Event
		deadline := time.After(2 * time.Second)
	collect:
		for len(evs) < 2 {
			select {
			case e := <-c.Events():
				evs = append(evs, e)
			case <-deadline:
				break collect
			}
		}
		ok := len(evs) == 2 &&
			evs[0] == (Event{Kind: EvKey, Code: code, State: keys.Down}) &&
			evs[1] == (Event{Kind: EvKey, Code: code, State: keys.Up})
		if !ok {
			t.Errorf("key %s (%d) came back as %+v", keys.Name(code), code, evs)
		}
	}
	if extra := drain(c.Events()); len(extra) != 0 {
		t.Errorf("unexpected events after the keys: %+v", extra)
	}
	t.Logf("capture: %d keys injected and captured", tested)

	// Key repeat.
	inj.Key(keys.A, keys.Down)
	inj.Key(keys.A, keys.Repeat)
	inj.Key(keys.A, keys.Up)
	waitCaptured(t, "key repeat", c.Events(),
		Event{Kind: EvKey, Code: keys.A, State: keys.Down},
		Event{Kind: EvKey, Code: keys.A, State: keys.Repeat},
		Event{Kind: EvKey, Code: keys.A, State: keys.Up})

	for b := uint8(keys.ButtonLeft); b <= keys.ButtonForward; b++ {
		inj.Button(b, true)
		inj.Button(b, false)
		waitCaptured(t, "mouse button", c.Events(),
			Event{Kind: EvButton, Code: uint16(b), State: 1},
			Event{Kind: EvButton, Code: uint16(b), State: 0})
	}
	inj.Wheel(0, 120)
	inj.Wheel(0, -240)
	inj.Wheel(-240, 0)
	waitCaptured(t, "wheel", c.Events(),
		Event{Kind: EvWheel, WY: 120},
		Event{Kind: EvWheel, WY: -240},
		Event{Kind: EvWheel, WX: -240})
	t.Log("capture: buttons and wheel injected and captured")

	// The hidden cursor must not have wandered far from where it was parked.
	cx, cy := cursor()
	if math.Abs(cx-parkX) > 2 || math.Abs(cy-parkY) > 2 {
		t.Errorf("the cursor moved to %.0f,%.0f while remote (it was frozen at %.0f,%.0f)", cx, cy, parkX, parkY)
	}

	// Back to local: the cursor appears where it re-enters, and input passes again.
	c.Release(layout.Left, 0.5, true)
	time.Sleep(200 * time.Millisecond)
	wantX, wantY := c.desk.Entry(layout.Left, 0.5, 2)
	cx, cy = cursor()
	if math.Abs(cx-float64(wantX)) > 1 || math.Abs(cy-float64(wantY)) > 1 {
		t.Errorf("after returning the cursor is at %.0f,%.0f, want %d,%d", cx, cy, wantX, wantY)
	}
	inj.MoveAbs(123, 234)
	if !at(123, 234) {
		t.Errorf("after returning to local mode mouse moves are still swallowed")
	}
	if got := drain(c.Events()); len(got) != 0 {
		t.Errorf("local mode reported events after returning: %+v", got)
	}
	t.Log("capture: returning to local mode works")
}
