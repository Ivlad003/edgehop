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

// dataLink is the client's half of content transfer: the shared clipboard
// and files sent on purpose. Content travels on a second, separate
// connection to the server (the data connection), so a large file never
// holds up mouse and keyboard input.
type dataLink struct {
	cb         clip.Clipboard // nil when the clipboard is not shared
	limit      int64
	control    *secure.Channel
	data       *secure.Channel
	server     string // the server's screen name
	serverDrop bool   // the server accepts sent files
	dropDir    string

	mu   sync.Mutex
	mark string // this computer's clipboard mark at the last sync

	sendMu sync.Mutex // one bundle at a time
	nextID uint32
	note   func(string) // records the last transfer
	recv   xfer.Receiver
}

// openData opens the data connection for a session. It returns nil when
// neither side has a use for it.
func (c *Client) openData(ctx context.Context, control *secure.Channel, welcome proto.Msg) *dataLink {
	shareClip := c.cfg.Clipboard != nil && welcome.Caps&proto.CapClipboard != 0
	serverDrop := welcome.Caps&proto.CapDrop != 0
	if !shareClip && !serverDrop && c.cfg.DropDir == "" {
		return nil
	}
	if welcome.Caps&(proto.CapClipboard|proto.CapDrop) == 0 {
		return nil // an older server without a data connection
	}
	d := net.Dialer{Timeout: dialTimeout, KeepAlive: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", c.cfg.Server)
	if err != nil {
		logx.Warnf("no data connection to the server: %v", shortErr(err))
		return nil
	}
	data, err := secure.ClientHandshake(conn, c.cfg.Key)
	if err == nil {
		err = data.Send(proto.AppendDataHello(nil, welcome.Session))
	}
	if err != nil {
		conn.Close()
		logx.Warnf("no data connection to the server: %v", err)
		return nil
	}
	l := &dataLink{
		limit:      c.cfg.ClipLimit,
		control:    control,
		data:       data,
		server:     welcome.Name,
		serverDrop: serverDrop,
		dropDir:    c.cfg.DropDir,
		recv:       xfer.Receiver{Dir: c.cfg.ClipDir, DropDir: c.cfg.DropDir, Limit: c.cfg.ClipLimit},
		note: func(msg string) {
			c.clipNote.Store(msg)
			c.clipCount.Add(1)
			logx.Infof("transfer: %s", msg)
		},
	}
	if shareClip {
		l.cb = c.cfg.Clipboard
	}
	return l
}

// receive applies the bundles the server sends until the connection closes.
func (l *dataLink) receive() {
	defer l.recv.Abort()
	for {
		frame, err := l.data.Recv()
		if err != nil {
			return
		}
		res, err := l.recv.Frame(frame)
		if err != nil {
			if errors.Is(err, xfer.ErrTooLarge) {
				logx.Infof("clipboard: not taken: over the size limit of %s", xfer.Size(l.limit))
			} else {
				logx.Warnf("transfer: receiving failed: %v", err)
			}
			continue
		}
		if res == nil {
			continue
		}
		if res.Drop {
			l.note("received " + res.Stats.String() + ", saved to " + l.dropDir)
			continue
		}
		if l.cb == nil {
			continue
		}
		if res.Content.Kind == clip.Text {
			err = l.cb.WriteText(res.Content.Text)
		} else {
			err = l.cb.WriteFiles(res.Content.Files)
		}
		if err != nil {
			logx.Warnf("clipboard: cannot set the clipboard: %v", err)
			continue
		}
		// What was just written is not a new local copy.
		mark, _ := l.cb.Mark()
		l.mu.Lock()
		l.mark = mark
		l.mu.Unlock()
		l.note("clipboard received: " + res.Stats.String())
	}
}

// cursorLeft tells the server whether something new was copied here while
// the cursor was on this computer.
func (l *dataLink) cursorLeft() {
	if l.cb == nil {
		return
	}
	mark, err := l.cb.Mark()
	if err != nil {
		logx.Debugf("clipboard: %v", err)
		return
	}
	l.mu.Lock()
	changed := mark != l.mark
	l.mark = mark
	l.mu.Unlock()
	l.control.Send(proto.AppendClipState(nil, changed))
}

// sendClipboard streams this computer's clipboard to the server as bundle id.
func (l *dataLink) sendClipboard(id uint32) {
	l.sendMu.Lock()
	defer l.sendMu.Unlock()
	if l.cb == nil {
		l.data.Send(xfer.Abort(id))
		return
	}
	content, err := l.cb.Read()
	if err != nil || content.Kind == clip.None {
		l.data.Send(xfer.Abort(id)) // nothing edgehop can carry
		return
	}
	st, err := xfer.Send(l.data.Send, id, content, l.limit)
	switch {
	case err == nil:
		l.note("clipboard sent: " + st.String())
	case errors.Is(err, xfer.ErrTooLarge):
		l.data.Send(xfer.Abort(id))
		logx.Infof("clipboard: not shared: %s is over the size limit of %s", xfer.Size(st.Bytes), xfer.Size(l.limit))
	default:
		logx.Warnf("clipboard: sending failed: %v", err)
	}
}

// sendFiles sends files to the server, which saves them in its folder for
// received files.
func (l *dataLink) sendFiles(paths []string) error {
	if !l.serverDrop {
		return errors.New("the server runs an edgehop version that cannot receive files")
	}
	l.sendMu.Lock()
	defer l.sendMu.Unlock()
	l.nextID++
	// Ids chosen by a client carry the top bit; the server numbers its own
	// requests from the lower half.
	st, err := xfer.SendDrop(l.data.Send, 1<<31|l.nextID, paths, "")
	if err != nil {
		return err
	}
	l.note(st.String() + " sent to " + l.server)
	return nil
}

func (l *dataLink) close() { l.data.Close() }
