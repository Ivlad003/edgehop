package client

import (
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
