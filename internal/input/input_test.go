package input

import (
	"testing"

	"github.com/Ivlad003/edgehop/internal/keys"
)

func TestKeyTrackerPlainRemoteTyping(t *testing.T) {
	var kt keyTracker
	fwd, st, sw := kt.onKey(true, keys.A, true)
	if !fwd || st != keys.Down || !sw {
		t.Fatalf("press: fwd=%v state=%d swallow=%v", fwd, st, sw)
	}
	fwd, st, sw = kt.onKey(true, keys.A, true)
	if !fwd || st != keys.Repeat || !sw {
		t.Fatalf("auto-repeat: fwd=%v state=%d swallow=%v", fwd, st, sw)
	}
	fwd, st, sw = kt.onKey(true, keys.A, false)
	if !fwd || st != keys.Up || !sw {
		t.Fatalf("release: fwd=%v state=%d swallow=%v", fwd, st, sw)
	}
	// A second release is a stray event: hidden from both sides.
	fwd, _, sw = kt.onKey(true, keys.A, false)
	if fwd || !sw {
		t.Fatalf("stray release: fwd=%v swallow=%v", fwd, sw)
	}
}

func TestKeyTrackerLocalModeIsTransparent(t *testing.T) {
	var kt keyTracker
	for _, down := range []bool{true, true, false} {
		fwd, _, sw := kt.onKey(false, keys.Enter, down)
		if fwd || sw {
			t.Fatalf("local mode must pass keys through untouched")
		}
	}
}

func TestKeyHeldAcrossSwitchToRemote(t *testing.T) {
	var kt keyTracker
	// Ctrl goes down locally, then the cursor crosses to the other computer.
	kt.onKey(false, keys.LeftCtrl, true)

	// Auto-repeat of the held key must not reach either side.
	fwd, _, sw := kt.onKey(true, keys.LeftCtrl, true)
	if fwd || !sw {
		t.Fatalf("repeat of a locally held key: fwd=%v swallow=%v", fwd, sw)
	}
	// Its release belongs to the local desktop, not the remote one.
	fwd, _, sw = kt.onKey(true, keys.LeftCtrl, false)
	if fwd || sw {
		t.Fatalf("release of a locally held key: fwd=%v swallow=%v", fwd, sw)
	}
	// After that the key behaves normally on the remote side.
	fwd, st, sw := kt.onKey(true, keys.LeftCtrl, true)
	if !fwd || st != keys.Down || !sw {
		t.Fatalf("fresh press afterwards: fwd=%v state=%d swallow=%v", fwd, st, sw)
	}
}

func TestKeyHeldAcrossSwitchBackToLocal(t *testing.T) {
	var kt keyTracker
	kt.onKey(true, keys.LeftShift, true) // pressed on the remote side
	kt.clearRemote()                     // cursor returns; the server released it remotely

	// The physical release arrives in local mode and simply passes through.
	fwd, _, sw := kt.onKey(false, keys.LeftShift, false)
	if fwd || sw {
		t.Fatalf("release after returning: fwd=%v swallow=%v", fwd, sw)
	}
	// Pressing it again remotely is a fresh press, not a repeat.
	_, st, _ := kt.onKey(true, keys.LeftShift, true)
	if st != keys.Down {
		t.Fatalf("state = %d, want Down", st)
	}
}

func TestKeyTrackerIgnoresUnknownCodes(t *testing.T) {
	var kt keyTracker
	if fwd, _, sw := kt.onKey(true, 0, true); fwd || !sw {
		t.Fatalf("code 0 in remote mode: fwd=%v swallow=%v", fwd, sw)
	}
	if fwd, _, sw := kt.onKey(false, keys.MaxCode+5, true); fwd || sw {
		t.Fatalf("out of range code in local mode: fwd=%v swallow=%v", fwd, sw)
	}
}

func TestEmitNeverBlocks(t *testing.T) {
	ch := make(chan Event, 1)
	emit(ch, Event{Kind: EvKey})
	emit(ch, Event{Kind: EvKey}) // channel is full: must return immediately
	if len(ch) != 1 {
		t.Fatalf("len = %d", len(ch))
	}
}

func mustHotkey(t *testing.T, s string) keys.Hotkey {
	t.Helper()
	h, err := keys.ParseHotkey(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// A hotkey fires once, on the press that completes it, and the key that
// completed it stays hidden until it is released - auto-repeat included.
func TestKeyboardHotkey(t *testing.T) {
	var kb keyboard
	kb.setHotkeys([]Hotkey{
		{Hotkey: mustHotkey(t, "ctrl+alt+shift+backspace"), RemoteOnly: true},
		{Hotkey: mustHotkey(t, "ctrl+alt+2")},
	})
	const two = 3 // the key "2"
	for _, remote := range []bool{false, true} {
		// The modifier keys pass: nobody can know yet what they are for.
		for _, code := range []uint16{keys.RightCtrl, keys.LeftAlt} {
			if hit, eat := kb.onKey(remote, code, true); hit != -1 || eat {
				t.Fatalf("remote=%v: modifier %s: hit=%d eat=%v", remote, keys.Name(code), hit, eat)
			}
		}
		if hit, eat := kb.onKey(remote, two, true); hit != 1 || !eat {
			t.Fatalf("remote=%v: the completing press: hit=%d eat=%v, want 1 true", remote, hit, eat)
		}
		for i := 0; i < 3; i++ {
			if hit, eat := kb.onKey(remote, two, true); hit != -1 || !eat {
				t.Fatalf("remote=%v: auto-repeat: hit=%d eat=%v, want -1 true", remote, hit, eat)
			}
		}
		if hit, eat := kb.onKey(remote, two, false); hit != -1 || !eat {
			t.Fatalf("remote=%v: the release: hit=%d eat=%v, want -1 true", remote, hit, eat)
		}
		// Still holding the modifiers and pressing the key again fires again.
		if hit, eat := kb.onKey(remote, two, true); hit != 1 || !eat {
			t.Fatalf("remote=%v: second press: hit=%d eat=%v", remote, hit, eat)
		}
		kb.onKey(remote, two, false)
		for _, code := range []uint16{keys.LeftAlt, keys.RightCtrl} {
			if hit, eat := kb.onKey(remote, code, false); hit != -1 || eat {
				t.Fatalf("remote=%v: modifier release: hit=%d eat=%v", remote, hit, eat)
			}
		}
		// Without the modifiers it is an ordinary key.
		if hit, eat := kb.onKey(remote, two, true); hit != -1 || eat {
			t.Fatalf("remote=%v: the plain key: hit=%d eat=%v", remote, hit, eat)
		}
		if hit, eat := kb.onKey(remote, two, false); hit != -1 || eat {
			t.Fatalf("remote=%v: the plain key released: hit=%d eat=%v", remote, hit, eat)
		}
	}
}

func TestKeyboardHotkeyNeedsAFreshPress(t *testing.T) {
	var kb keyboard
	kb.setHotkeys([]Hotkey{{Hotkey: mustHotkey(t, "ctrl+f1")}})
	// F1 is held first and Ctrl comes later: that is not Ctrl+F1.
	kb.onKey(false, keys.F1, true)
	kb.onKey(false, keys.LeftCtrl, true)
	if hit, eat := kb.onKey(false, keys.F1, true); hit != -1 || eat {
		t.Fatalf("auto-repeat of a key held before the modifier: hit=%d eat=%v", hit, eat)
	}
	kb.onKey(false, keys.F1, false)
	if hit, eat := kb.onKey(false, keys.F1, true); hit != 0 || !eat {
		t.Fatalf("a fresh press with the modifier held: hit=%d eat=%v", hit, eat)
	}
}

func TestKeyboardRemoteOnlyHotkey(t *testing.T) {
	var kb keyboard
	kb.setHotkeys([]Hotkey{{Hotkey: mustHotkey(t, "ctrl+backspace"), RemoteOnly: true}})
	kb.onKey(false, keys.LeftCtrl, true)
	if hit, eat := kb.onKey(false, keys.Backspace, true); hit != -1 || eat {
		t.Fatalf("in local mode the keys belong to the local desktop: hit=%d eat=%v", hit, eat)
	}
	kb.onKey(false, keys.Backspace, false)
	if hit, eat := kb.onKey(true, keys.Backspace, true); hit != 0 || !eat {
		t.Fatalf("in remote mode: hit=%d eat=%v, want 0 true", hit, eat)
	}
	// The release arrives after the switch back to local mode: still hidden.
	if hit, eat := kb.onKey(false, keys.Backspace, false); hit != -1 || !eat {
		t.Fatalf("release after the mode changed: hit=%d eat=%v", hit, eat)
	}
}

func TestKeyboardWithoutHotkeys(t *testing.T) {
	var kb keyboard // SetHotkeys was never called
	kb.onKey(false, keys.LeftCtrl, true)
	if hit, eat := kb.onKey(false, keys.A, true); hit != -1 || eat {
		t.Fatalf("hit=%d eat=%v", hit, eat)
	}
	if hit, eat := kb.onKey(true, 0, true); hit != -1 || eat {
		t.Fatalf("code 0: hit=%d eat=%v", hit, eat)
	}
	if hit, eat := kb.onKey(true, keys.MaxCode+1, true); hit != -1 || eat {
		t.Fatalf("out of range code: hit=%d eat=%v", hit, eat)
	}
}

func TestKeyboardMods(t *testing.T) {
	var kb keyboard
	if kb.mods() != 0 {
		t.Fatalf("mods = %04b on a fresh keyboard", kb.mods())
	}
	kb.onKey(false, keys.LeftCtrl, true)
	kb.onKey(false, keys.RightShift, true)
	kb.onKey(false, keys.A, true)
	if got := kb.mods(); got != keys.ModCtrl|keys.ModShift {
		t.Fatalf("mods = %04b, want ctrl+shift", got)
	}
	// Both keys of a pair: the modifier stays until the second one is up.
	kb.onKey(false, keys.RightCtrl, true)
	kb.onKey(false, keys.LeftCtrl, false)
	if got := kb.mods(); got != keys.ModCtrl|keys.ModShift {
		t.Fatalf("mods = %04b after releasing one of two Ctrl keys", got)
	}
	kb.onKey(false, keys.RightCtrl, false)
	if got := kb.mods(); got != keys.ModShift {
		t.Fatalf("mods = %04b, want shift", got)
	}
	kb.reset()
	if kb.mods() != 0 {
		t.Fatalf("mods = %04b after reset", kb.mods())
	}
}

// A release the capture never saw (the lock screen swallowed it) must not
// leave a modifier held for ever: in local mode the system knows better.
func TestKeyboardKeep(t *testing.T) {
	var kb keyboard
	kb.setHotkeys([]Hotkey{{Hotkey: mustHotkey(t, "ctrl+alt+2")}})
	kb.onKey(false, keys.LeftCtrl, true)
	kb.onKey(false, keys.LeftAlt, true)
	// ... Ctrl+Alt+Del, the releases are lost ...
	kb.keep(0)
	if kb.mods() != 0 {
		t.Fatalf("mods = %04b, the system said none are held", kb.mods())
	}
	if hit, eat := kb.onKey(false, 3, true); hit != -1 || eat {
		t.Fatalf("a lone 2 fired the hotkey: hit=%d eat=%v", hit, eat)
	}
	kb.onKey(false, 3, false)

	// A modifier pressed in remote mode was swallowed: the system never saw
	// it, so its word does not count for that key.
	kb.onKey(true, keys.LeftCtrl, true)
	kb.onKey(false, keys.LeftShift, true)
	kb.keep(keys.ModShift)
	if got := kb.mods(); got != keys.ModCtrl|keys.ModShift {
		t.Fatalf("mods = %04b, want ctrl (pressed remotely) + shift", got)
	}
	kb.keep(0)
	if got := kb.mods(); got != keys.ModCtrl {
		t.Fatalf("mods = %04b, want ctrl only", got)
	}
	// Once it is released and pressed again locally, the system is believed.
	kb.onKey(false, keys.LeftCtrl, false)
	kb.onKey(false, keys.LeftCtrl, true)
	kb.keep(0)
	if kb.mods() != 0 {
		t.Fatalf("mods = %04b", kb.mods())
	}
}

// "ctrl+2" also matches while Alt is held, but when another hotkey asks for
// exactly Ctrl+Alt+2, that one is meant - whatever their order.
func TestKeyboardMostSpecificHotkeyWins(t *testing.T) {
	const two = 3
	for _, order := range [][]string{{"ctrl+2", "ctrl+alt+2"}, {"ctrl+alt+2", "ctrl+2"}} {
		var kb keyboard
		list := make([]Hotkey, len(order))
		index := map[string]int{}
		for i, s := range order {
			list[i] = Hotkey{Hotkey: mustHotkey(t, s)}
			index[s] = i
		}
		kb.setHotkeys(list)
		kb.onKey(false, keys.LeftCtrl, true)
		if hit, _ := kb.onKey(false, two, true); hit != index["ctrl+2"] {
			t.Errorf("%v: Ctrl+2 fired hotkey %d, want %d", order, hit, index["ctrl+2"])
		}
		kb.onKey(false, two, false)
		kb.onKey(false, keys.LeftAlt, true)
		if hit, _ := kb.onKey(false, two, true); hit != index["ctrl+alt+2"] {
			t.Errorf("%v: Ctrl+Alt+2 fired hotkey %d, want %d", order, hit, index["ctrl+alt+2"])
		}
	}
}
