package server_test

import (
	"sync"
	"testing"
	"time"

	"github.com/Ivlad003/edgehop/internal/client"
	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
	"github.com/Ivlad003/edgehop/internal/rview"
	"github.com/Ivlad003/edgehop/internal/server"
)

// dragRig is a server with one client "mac" to its right, which reports the
// window `dragged` as being moved.
type dragRig struct {
	*rig
	mac *fakeInjector

	mu      sync.Mutex
	dragged uint64 // what the client answers
	asked   int    // how often it was asked
	got     []server.DraggedWindow
}

func newDragRig(t *testing.T, hold time.Duration, withAnswer bool) *dragRig {
	t.Helper()
	d := &dragRig{dragged: 77}
	d.rig = startServerWith(t, "desk", []string{"desk", "mac"}, nil, false, func(c *server.Config) {
		c.ShowsWindows = true
		c.DragHold, c.DragPush = hold, 50
		c.OnWindowDrag = func(w server.DraggedWindow) {
			d.mu.Lock()
			d.got = append(d.got, w)
			d.mu.Unlock()
		}
	})
	d.mac, _ = d.connectWith("mac", 1000, 500, func(c *client.Config) {
		c.View = &rview.Source{Windows: fakeWindows{}, Injector: &fakeInjector{}, Name: "mac", OS: "test"}
		if withAnswer {
			c.Dragged = func() (uint64, int, int) {
				d.mu.Lock()
				defer d.mu.Unlock()
				d.asked++
				return d.dragged, 640, 480
			}
		}
	})
	// Onto the client, a little inside, and press the left button: a drag.
	d.cap.edge(layout.Right, 0.5)
	d.waitFocus("mac")
	d.cap.move(300, 0)
	waitPos(t, d.mac, 302, 250)
	d.cap.button(keys.ButtonLeft, true)
	return d
}

func (d *dragRig) windows() []server.DraggedWindow {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]server.DraggedWindow(nil), d.got...)
}

func (d *dragRig) questions() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.asked
}

// A window pushed against the edge towards the server, and held there, comes
// over: the drag ends on the client, the cursor returns to the server, and
// the window is handed to whoever shows it.
func TestWindowPushedOverTheEdgeComesToTheServer(t *testing.T) {
	d := newDragRig(t, 60*time.Millisecond, true)

	// Reaching the edge with the button held keeps the cursor there, as ever.
	d.cap.move(-400, 0)
	waitPos(t, d.mac, 0, 250)
	if len(d.windows()) != 0 || d.srv.Focus() != "mac" {
		t.Fatal("touching the edge must not be enough")
	}
	// Held there for a moment and pushed further: now it counts.
	time.Sleep(120 * time.Millisecond)
	d.cap.move(-30, 0)
	d.cap.move(-30, 0)
	d.waitFocus("")
	eventually(t, "the window to be handed over", func() bool { return len(d.windows()) == 1 })
	w := d.windows()[0]
	if w.Peer != "mac" || w.ID != 77 || w.W != 640 || w.H != 480 || w.Side != layout.Right || w.Frac < 0.45 || w.Frac > 0.55 {
		t.Errorf("handed over as %+v", w)
	}
	// The button was let go on the client, so the window is dropped there.
	eventually(t, "the button release on the client", func() bool { return d.mac.has("button 1 false at 0,250") })
	if _, releases, _ := d.cap.state(); len(releases) == 0 {
		t.Error("the server's own cursor was not given back")
	}
}

// When no window is being moved - the user drags something else - nothing
// happens, and the client is not asked again and again.
func TestDragOfSomethingElseStaysOnItsScreen(t *testing.T) {
	d := newDragRig(t, 60*time.Millisecond, true)
	d.mu.Lock()
	d.dragged = 0
	d.mu.Unlock()

	d.cap.move(-400, 0)
	waitPos(t, d.mac, 0, 250)
	time.Sleep(120 * time.Millisecond)
	for i := 0; i < 10; i++ {
		d.cap.move(-30, 0)
	}
	eventually(t, "the question", func() bool { return d.questions() == 1 })
	time.Sleep(150 * time.Millisecond)
	if d.srv.Focus() != "mac" || len(d.windows()) != 0 || d.questions() != 1 {
		t.Fatalf("focus %q, windows %d, questions %d; want the cursor to stay and one question", d.srv.Focus(), len(d.windows()), d.questions())
	}
	// Let go, press again: a new drag may ask again.
	d.cap.button(keys.ButtonLeft, false)
	d.cap.move(100, 0)
	d.cap.button(keys.ButtonLeft, true)
	d.cap.move(-400, 0)
	time.Sleep(120 * time.Millisecond)
	for i := 0; i < 10; i++ {
		d.cap.move(-30, 0)
	}
	eventually(t, "a second question", func() bool { return d.questions() == 2 })
}

// A quick fling against the edge - as when snapping a window to half the
// screen - is not a request to move the window.
func TestQuickPushAgainstTheEdgeIsNotARequest(t *testing.T) {
	d := newDragRig(t, 0, true) // the default hold time
	for i := 0; i < 20; i++ {
		d.cap.move(-100, 0)
	}
	waitPos(t, d.mac, 0, 250)
	time.Sleep(100 * time.Millisecond)
	if d.questions() != 0 || d.srv.Focus() != "mac" {
		t.Errorf("questions %d, focus %q", d.questions(), d.srv.Focus())
	}
}

// A client that cannot tell which window is dragged is never asked: an older
// edgehop would not understand the question.
func TestClientWithoutDragSupportIsLeftAlone(t *testing.T) {
	d := newDragRig(t, 60*time.Millisecond, false)
	d.cap.move(-400, 0)
	waitPos(t, d.mac, 0, 250)
	time.Sleep(120 * time.Millisecond)
	for i := 0; i < 10; i++ {
		d.cap.move(-30, 0)
	}
	time.Sleep(150 * time.Millisecond)
	if d.srv.Focus() != "mac" || len(d.windows()) != 0 {
		t.Errorf("focus %q, windows %d", d.srv.Focus(), len(d.windows()))
	}
	// Still connected and working.
	d.cap.button(keys.ButtonLeft, false)
	d.cap.move(40, 0)
	waitPos(t, d.mac, 40, 250)
}
