package server

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Ivlad003/edgehop/internal/proto"
	"github.com/Ivlad003/edgehop/internal/secure"
)

// Remote windows (experimental): a window of a client shown on the server.
//
// Each shown window has a view connection of its own. Clients always dial
// the server, so the server asks the client over the control connection to
// open one, and the client connects back with the number it was given.

const viewOpenTimeout = 8 * time.Second

// ViewPeer is a connected computer whose windows can be shown here.
type ViewPeer struct {
	Name string `json:"name"`
	OS   string `json:"os"`
}

type viewWaiter struct {
	session uint32
	ch      chan *secure.Channel
}

// viewHub pairs the server's requests with the connections that answer them.
type viewHub struct {
	mu      sync.Mutex
	next    uint32
	waiting map[uint32]viewWaiter
}

// ViewPeers lists the connected computers that allow showing their windows.
func (s *Server) ViewPeers() []ViewPeer {
	s.idMu.RLock()
	defer s.idMu.RUnlock()
	var out []ViewPeer
	for _, sess := range s.byID {
		if sess.joined.Load() && sess.caps&proto.CapView != 0 {
			out = append(out, ViewPeer{Name: sess.name, OS: sess.os})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ViewRemap returns the key remapping configured for a screen, so keys typed
// into a remote window are swapped the same way as with the shared keyboard.
func (s *Server) ViewRemap(name string) map[uint16]uint16 {
	return s.cfg.Screens[name].Remap
}

// OpenView asks the named client for a view connection and waits for it.
func (s *Server) OpenView(name string) (*secure.Channel, error) {
	var sess *session
	s.idMu.RLock()
	for _, c := range s.byID {
		if c.name == name && c.joined.Load() && !c.closed.Load() {
			sess = c
		}
	}
	s.idMu.RUnlock()
	if sess == nil {
		return nil, fmt.Errorf("%q is not connected", name)
	}
	if sess.caps&proto.CapView == 0 {
		return nil, fmt.Errorf("%q does not allow showing its windows (remote windows are off there, or its edgehop is older)", name)
	}

	w := viewWaiter{session: sess.id, ch: make(chan *secure.Channel, 1)}
	s.views.mu.Lock()
	if s.views.waiting == nil {
		s.views.waiting = map[uint32]viewWaiter{}
	}
	s.views.next++
	if s.views.next == 0 {
		s.views.next = 1
	}
	id := s.views.next
	s.views.waiting[id] = w
	s.views.mu.Unlock()
	defer func() {
		s.views.mu.Lock()
		delete(s.views.waiting, id)
		s.views.mu.Unlock()
		select { // a connection that arrived just too late
		case ch := <-w.ch:
			ch.Close()
		default:
		}
	}()

	// Not through sess.send: that one belongs to the run loop.
	if err := sess.ch.Send(proto.AppendViewOpen(nil, id)); err != nil {
		return nil, fmt.Errorf("%q: %v", name, err)
	}
	timeout := time.NewTimer(viewOpenTimeout)
	defer timeout.Stop()
	select {
	case ch := <-w.ch:
		return ch, nil
	case <-timeout.C:
		return nil, fmt.Errorf("%q did not open the connection for its windows", name)
	case <-s.done:
		return nil, errors.New("edgehop is stopping")
	}
}

// handleView takes a connection that announced itself as view id of a
// session. Only connections the server asked for are kept.
func (s *Server) handleView(ch *secure.Channel, sessionID, id uint32) {
	s.views.mu.Lock()
	w, ok := s.views.waiting[id]
	if ok && w.session == sessionID {
		delete(s.views.waiting, id)
	} else {
		ok = false
	}
	s.views.mu.Unlock()
	if !ok {
		ch.Close()
		return
	}
	w.ch <- ch
}
