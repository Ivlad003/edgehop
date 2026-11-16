package server_test

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivlad003/edgehop/internal/client"
	"github.com/Ivlad003/edgehop/internal/input"
	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
	"github.com/Ivlad003/edgehop/internal/proto"
	"github.com/Ivlad003/edgehop/internal/rview"
	"github.com/Ivlad003/edgehop/internal/secure"
	"github.com/Ivlad003/edgehop/internal/server"
)

// The other direction of remote windows: a client asks, on a connection of
// its own, for the windows of the server itself.
func TestServerShowsItsOwnWindowsToAClient(t *testing.T) {
	r := startServerWith(t, "mac", []string{"old", "mac", "pc"}, nil, false, func(c *server.Config) {
		c.View = &rview.Source{Windows: fakeWindows{}, Injector: &fakeInjector{}, Name: "mac", OS: "test"}
	})
	pcInj, _ := r.connectWith("pc", 1000, 500, func(c *client.Config) { c.Shows = true })
	r.connect("old", 800, 600) // a client that does not show windows

	pc, old := r.clients["pc"], r.clients["old"]
	if !pc.ServerGivesWindows() {
		t.Fatal("the client was not told that the server lets its windows be shown")
	}
	if old.ServerGivesWindows() {
		t.Error("a client that shows no windows believes it can")
	}
	for _, c := range r.srv.Clients() {
		if c.Shows != (c.Name == "pc") {
			t.Errorf("%s: shows = %v", c.Name, c.Shows)
		}
	}

	// Several views at a time, each on a connection of its own.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, err := pc.OpenView()
			if err != nil {
				t.Errorf("open: %v", err)
				return
			}
			defer ch.Close()
			list, err := rview.ListWindows(ch)
			if err != nil {
				t.Errorf("list: %v", err)
				return
			}
			if list.Name != "mac" || len(list.Windows) != 1 || list.Windows[0].Title != "Editor" {
				t.Errorf("list = %+v", list)
			}
		}()
	}
	wg.Wait()

	if _, err := old.OpenView(); err == nil || !strings.Contains(err.Error(), "does not let") {
		t.Errorf("a client that shows no windows: %v", err)
	}

	// Asking without a session of one's own gets nothing, even with the key.
	conn, err := net.Dial("tcp", r.srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := secure.ClientHandshake(conn, r.key)
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(proto.AppendViewAsk(nil, 4242)); err != nil {
		t.Fatal(err)
	}
	ch.Send([]byte{1}) // "list your windows", in the view connection's own language
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := ch.Recv(); err == nil {
		t.Error("a view of the server's windows was given to a stranger")
	}

	// And the cursor still crosses to that client as before.
	r.cap.edge(layout.Right, 0.5)
	r.waitFocus("pc")
	waitPos(t, pcInj, 2, 250)
}

// A server that offers none of its windows says so.
func TestClientSeesThatTheServerGivesNoWindows(t *testing.T) {
	r := startServer(t, "mac", []string{"mac", "pc"}, nil)
	r.connectWith("pc", 1000, 500, func(c *client.Config) { c.Shows = true })
	pc := r.clients["pc"]
	if pc.ServerGivesWindows() {
		t.Error("the client believes the server lets its windows be shown")
	}
	if _, err := pc.OpenView(); err == nil {
		t.Error("a view was opened although the server gives none")
	}
}

// giveRig is a server "mac" between a client "pc" (right) that shows windows
// and a client "old" (left) that does not.
type giveRig struct {
	*rig
	pc, old *fakeInjector

	mu      sync.Mutex
	window  uint64 // what the server finds as the window to send
	picked  int
	dropped []string
	got     []client.SentWindow
}

const sendKeys = keys.ModCtrl | keys.ModAlt

func newGiveRig(t *testing.T) *giveRig {
	t.Helper()
	g := &giveRig{window: 42}
	g.rig = startServerWith(t, "mac", []string{"old", "mac", "pc"}, nil, false, func(c *server.Config) {
		c.View = &rview.Source{Windows: fakeWindows{}, Injector: &fakeInjector{}, Name: "mac", OS: "test"}
		c.WindowKeys = sendKeys
		c.PickWindow = func() (uint64, int, int) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.picked++
			return g.window, 800, 600
		}
		c.EndDrag = func(side layout.Side, frac float64) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.dropped = append(g.dropped, side.String())
		}
	})
	g.pc, _ = g.connectWith("pc", 1000, 500, func(c *client.Config) {
		c.Shows = true
		c.OnWindow = func(w client.SentWindow) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.got = append(g.got, w)
		}
	})
	g.old, _ = g.connect("old", 800, 600)
	return g
}

func (g *giveRig) edge(side layout.Side, frac float64, mods uint8, drag bool) {
	g.cap.ch <- input.Event{Kind: input.EvEdge, Side: side, Frac: frac, Mods: mods, Drag: drag}
}

func (g *giveRig) sent() []client.SentWindow {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]client.SentWindow(nil), g.got...)
}

func (g *giveRig) counts() (picked int, dropped []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.picked, append([]string(nil), g.dropped...)
}

// quiet waits a moment and reports whether the cursor stayed on the server.
func (g *giveRig) quiet() bool {
	time.Sleep(80 * time.Millisecond)
	return g.srv.Focus() == ""
}

// With the keys for it held, taking the cursor over the edge takes the
// window the user works in along: the client is told to open it where the
// cursor comes in.
func TestWindowGoesAlongWithTheKeysHeld(t *testing.T) {
	g := newGiveRig(t)

	g.edge(layout.Right, 0.25, sendKeys, false)
	g.waitFocus("pc")
	waitPos(t, g.pc, 2, 125)
	eventually(t, "the window to arrive", func() bool { return len(g.sent()) == 1 })
	w := g.sent()[0]
	if w.Peer != "mac" || w.ID != 42 || w.W != 800 || w.H != 600 || w.Side != layout.Left || w.Frac < 0.24 || w.Frac > 0.26 {
		t.Errorf("the client got %+v", w)
	}
	if _, dropped := g.counts(); len(dropped) != 0 {
		t.Errorf("nothing was dragged, yet a drag was ended: %v", dropped)
	}

	// The mouse and keyboard work over there as after any crossing.
	g.cap.move(40, 10)
	waitPos(t, g.pc, 42, 135)
	g.cap.key(keys.A, keys.Down)
	eventually(t, "the key to arrive", func() bool { return g.pc.has("key a 1") })

	// Back, and once more in the middle of a drag: the window is let go on
	// the server first.
	g.cap.key(keys.A, keys.Up)
	g.cap.move(-100, 0)
	g.waitFocus("")
	g.edge(layout.Right, 0.5, sendKeys, true)
	g.waitFocus("pc")
	eventually(t, "the second window to arrive", func() bool { return len(g.sent()) == 2 })
	if _, dropped := g.counts(); len(dropped) != 1 || dropped[0] != layout.Right.String() {
		t.Errorf("drags ended: %v, want one at the right edge", dropped)
	}
}

// Everything else at the edge stays what it was.
func TestOnlyTheRightKeysSendAWindow(t *testing.T) {
	g := newGiveRig(t)

	// A drag never leaves the screen: not alone, not with other keys, and
	// not when there is no window to send.
	g.edge(layout.Right, 0.5, 0, true)
	g.edge(layout.Right, 0.5, keys.ModShift, true)
	if !g.quiet() {
		t.Fatal("a drag took the cursor to another computer")
	}
	if picked, _ := g.counts(); picked != 0 {
		t.Errorf("looked for a window %d times without the keys", picked)
	}
	g.mu.Lock()
	g.window = 0
	g.mu.Unlock()
	g.edge(layout.Right, 0.5, sendKeys, true)
	g.edge(layout.Right, 0.5, sendKeys, true) // the cursor rests there: one look is enough
	if !g.quiet() {
		t.Fatal("a drag without a window to send took the cursor to another computer")
	}
	if picked, dropped := g.counts(); picked != 1 || len(dropped) != 0 {
		t.Errorf("looked for a window %d times, ended %d drags", picked, len(dropped))
	}

	// Without a drag the cursor crosses as always - with no window, with
	// other keys, and towards a client that shows no windows.
	g.edge(layout.Right, 0.5, sendKeys, false)
	g.waitFocus("pc")
	g.cap.move(-100, 0)
	g.waitFocus("")

	g.mu.Lock()
	g.window = 42
	g.mu.Unlock()
	g.edge(layout.Right, 0.5, keys.ModShift, false)
	g.waitFocus("pc")
	g.cap.move(-100, 0)
	g.waitFocus("")

	g.edge(layout.Left, 0.5, sendKeys, false)
	g.waitFocus("old")
	waitPos(t, g.old, 797, 300)
	g.cap.move(-30, 0)
	waitPos(t, g.old, 767, 300) // still connected: it was not sent what it does not know
	if sent := g.sent(); len(sent) != 0 {
		t.Errorf("windows arrived: %+v", sent)
	}
}
