package server

import (
	"testing"
	"time"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
)

// guarded returns a server that only knows its protection settings, and a
// clock the test moves by hand.
func guarded(cfg Config) (*Server, func(time.Duration)) {
	now := time.Unix(1700000000, 0)
	s := &Server{cfg: cfg, now: func() time.Time { return now }}
	return s, func(d time.Duration) { now = now.Add(d) }
}

const ms = time.Millisecond

func TestNoProtectionByDefault(t *testing.T) {
	s, _ := guarded(Config{})
	s.now = func() time.Time { t.Fatal("the clock is not needed without a delay"); return time.Time{} }
	for side := layout.Left; side <= layout.Bottom; side++ {
		if !s.mayCross(side, 0, 1080, 0) {
			t.Fatalf("%s: with nothing configured the cursor crosses at the first touch, corners included", side)
		}
	}
}

func TestSwitchDelay(t *testing.T) {
	s, wait := guarded(Config{SwitchDelay: 250 * ms})
	press := func(side layout.Side) bool { return s.mayCross(side, 500, 1000, 0) }

	// A steady press: mouse movements a few milliseconds apart.
	for at := 0; at < 250; at += 10 {
		if press(layout.Right) {
			t.Fatalf("crossed after %d ms, the delay is 250 ms", at)
		}
		wait(10 * ms)
	}
	if !press(layout.Right) {
		t.Fatal("did not cross after pressing for 250 ms")
	}

	// A press that stops and starts again counts from the start again.
	s.press = edgePress{}
	press(layout.Right)
	wait(140 * ms)
	press(layout.Right) // 140 ms: still the same press
	wait(pressGap + ms) // the hand rested
	if press(layout.Right) {
		t.Fatal("crossed although the press was interrupted")
	}
	wait(140 * ms)
	press(layout.Right)
	wait(120 * ms)
	if !press(layout.Right) {
		t.Fatal("did not cross 260 ms into the second press")
	}

	// Every edge has its own count: pressing into a corner where two
	// screens meet reaches one of them, and one edge does not pay for another.
	s.press = edgePress{}
	press(layout.Right)
	press(layout.Top)
	wait(130 * ms)
	press(layout.Right)
	press(layout.Top)
	wait(130 * ms)
	if !press(layout.Right) || !press(layout.Top) {
		t.Fatal("two edges pressed together must both be ready after the delay")
	}
	s.press = edgePress{}
	press(layout.Left)
	wait(130 * ms)
	press(layout.Left)
	wait(130 * ms)
	if press(layout.Bottom) {
		t.Fatal("the time pressed against the left edge was counted for the bottom edge")
	}

	// Moving away from the edge ends the press.
	s.press = edgePress{}
	press(layout.Right)
	wait(100 * ms)
	press(layout.Right)
	s.press.stop(layout.Right) // what a movement back into the screen does
	wait(100 * ms)
	press(layout.Right)
	wait(100 * ms)
	if press(layout.Right) {
		t.Fatal("crossed 100 ms after coming back to the edge")
	}
}

func TestSwitchKeys(t *testing.T) {
	s, _ := guarded(Config{SwitchMods: keys.ModCtrl | keys.ModShift})
	cases := map[uint8]bool{
		0:                                  false,
		keys.ModCtrl:                       false,
		keys.ModShift:                      false,
		keys.ModAlt | keys.ModMeta:         false,
		keys.ModCtrl | keys.ModShift:       true,
		keys.ModCtrl | keys.ModShift | 0x8: true, // and one more key
	}
	for mods, want := range cases {
		if got := s.mayCross(layout.Left, 500, 1000, mods); got != want {
			t.Errorf("with the keys %04b held: crossing = %v, want %v", mods, got, want)
		}
	}
}

func TestSwitchCorner(t *testing.T) {
	s, _ := guarded(Config{SwitchCorner: 40})
	for corner, want := range map[int]bool{0: false, 39: false, 40: true, 500: true} {
		if got := s.mayCross(layout.Bottom, corner, 1080, 0); got != want {
			t.Errorf("%d pixels from a corner: crossing = %v, want %v", corner, got, want)
		}
	}
	// On a short edge the corners never take more than a quarter each.
	for corner, want := range map[int]bool{0: false, 24: false, 25: true, 50: true} {
		if got := s.mayCross(layout.Bottom, corner, 100, 0); got != want {
			t.Errorf("a 100 pixel edge, %d pixels from a corner: crossing = %v, want %v", corner, got, want)
		}
	}
	if cornerZone(40, 0) != 40 || cornerZone(0, 1000) != 0 || cornerZone(-3, 1000) != 0 || cornerZone(1000, 1200) != 300 {
		t.Errorf("cornerZone: %d %d %d %d", cornerZone(40, 0), cornerZone(0, 1000), cornerZone(-3, 1000), cornerZone(1000, 1200))
	}
}

// With everything switched on, the delay only counts while the keys are held
// and the cursor is away from the corners.
func TestProtectionCombined(t *testing.T) {
	s, wait := guarded(Config{SwitchDelay: 200 * ms, SwitchMods: keys.ModCtrl, SwitchCorner: 40})
	// Pressing against the edge without the key: time does not count.
	for i := 0; i < 30; i++ {
		if s.mayCross(layout.Right, 500, 1000, 0) {
			t.Fatal("crossed without the key")
		}
		wait(10 * ms)
	}
	// The key goes down: the wait starts now.
	if s.mayCross(layout.Right, 500, 1000, keys.ModCtrl) {
		t.Fatal("crossed the moment the key went down, the delay was skipped")
	}
	wait(100 * ms)
	// The cursor slides into the corner zone: the wait starts over.
	if s.mayCross(layout.Right, 10, 1000, keys.ModCtrl) {
		t.Fatal("crossed in the corner")
	}
	wait(100 * ms)
	if s.mayCross(layout.Right, 500, 1000, keys.ModCtrl) {
		t.Fatal("the time before the corner was counted")
	}
	wait(100 * ms)
	s.mayCross(layout.Right, 500, 1000, keys.ModCtrl)
	wait(100 * ms)
	if !s.mayCross(layout.Right, 500, 1000, keys.ModCtrl) {
		t.Fatal("did not cross after 200 ms with the key held, away from the corners")
	}
}
