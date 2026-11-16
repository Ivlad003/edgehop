//go:build darwin

package wincap

import (
	"os"
	"strings"
	"testing"
)

// The window list and a picture of a real window, on a real Mac. It needs a
// desktop session and the Screen Recording permission, so it only runs when
// asked for.
func TestLiveListAndCapture(t *testing.T) {
	if os.Getenv("EDGEHOP_LIVE") != "1" {
		t.Skip("set EDGEHOP_LIVE=1 to run (needs a desktop and the Screen Recording permission)")
	}
	if err := Supported(); err != nil {
		t.Fatalf("not supported on this macOS: %v", err)
	}
	var sys System
	wins, err := sys.List()
	if err != nil {
		if strings.Contains(err.Error(), "has not allowed") {
			t.Skipf("no Screen Recording permission here, nothing verified: %v", strings.SplitN(err.Error(), "\n", 2)[0])
		}
		t.Fatal(err)
	}
	t.Logf("%d windows listed", len(wins))
	for _, w := range wins {
		t.Logf("  %q of %q, %dx%d", w.Title, w.App, w.W, w.H)
	}
	if len(wins) == 0 {
		t.Skip("no windows on this desktop, capturing not verified")
	}
	// The window in front is the one that goes along when the user sends a
	// window to another computer.
	if w, ok := sys.Frontmost(); !ok || w.ID != wins[0].ID || w.W <= 0 || w.H <= 0 {
		t.Errorf("the window in front: %+v (%v), want %+v", w, ok, wins[0])
	} else {
		t.Logf("in front: %q of %q", w.Title, w.App)
	}
	captured := 0
	for _, w := range wins {
		c, err := sys.Open(w.ID)
		if err != nil {
			t.Errorf("open %q: %v", w.Title, err)
			continue
		}
		var f Frame
		err = c.Capture(&f)
		front := c.Front()
		c.Close()
		if err != nil {
			t.Errorf("capture %q: %v", w.Title, err)
			continue
		}
		if f.W != w.W || f.H != w.H || len(f.Pix) != f.W*f.H*4 {
			t.Errorf("%q: picture %dx%d (%d bytes), listed %dx%d", w.Title, f.W, f.H, len(f.Pix), w.W, w.H)
			continue
		}
		colours := map[[3]byte]bool{}
		for i := 0; i+3 < len(f.Pix) && len(colours) < 64; i += 4 * 97 {
			colours[[3]byte{f.Pix[i], f.Pix[i+1], f.Pix[i+2]}] = true
		}
		t.Logf("  captured %q: %dx%d at %d,%d, at least %d colours, front=%v", w.Title, f.W, f.H, f.X, f.Y, len(colours), front)
		if len(colours) > 1 {
			captured++
		}
	}
	if captured == 0 {
		t.Error("no window gave a picture with more than one colour")
	}
}
