package server_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Ivlad003/edgehop/internal/input"
	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
	"github.com/Ivlad003/edgehop/internal/server"
)

func hotkey(t *testing.T, s string) keys.Hotkey {
	t.Helper()
	h, err := keys.ParseHotkey(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// stays checks that the cursor is still on the given screen a moment after
// the events that must not have moved it were sent.
func (r *rig) stays(want, why string) {
	r.t.Helper()
	time.Sleep(60 * time.Millisecond)
	if got := r.srv.Focus(); got != want {
		r.t.Fatalf("%s: the cursor is on %q, it should have stayed on %q", why, got, want)
	}
}

// A hotkey moves the cursor to its screen from anywhere: to the middle the
// first time, afterwards to where the cursor last was on that screen.
func TestHotkeyJumpsBetweenScreens(t *testing.T) {
	r := startServer(t, "desk", []string{"lap", "desk", "mac", "tv"}, map[string]server.ScreenOpts{
		"mac": {Hotkey: hotkey(t, "ctrl+alt+2")},
		"lap": {Hotkey: hotkey(t, "ctrl+alt+1")},
		"tv":  {Hotkey: hotkey(t, "ctrl+alt+3")},
	})
	// What the capture is asked to watch for: the release hotkey first, and
	// only while another screen has the cursor; then one per screen.
	var list []input.Hotkey
	eventually(t, "the hotkeys to be registered", func() bool {
		list = r.cap.registered()
		return len(list) == 4
	})
	if !list[0].RemoteOnly || list[0].Key != keys.Backspace {
		t.Fatalf("hotkey 0 = %+v, want the release hotkey, in remote mode only", list[0])
	}
	const toLap, toMac, toTV = 1, 2, 3
	for i, key := range map[int]uint16{toLap: keys.Num1, toMac: keys.Num1 + 1, toTV: keys.Num1 + 2} {
		if list[i].RemoteOnly || list[i].Key != key || len(list[i].Mods) != 2 {
			t.Fatalf("hotkey %d = %+v", i, list[i])
		}
	}

	mac, _ := r.connect("mac", 1000, 500)
	lap, _ := r.connect("lap", 800, 600)

	// "tv" is switched off: its hotkey does nothing.
	r.cap.hotkey(toTV)
	r.stays("", "the hotkey of a screen that is not connected")

	// From the server's desktop to the middle of "mac".
	r.cap.hotkey(toMac)
	r.waitFocus("mac")
	waitPos(t, mac, 500, 250)
	if homes := r.cap.grabbed(); len(homes) != 1 || !homes[0] {
		t.Fatalf("grabs = %v, want one that remembers where the cursor was", homes)
	}
	r.cap.move(100, 40)
	waitPos(t, mac, 600, 290)

	// On to "lap", with the modifier keys of the hotkey still held: "mac"
	// must not be left with them pressed.
	r.cap.key(keys.LeftCtrl, keys.Down)
	r.cap.key(keys.LeftAlt, keys.Down)
	r.cap.hotkey(toLap)
	r.waitFocus("lap")
	waitPos(t, lap, 400, 300)
	eventually(t, "the held keys to be released on mac", func() bool {
		ev := mac.events()
		return strings.Contains(ev, "key leftctrl 0") && strings.Contains(ev, "key leftalt 0") && strings.Contains(ev, "releaseall")
	})
	// The releases of those keys come later and belong to no screen.
	r.cap.key(keys.LeftAlt, keys.Up)
	r.cap.key(keys.LeftCtrl, keys.Up)
	r.cap.hotkey(toLap) // already there
	r.cap.move(-50, 10)
	waitPos(t, lap, 350, 310)
	if ev := lap.events(); strings.Contains(ev, "key") {
		t.Fatalf("lap received key events it has nothing to do with: %s", ev)
	}
	if homes := r.cap.grabbed(); len(homes) != 1 {
		t.Fatalf("grabs = %v: moving between two clients must not grab again", homes)
	}

	// Back to "mac": the cursor is where it was left.
	r.cap.hotkey(toMac)
	r.waitFocus("mac")
	waitPos(t, mac, 600, 290)
	eventually(t, "lap to be told that the cursor left", func() bool { return lap.has("releaseall") })

	// Not in the middle of a drag - whether the button went down on this
	// screen or, as the capture reports it, before the cursor got here.
	r.cap.button(keys.ButtonLeft, true)
	r.cap.hotkey(toLap)
	r.cap.move(5, 0)
	waitPos(t, mac, 605, 290)
	r.stays("mac", "a hotkey during a drag")
	r.cap.button(keys.ButtonLeft, false)
	r.cap.ch <- input.Event{Kind: input.EvHotkey, Code: toLap, Drag: true}
	r.cap.ch <- input.Event{Kind: input.EvHotkey, Code: 99} // not a hotkey at all
	r.cap.move(5, 0)
	waitPos(t, mac, 610, 290)
	r.stays("mac", "a hotkey while the capture reports a drag")

	// The release hotkey brings the cursor home, without placing it anywhere.
	r.cap.hotkey(0)
	r.waitFocus("")
	if _, rel, _ := r.cap.state(); len(rel) != 1 || rel[0] != "nowarp" {
		t.Fatalf("releases = %v", rel)
	}
	eventually(t, "mac to be told that the cursor left", func() bool {
		return strings.HasSuffix(mac.events(), "releaseall")
	})

	// And away again: "lap" still knows where the cursor was.
	r.cap.hotkey(toLap)
	r.waitFocus("lap")
	waitPos(t, lap, 350, 310)
	if homes := r.cap.grabbed(); len(homes) != 2 || !homes[1] {
		t.Fatalf("grabs = %v", homes)
	}
}

// A remembered position must still be on the screen after the client came
// back with a smaller one.
func TestHotkeyAfterTheScreenShrank(t *testing.T) {
	r := startServer(t, "desk", []string{"desk", "mac"}, map[string]server.ScreenOpts{
		"mac": {Hotkey: hotkey(t, "ctrl+alt+2")},
	})
	mac, stop := r.connect("mac", 2000, 1000)
	r.cap.hotkey(1)
	r.waitFocus("mac")
	waitPos(t, mac, 1000, 500)
	r.cap.move(900, 450)
	waitPos(t, mac, 1900, 950)
	stop() // it goes away with the cursor on it
	r.waitFocus("")
	eventually(t, "mac gone", func() bool { return len(r.srv.Connected()) == 0 })

	small, _ := r.connect("mac", 800, 600)
	r.cap.hotkey(1)
	r.waitFocus("mac")
	waitPos(t, small, 799, 599)
}

// An edge crossing leaves through an edge, so the capture is not asked to
// remember where the cursor was.
func TestEdgeCrossingDoesNotRememberHome(t *testing.T) {
	r := startServer(t, "desk", []string{"desk", "mac"}, nil)
	r.connect("mac", 1000, 500)
	r.cap.edge(layout.Right, 0.5)
	r.waitFocus("mac")
	if homes := r.cap.grabbed(); len(homes) != 1 || homes[0] {
		t.Fatalf("grabs = %v, want one plain grab", homes)
	}
}

func TestCrossingOnlyWithKeysHeld(t *testing.T) {
	r := startServerWith(t, "desk", []string{"desk", "mac"}, nil, false, func(c *server.Config) {
		c.SwitchMods = keys.ModCtrl
	})
	mac, _ := r.connect("mac", 1000, 500)

	// Without Ctrl an edge is just an edge.
	r.cap.edge(layout.Right, 0.5)
	r.cap.ch <- input.Event{Kind: input.EvEdge, Side: layout.Right, Frac: 0.5, Mods: keys.ModShift | keys.ModAlt}
	r.stays("", "the edge without the keys")
	// More keys than asked for are fine.
	r.cap.ch <- input.Event{Kind: input.EvEdge, Side: layout.Right, Frac: 0.5, Mods: keys.ModCtrl | keys.ModShift}
	r.waitFocus("mac")
	waitPos(t, mac, 2, 250)

	// The same on the way back from the client.
	r.cap.move(-500, 0)
	waitPos(t, mac, 0, 250)
	r.stays("mac", "the client's edge without the keys")
	r.cap.ch <- input.Event{Kind: input.EvMotion, DX: -10, Mods: keys.ModCtrl}
	r.waitFocus("")
}

func TestNoCrossingNearACorner(t *testing.T) {
	r := startServerWith(t, "desk", []string{"desk", "mac"}, nil, false, func(c *server.Config) {
		c.SwitchCorner = 50
	})
	mac, _ := r.connect("mac", 1000, 500)
	edge := func(corner int) {
		r.cap.ch <- input.Event{Kind: input.EvEdge, Side: layout.Right, Frac: 0.05, Corner: corner, Span: 1080}
	}
	// The cursor aims at a corner of the server's screen: that is not a
	// wish to leave it.
	edge(0)
	edge(49)
	r.stays("", "the edge 49 pixels from a corner")
	edge(50)
	r.waitFocus("mac")
	waitPos(t, mac, 2, 25) // 0.05 of the client's height

	// On the client the cursor is now 25 pixels below the top corner.
	r.cap.move(-100, 0)
	waitPos(t, mac, 0, 25)
	r.stays("mac", "the client's edge 25 pixels from a corner")
	r.cap.move(10, 35)
	waitPos(t, mac, 10, 60)
	r.cap.move(-100, 0)
	r.waitFocus("")
	if _, rel, _ := r.cap.state(); len(rel) != 1 || rel[0] != "warp right 0.12" { // 60 / 499
		t.Fatalf("server release = %v", rel)
	}
}

// With a delay the cursor has to keep pressing against the edge. The exact
// timing is tested with a clock of its own in guard_test.go; here the real
// clock only has to show that a touch does not cross and a long press does.
func TestCrossingAfterPressingAgainstTheEdge(t *testing.T) {
	const delay = 300 * time.Millisecond
	r := startServerWith(t, "desk", []string{"desk", "mac"}, nil, false, func(c *server.Config) {
		c.SwitchDelay = delay
	})
	mac, _ := r.connect("mac", 1000, 500)

	// pressUntil keeps doing what a hand pushing the mouse does until the
	// cursor is on the wanted screen, and returns how long that took.
	pressUntil := func(want string, push func()) time.Duration {
		t.Helper()
		start := time.Now()
		for r.srv.Focus() != want {
			if time.Since(start) > 10*time.Second {
				t.Fatalf("pressing against the edge never took the cursor to %q", want)
			}
			push()
			time.Sleep(10 * time.Millisecond)
		}
		return time.Since(start)
	}

	// A touch of the edge, however hard, is not enough.
	for i := 0; i < 20; i++ {
		r.cap.edge(layout.Right, 0.5)
	}
	r.stays("", "a short touch of the edge")
	time.Sleep(250 * time.Millisecond) // that touch is over and forgotten
	if took := pressUntil("mac", func() { r.cap.edge(layout.Right, 0.5) }); took < delay {
		t.Fatalf("the cursor crossed after %v, the delay is %v", took, delay)
	}
	waitPos(t, mac, 2, 250)

	// The same at the edge of the client.
	r.cap.move(-500, 0)
	waitPos(t, mac, 0, 250)
	r.stays("mac", "one shove against the client's edge")
	time.Sleep(250 * time.Millisecond)
	if took := pressUntil("", func() { r.cap.move(-5, 0) }); took < delay {
		t.Fatalf("the cursor crossed back after %v, the delay is %v", took, delay)
	}
}
