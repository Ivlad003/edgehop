package client

import (
	"errors"
	"net"
	"time"

	"github.com/Ivlad003/edgehop/internal/logx"
	"github.com/Ivlad003/edgehop/internal/proto"
	"github.com/Ivlad003/edgehop/internal/secure"
)

// maxViews is how many of this computer's windows can be shown at a time.
const maxViews = 6

// openView answers the server's request to show a window of this computer
// (remote windows, experimental): it opens view connection id and serves it
// until one side closes it or the session with the server ends.
//
// The connection is separate from the one that carries the mouse and the
// keyboard, so whatever happens here cannot disturb them.
func (c *Client) openView(session, id uint32, stop <-chan struct{}) {
	if c.views.Add(1) > maxViews {
		c.views.Add(-1)
		logx.Warnf("remote windows: too many windows are being shown already")
		return
	}
	defer c.views.Add(-1)

	d := net.Dialer{Timeout: dialTimeout, KeepAlive: 10 * time.Second}
	conn, err := d.Dial("tcp", c.cfg.Server)
	if err != nil {
		logx.Warnf("remote windows: no connection to the server: %v", shortErr(err))
		return
	}
	ch, err := secure.ClientHandshake(conn, c.cfg.Key)
	if err == nil {
		err = ch.Send(proto.AppendViewHello(nil, session, id))
	}
	if err != nil {
		conn.Close()
		logx.Warnf("remote windows: no connection to the server: %v", err)
		return
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
			ch.Close()
		case <-done:
		}
	}()
	logx.Debugf("remote windows: view %d opened", id)
	c.cfg.View.Serve(ch)
	logx.Debugf("remote windows: view %d closed", id)
}

// OpenView opens a connection on which the server shows its own windows
// (remote windows, experimental - the other direction): this computer is the
// viewer, and the server answers like any other source of windows. The
// caller closes the connection when it is done.
func (c *Client) OpenView() (*secure.Channel, error) {
	session := c.sessID.Load()
	if session == 0 {
		return nil, errors.New("not connected to the server")
	}
	if !c.srvGives.Load() {
		return nil, errors.New("the server does not let its windows be shown (remote windows are off there, it is not a Mac, or its edgehop is older)")
	}
	d := net.Dialer{Timeout: dialTimeout, KeepAlive: 10 * time.Second}
	conn, err := d.Dial("tcp", c.cfg.Server)
	if err != nil {
		return nil, shortErr(err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	ch, err := secure.ClientHandshake(conn, c.cfg.Key)
	if err == nil {
		err = ch.Send(proto.AppendViewAsk(nil, session))
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ch, nil
}
