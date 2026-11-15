package server_test

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivlad003/edgehop/internal/client"
	"github.com/Ivlad003/edgehop/internal/layout"
	"github.com/Ivlad003/edgehop/internal/proto"
	"github.com/Ivlad003/edgehop/internal/rview"
	"github.com/Ivlad003/edgehop/internal/secure"
	"github.com/Ivlad003/edgehop/internal/server"
	"github.com/Ivlad003/edgehop/internal/wincap"
)

// fakeWindows is a computer with one window that cannot be opened.
type fakeWindows struct{}

func (fakeWindows) List() ([]wincap.Window, error) {
	return []wincap.Window{{ID: 5, Title: "Editor", App: "edit", W: 640, H: 480}}, nil
}
func (fakeWindows) Open(uint64) (wincap.Capturer, error) { return nil, wincap.ErrGone }

// Remote windows: the server asks a client for a connection of its own, the
// client opens it, and the two talk over it - while the mouse and keyboard
// keep working as before.
func TestViewConnectionToAClient(t *testing.T) {
	r := startServerWith(t, "desk", []string{"desk", "mac", "old"}, nil, false, func(c *server.Config) {
		c.ShowsWindows = true
	})
	mac, _ := r.connectWith("mac", 1000, 500, func(c *client.Config) {
		c.View = &rview.Source{Windows: fakeWindows{}, Injector: &fakeInjector{}, Name: "mac", OS: "test"}
	})
	r.connect("old", 800, 600) // a client that does not offer its windows

	// The client learns that the server is ready to show its windows.
	if c := r.clients["mac"]; !c.ServerShowsWindows() || c.ServerName() != "desk" {
		t.Errorf("the client sees the server as %q, shows windows: %v", c.ServerName(), c.ServerShowsWindows())
	}

	peers := r.srv.ViewPeers()
	if len(peers) != 1 || peers[0].Name != "mac" {
		t.Fatalf("peers = %+v, want only mac", peers)
	}

	// Several views at a time, each on a connection of its own.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, err := r.srv.OpenView("mac")
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

	if _, err := r.srv.OpenView("old"); err == nil || !strings.Contains(err.Error(), "does not allow") {
		t.Errorf("a client that did not allow it: %v", err)
	}
	if _, err := r.srv.OpenView("ghost"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("an unknown computer: %v", err)
	}

	// A view connection nobody asked for is closed at once, even with the
	// right key.
	conn, err := net.Dial("tcp", r.srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := secure.ClientHandshake(conn, r.key)
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(proto.AppendViewHello(nil, 1, 4242)); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := ch.Recv(); err == nil {
		t.Error("an unrequested view connection was kept open")
	}

	// And the cursor still crosses to that client as before.
	r.cap.edge(layout.Right, 0.5)
	r.waitFocus("mac")
	waitPos(t, mac, 2, 250)
	r.cap.move(40, 10)
	waitPos(t, mac, 42, 260)
}

// A server that has remote windows turned off says so, and the client
// reports that the other side is not ready.
func TestClientSeesThatTheServerShowsNoWindows(t *testing.T) {
	r := startServer(t, "desk", []string{"desk", "mac"}, nil)
	r.connectWith("mac", 1000, 500, func(c *client.Config) {
		c.View = &rview.Source{Windows: fakeWindows{}, Injector: &fakeInjector{}, Name: "mac", OS: "test"}
	})
	if c := r.clients["mac"]; c.ServerShowsWindows() || c.ServerName() != "desk" {
		t.Errorf("the client sees the server as %q, shows windows: %v", c.ServerName(), c.ServerShowsWindows())
	}
}
