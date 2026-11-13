package client

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/Ivlad003/edgehop/internal/clip"
	"github.com/Ivlad003/edgehop/internal/logx"
	"github.com/Ivlad003/edgehop/internal/proto"
	"github.com/Ivlad003/edgehop/internal/secure"
	"github.com/Ivlad003/edgehop/internal/xfer"
)

// clipSync is the client's half of the shared clipboard. Content travels on
// a second, separate connection to the server (the data connection), so a
// large file never holds up mouse and keyboard input.
type clipSync struct {
	cb      clip.Clipboard
	limit   int64
	control *secure.Channel
	data    *secure.Channel

	mu   sync.Mutex
	mark string // this computer's clipboard mark at the last sync

	sendMu sync.Mutex   // one bundle at a time
	note   func(string) // records the last transfer
	recv   xfer.Receiver
}

// openClipboard opens the data connection for a session. It returns nil when
// the clipboard is not shared (disabled here, or not supported by the server).
func (c *Client) openClipboard(ctx context.Context, control *secure.Channel, welcome proto.Msg) *clipSync {
	if c.cfg.Clipboard == nil || welcome.Caps&proto.CapClipboard == 0 {
		return nil
	}
	d := net.Dialer{Timeout: dialTimeout, KeepAlive: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", c.cfg.Server)
	if err != nil {
		logx.Warnf("clipboard: no data connection to the server: %v", shortErr(err))
		return nil
	}
	data, err := secure.ClientHandshake(conn, c.cfg.Key)
	if err == nil {
		err = data.Send(proto.AppendDataHello(nil, welcome.Session))
	}
	if err != nil {
		conn.Close()
		logx.Warnf("clipboard: no data connection to the server: %v", err)
		return nil
	}
	return &clipSync{
		cb:      c.cfg.Clipboard,
		limit:   c.cfg.ClipLimit,
		control: control,
		data:    data,
		recv:    xfer.Receiver{Dir: c.cfg.ClipDir, Limit: c.cfg.ClipLimit},
		note: func(msg string) {
			c.clipNote.Store(msg)
			c.clipCount.Add(1)
			logx.Infof("clipboard: %s", msg)
		},
	}
}

// receive applies the bundles the server sends until the connection closes.
func (s *clipSync) receive() {
	defer s.recv.Abort()
	for {
		frame, err := s.data.Recv()
		if err != nil {
			return
		}
		res, err := s.recv.Frame(frame)
		if err != nil {
			if errors.Is(err, xfer.ErrTooLarge) {
				logx.Infof("clipboard: not taken: over the size limit of %s", xfer.Size(s.limit))
			} else {
				logx.Warnf("clipboard: receiving failed: %v", err)
			}
			continue
		}
		if res == nil {
			continue
		}
		if res.Content.Kind == clip.Text {
			err = s.cb.WriteText(res.Content.Text)
		} else {
			err = s.cb.WriteFiles(res.Content.Files)
		}
		if err != nil {
			logx.Warnf("clipboard: cannot set the clipboard: %v", err)
			continue
		}
		// What was just written is not a new local copy.
		mark, _ := s.cb.Mark()
		s.mu.Lock()
		s.mark = mark
		s.mu.Unlock()
		s.note("received " + res.Stats.String())
	}
}

// cursorLeft tells the server whether something new was copied here while
// the cursor was on this computer.
func (s *clipSync) cursorLeft() {
	mark, err := s.cb.Mark()
	if err != nil {
		logx.Debugf("clipboard: %v", err)
		return
	}
	s.mu.Lock()
	changed := mark != s.mark
	s.mark = mark
	s.mu.Unlock()
	s.control.Send(proto.AppendClipState(nil, changed))
}

// send streams this computer's clipboard to the server as bundle id.
func (s *clipSync) send(id uint32) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	content, err := s.cb.Read()
	if err != nil || content.Kind == clip.None {
		s.data.Send(xfer.Abort(id)) // nothing edgehop can carry
		return
	}
	st, err := xfer.Send(s.data.Send, id, content, s.limit)
	switch {
	case err == nil:
		s.note("sent " + st.String())
	case errors.Is(err, xfer.ErrTooLarge):
		s.data.Send(xfer.Abort(id))
		logx.Infof("clipboard: not shared: %s is over the size limit of %s", xfer.Size(st.Bytes), xfer.Size(s.limit))
	default:
		logx.Warnf("clipboard: sending failed: %v", err)
	}
}

func (s *clipSync) close() { s.data.Close() }
