package input

import (
	"testing"

	"edgehop/internal/keys"
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
