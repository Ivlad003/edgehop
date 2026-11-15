package rview

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/logx"
	"github.com/Ivlad003/edgehop/internal/wincap"
)

// Windows is the set of windows of the source computer; wincap.System is the
// real one.
type Windows interface {
	List() ([]wincap.Window, error)
	Open(id uint64) (wincap.Capturer, error)
}

// Injector is the part of input.Injector the source needs.
type Injector interface {
	MoveAbs(x, y int)
	Button(button uint8, down bool)
	Wheel(dx, dy int)
	Key(code uint16, state uint8)
}

// Source shows windows of this computer to a viewer and replays the input
// the viewer sends back.
type Source struct {
	Windows  Windows
	Injector Injector
	Name     string // this computer's screen name
	OS       string

	// Fast and Slow are the pauses between two pictures while the window is
	// in use and while nothing happens. Zero means the default.
	Fast, Slow time.Duration
}

const (
	defaultFast  = 50 * time.Millisecond
	defaultSlow  = 250 * time.Millisecond
	calmAfter    = 2 * time.Second        // without changes or input: take pictures slowly
	refineAfter  = 400 * time.Millisecond // without changes: resend the blurry parts sharp
	lossyAbove   = 0.25                   // share of the picture that changed, above which it is sent as JPEG
	jpegQuality  = 80
	focusWait    = 300 * time.Millisecond
	ackTimeout   = 10 * time.Second
	maxFailures  = 40
	maxListed    = 200
	maxListBytes = 40000
)

// listReply is the answer to mList.
type listReply struct {
	Name    string          `json:"name"`
	OS      string          `json:"os"`
	Windows []wincap.Window `json:"windows"`
	Error   string          `json:"error,omitempty"`
}

// metaInfo describes the window being shown.
type metaInfo struct {
	Title string `json:"title"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	Name  string `json:"name"`
	OS    string `json:"os"`
}

// view is one view connection on the source side.
type view struct {
	src    *Source
	conn   Conn
	sendMu sync.Mutex // the pieces of one rectangle must stay together
	acks   chan uint32
	stop   chan struct{}
	done   chan struct{} // closed when the picture loop has ended
	active atomic.Int64  // unix nanoseconds of the last change or input

	mu         sync.Mutex // everything below
	cap        wincap.Capturer
	x, y, w, h int // where the last picture sits on the desktop
	held       map[uint16]bool
	buttons    map[uint8]bool
}

// Serve handles one view connection until it closes.
func (s *Source) Serve(conn Conn) {
	v := &view{
		src: s, conn: conn,
		acks: make(chan uint32, 8), stop: make(chan struct{}),
		held: map[uint16]bool{}, buttons: map[uint8]bool{},
	}
	defer v.end()
	for {
		msg, err := conn.Recv()
		if err != nil {
			return
		}
		if len(msg) == 0 {
			continue
		}
		switch msg[0] {
		case mList:
			v.list()
		case mStart:
			if len(msg) >= 9 {
				v.start(be.Uint64(msg[1:]))
			}
		case mAck:
			if len(msg) >= 5 {
				select {
				case v.acks <- be.Uint32(msg[1:]):
				default:
				}
			}
		case mPointer, mButton, mWheel, mKey, mRelease:
			v.input(msg)
		}
	}
}

func (v *view) send(frame []byte) error {
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
	return v.conn.Send(frame)
}

func (v *view) list() {
	reply := listReply{Name: v.src.Name, OS: v.src.OS}
	wins, err := v.src.Windows.List()
	if err != nil {
		reply.Error = err.Error()
	}
	if len(wins) > maxListed {
		wins = wins[:maxListed]
	}
	reply.Windows = wins
	data, _ := json.Marshal(reply)
	for len(data) > maxListBytes && len(reply.Windows) > 1 { // must fit one frame
		reply.Windows = reply.Windows[:len(reply.Windows)/2]
		data, _ = json.Marshal(reply)
	}
	v.send(append([]byte{mWindows}, data...))
}

func (v *view) start(id uint64) {
	v.mu.Lock()
	running := v.cap != nil
	v.mu.Unlock()
	if running {
		return
	}
	c, err := v.src.Windows.Open(id)
	if err != nil {
		v.send(appendText(mEnd, err.Error()))
		return
	}
	v.mu.Lock()
	v.cap = c
	v.mu.Unlock()
	v.done = make(chan struct{})
	v.active.Store(time.Now().UnixNano())
	go v.pictures(c)
}

// end stops everything this connection started.
func (v *view) end() {
	close(v.stop)
	v.conn.Close()
	if v.done != nil {
		<-v.done
	}
	v.mu.Lock()
	v.release()
	if v.cap != nil {
		v.cap.Close()
		v.cap = nil
	}
	v.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Pictures

func (v *view) pause(d time.Duration) bool {
	select {
	case <-v.stop:
		return false
	case <-time.After(d):
		return true
	}
}

func (v *view) awaitAck(seq uint32) bool {
	timeout := time.NewTimer(ackTimeout)
	defer timeout.Stop()
	for {
		select {
		case <-v.stop:
			return false
		case a := <-v.acks:
			if a == seq {
				return true
			}
		case <-timeout.C:
			return true // a stalled viewer must not stop the pictures for good
		}
	}
}

// pictures takes pictures of the window and sends what changed, until the
// window or the connection is gone.
func (v *view) pictures(c wincap.Capturer) {
	defer close(v.done)
	fast, slow := v.src.Fast, v.src.Slow
	if fast <= 0 {
		fast = defaultFast
	}
	if slow <= 0 {
		slow = defaultSlow
	}
	var (
		cur, prev  wincap.Frame
		dirty      []bool
		lossy      []bool // tiles the viewer has only a blurry version of
		anyLossy   bool
		lastChange time.Time
		lastMeta   metaInfo
		seq        uint32
		failures   int
		enc        bytes.Buffer
		buf        []byte
		pngEnc     = png.Encoder{CompressionLevel: png.BestSpeed}
	)
	for first := true; ; first = false {
		if !first {
			d := fast
			if time.Since(time.Unix(0, v.active.Load())) > calmAfter && !anyLossy {
				d = slow
			}
			if !v.pause(d) {
				return
			}
		}
		if err := c.Capture(&cur); err != nil {
			if errors.Is(err, wincap.ErrGone) {
				v.send(appendText(mEnd, "the window was closed"))
				return
			}
			if failures++; failures > maxFailures {
				v.send(appendText(mEnd, err.Error()))
				return
			}
			continue
		}
		failures = 0
		w, h := cur.W, cur.H
		if w <= 0 || h <= 0 || w > 0xFFFF || h > 0xFFFF || len(cur.Pix) < w*h*4 {
			continue
		}
		v.mu.Lock()
		v.x, v.y, v.w, v.h = cur.X, cur.Y, w, h
		v.mu.Unlock()

		resized := prev.W != w || prev.H != h
		if m := (metaInfo{Title: cur.Title, W: w, H: h, Name: v.src.Name, OS: v.src.OS}); m != lastMeta {
			data, _ := json.Marshal(m)
			if v.send(append([]byte{mMeta}, data...)) != nil {
				return
			}
			lastMeta = m
		}

		tw, th := tilesFor(w, h)
		var rects []image.Rectangle
		format := uint8(fmtPNG)
		switch {
		case resized:
			dirty, lossy = make([]bool, tw*th), make([]bool, tw*th)
			rects = []image.Rectangle{image.Rect(0, 0, w, h)}
		default:
			if changedTiles(prev.Pix, cur.Pix, w, h, dirty) > 0 {
				rects = rectsOf(dirty, w, h)
			}
		}
		switch {
		case len(rects) > 0:
			lastChange = time.Now()
			v.active.Store(lastChange.UnixNano())
			if float64(area(rects)) > lossyAbove*float64(w*h) {
				format = fmtJPEG
			}
			markTiles(lossy, rects, w, h, format == fmtJPEG)
			anyLossy = anyLossy || format == fmtJPEG
		case anyLossy && time.Since(lastChange) > refineAfter:
			// Nothing moves any more: send the blurry parts again, sharp.
			rects = rectsOf(lossy, w, h)
			clear(lossy)
			anyLossy = false
		}
		if len(rects) == 0 {
			continue
		}
		if format == fmtPNG && anyLossy {
			// A sharp update covers some tiles; others may still be blurry.
			anyLossy = false
			for _, l := range lossy {
				anyLossy = anyLossy || l
			}
		}

		seq++
		img := &image.RGBA{Pix: cur.Pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
		for i, r := range rects {
			enc.Reset()
			var err error
			if format == fmtJPEG {
				err = jpeg.Encode(&enc, img.SubImage(r), &jpeg.Options{Quality: jpegQuality})
			} else {
				err = pngEnc.Encode(&enc, img.SubImage(r))
			}
			if err != nil {
				logx.Debugf("remote windows: cannot encode a picture: %v", err)
				return
			}
			hdr := rectHeader{seq: seq, x: r.Min.X, y: r.Min.Y, w: r.Dx(), h: r.Dy(), format: format, last: i == len(rects)-1}
			v.sendMu.Lock()
			buf, err = sendRect(v.conn, buf, hdr, enc.Bytes())
			v.sendMu.Unlock()
			if err != nil {
				return
			}
		}
		cur, prev = prev, cur // prev is now what the viewer has
		if !v.awaitAck(seq) {
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Input

// point converts a position in the picture to desktop coordinates.
func (v *view) point(msg []byte) (int, int, bool) {
	if v.w <= 0 || v.h <= 0 {
		return 0, 0, false
	}
	px, py := int(be.Uint16(msg[1:])), int(be.Uint16(msg[3:]))
	return v.x + px*(v.w-1)/0xFFFF, v.y + py*(v.h-1)/0xFFFF, true
}

// focus makes the window the one that gets the keyboard, as far as the
// system allows, and reports whether it is.
func (v *view) focus() bool {
	if v.cap.Front() {
		return true
	}
	v.cap.Raise()
	for waited := time.Duration(0); waited < focusWait; waited += 20 * time.Millisecond {
		time.Sleep(20 * time.Millisecond)
		if v.cap.Front() {
			return true
		}
	}
	return false
}

func (v *view) input(msg []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cap == nil {
		return // no window is shown on this connection
	}
	v.active.Store(time.Now().UnixNano())
	inj := v.src.Injector
	switch msg[0] {
	case mPointer:
		if len(msg) < 5 {
			return
		}
		if x, y, ok := v.point(msg); ok {
			inj.MoveAbs(x, y)
		}
	case mButton:
		if len(msg) < 7 {
			return
		}
		button, down := msg[5], msg[6] != 0
		if button == 0 || button > keys.ButtonForward || v.buttons[button] == down {
			return
		}
		x, y, ok := v.point(msg)
		if !ok {
			return
		}
		if down {
			v.focus() // the click itself activates the window once it is on top
		}
		inj.MoveAbs(x, y)
		inj.Button(button, down)
		if down {
			v.buttons[button] = true
		} else {
			delete(v.buttons, button)
		}
	case mWheel:
		if len(msg) < 9 {
			return
		}
		if x, y, ok := v.point(msg); ok {
			inj.MoveAbs(x, y)
			inj.Wheel(int(int16(be.Uint16(msg[5:]))), int(int16(be.Uint16(msg[7:]))))
		}
	case mKey:
		if len(msg) < 4 {
			return
		}
		code, state := be.Uint16(msg[1:]), msg[3]
		if code == 0 || code >= keys.MaxCode {
			return
		}
		if state == keys.Up {
			if v.held[code] {
				delete(v.held, code)
				inj.Key(code, keys.Up)
			}
			return
		}
		if v.held[code] {
			inj.Key(code, keys.Repeat)
			return
		}
		// Typing must never land in some other window of that computer.
		if !v.focus() {
			return
		}
		v.held[code] = true
		inj.Key(code, keys.Down)
	case mRelease:
		v.release()
	}
}

// release lets go of every key and button this connection holds down.
func (v *view) release() {
	for code := range v.held {
		v.src.Injector.Key(code, keys.Up)
	}
	clear(v.held)
	for b := range v.buttons {
		v.src.Injector.Button(b, false)
	}
	clear(v.buttons)
}
