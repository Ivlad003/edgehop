// Package rview is the experimental "remote windows" feature: a window of a
// controlled computer is shown in a window on the computer with the mouse,
// and takes its mouse and keyboard input there.
//
// The controlled computer is the source: it takes pictures of the window,
// sends the parts that changed, and replays the input it gets back. The
// computer with the mouse is the viewer: a small local web server that feeds
// a browser window. The two talk over a view connection - one more encrypted
// connection next to the control and data connections, opened per window, so
// nothing here can hold up or break the sharing of the mouse and keyboard.
package rview

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Conn is one view connection.
type Conn interface {
	Send([]byte) error
	Recv() ([]byte, error)
	Close() error
}

// Messages on a view connection. The first byte is the type.
const (
	// viewer -> source
	mList    = 1  // which windows are there?
	mStart   = 2  // show this window: id u64
	mPointer = 3  // x u16, y u16: a position in the picture, 0..65535 on both axes
	mButton  = 4  // x u16, y u16, button u8, down u8
	mWheel   = 5  // x u16, y u16, dx i16, dy i16 (1/120 of a notch)
	mKey     = 6  // code u16, state u8
	mAck     = 7  // seq u32: update seq is on the screen, send the next one
	mRelease = 8  // let go of every key and button (the viewer lost the focus)
	mFront   = 9  // bring the window to the front and fully onto its screen
	mResize  = 10 // give the window this size: w u16, h u16
	mHide    = 11 // u8: 1 hides the window on its own screen while it is shown here, 0 shows it

	// source -> viewer
	mWindows = 0x20 // JSON: the window list
	mMeta    = 0x21 // JSON: title and size of the window being shown
	mRect    = 0x22 // one changed rectangle, see rectHeader; long ones continue in mMore
	mMore    = 0x23 // the next piece of the rectangle
	mEnd     = 0x24 // the source stops: reason as text
)

// Picture formats of a rectangle.
const (
	fmtJPEG = 1
	fmtPNG  = 2
)

const (
	rectHeaderLen = 1 + 4 + 2 + 2 + 2 + 2 + 1 + 1 + 4
	chunkSize     = 48 * 1024 // well below the largest frame the secure channel takes
	maxRectBytes  = 64 << 20
)

var be = binary.BigEndian

// rectHeader describes one rectangle of an update. All rectangles of an
// update share seq; the last one has last set.
type rectHeader struct {
	seq        uint32
	x, y, w, h int
	format     uint8
	last       bool
	total      int // bytes of picture data
}

func appendRectHeader(b []byte, h rectHeader) []byte {
	b = append(b, mRect)
	b = be.AppendUint32(b, h.seq)
	b = be.AppendUint16(b, uint16(h.x))
	b = be.AppendUint16(b, uint16(h.y))
	b = be.AppendUint16(b, uint16(h.w))
	b = be.AppendUint16(b, uint16(h.h))
	last := byte(0)
	if h.last {
		last = 1
	}
	b = append(b, h.format, last)
	return be.AppendUint32(b, uint32(h.total))
}

func parseRectHeader(b []byte) (rectHeader, error) {
	if len(b) < rectHeaderLen || b[0] != mRect {
		return rectHeader{}, errors.New("rview: bad rectangle header")
	}
	h := rectHeader{
		seq: be.Uint32(b[1:]),
		x:   int(be.Uint16(b[5:])), y: int(be.Uint16(b[7:])),
		w: int(be.Uint16(b[9:])), h: int(be.Uint16(b[11:])),
		format: b[13], last: b[14] != 0,
		total: int(be.Uint32(b[15:])),
	}
	if h.total < 0 || h.total > maxRectBytes {
		return h, fmt.Errorf("rview: rectangle of %d bytes", h.total)
	}
	return h, nil
}

// sendRect sends one rectangle, in pieces if it is long.
func sendRect(conn Conn, buf []byte, h rectHeader, data []byte) ([]byte, error) {
	h.total = len(data)
	buf = appendRectHeader(buf[:0], h)
	n := min(len(data), chunkSize)
	buf = append(buf, data[:n]...)
	if err := conn.Send(buf); err != nil {
		return buf, err
	}
	for data = data[n:]; len(data) > 0; data = data[n:] {
		n = min(len(data), chunkSize)
		buf = append(append(buf[:0], mMore), data[:n]...)
		if err := conn.Send(buf); err != nil {
			return buf, err
		}
	}
	return buf, nil
}

// recvRect completes a rectangle whose first frame was just read: it reads
// the remaining pieces and returns header and picture data.
func recvRect(conn Conn, first []byte) (rectHeader, []byte, error) {
	h, err := parseRectHeader(first)
	if err != nil {
		return h, nil, err
	}
	data := make([]byte, 0, h.total)
	data = append(data, first[rectHeaderLen:]...)
	for len(data) < h.total {
		more, err := conn.Recv()
		if err != nil {
			return h, nil, err
		}
		if len(more) == 0 || more[0] != mMore {
			return h, nil, errors.New("rview: a rectangle was cut short")
		}
		data = append(data, more[1:]...)
	}
	if len(data) != h.total {
		return h, nil, errors.New("rview: a rectangle is longer than announced")
	}
	return h, data, nil
}

func appendPos(b []byte, typ byte, x, y uint16) []byte {
	return be.AppendUint16(be.AppendUint16(append(b, typ), x), y)
}

func appendText(typ byte, s string) []byte {
	return append([]byte{typ}, s...)
}
