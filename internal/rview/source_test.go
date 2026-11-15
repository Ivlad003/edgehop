package rview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/wincap"
)

// ---------------------------------------------------------------------------
// Fakes

// pipeConn is one end of an in-memory view connection.
type pipeConn struct {
	in     <-chan []byte
	out    chan<- []byte
	closed chan struct{}
	once   *sync.Once
}

func newPipe() (*pipeConn, *pipeConn) {
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	closed, once := make(chan struct{}), &sync.Once{}
	return &pipeConn{in: a, out: b, closed: closed, once: once}, &pipeConn{in: b, out: a, closed: closed, once: once}
}

func (p *pipeConn) Send(b []byte) error {
	select {
	case <-p.closed:
		return errors.New("closed")
	case p.out <- append([]byte(nil), b...):
		return nil
	}
}

func (p *pipeConn) Recv() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.closed:
		return nil, errors.New("closed")
	}
}

func (p *pipeConn) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

// fakeDesk is a computer with one window that the test paints.
type fakeDesk struct {
	mu     sync.Mutex
	pix    []byte
	w, h   int
	x, y   int
	title  string
	gone   bool
	front  bool
	raises int
	raised bool // whether raising brings the window to the front
	closed bool
}

func newDesk(w, h int) *fakeDesk {
	d := &fakeDesk{w: w, h: h, x: 300, y: 200, title: "Notes", pix: make([]byte, w*h*4), raised: true}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d.set(x, y, byte(x), byte(y), byte(x+y))
		}
	}
	return d
}

func (d *fakeDesk) set(x, y int, r, g, b byte) {
	i := (y*d.w + x) * 4
	d.pix[i], d.pix[i+1], d.pix[i+2], d.pix[i+3] = r, g, b, 0xFF
}

// paint fills a rectangle with one colour.
func (d *fakeDesk) paint(r image.Rectangle, c [3]byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			d.set(x, y, c[0], c[1], c[2])
		}
	}
}

func (d *fakeDesk) List() ([]wincap.Window, error) {
	return []wincap.Window{{ID: 7, Title: d.title, App: "notes", W: d.w, H: d.h}}, nil
}

func (d *fakeDesk) Open(id uint64) (wincap.Capturer, error) {
	if id != 7 {
		return nil, wincap.ErrGone
	}
	return d, nil
}

func (d *fakeDesk) Capture(f *wincap.Frame) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.gone {
		return wincap.ErrGone
	}
	f.Pix = append(f.Pix[:0], d.pix...)
	f.W, f.H, f.X, f.Y, f.Title = d.w, d.h, d.x, d.y, d.title
	return nil
}

func (d *fakeDesk) Front() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.front
}

func (d *fakeDesk) Raise() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.raises++
	d.front = d.raised
}

func (d *fakeDesk) Close() {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
}

// fakeInjector records what would have been done to the mouse and keyboard.
type fakeInjector struct {
	mu  sync.Mutex
	log []string
}

func (f *fakeInjector) add(format string, args ...any) {
	f.mu.Lock()
	f.log = append(f.log, fmt.Sprintf(format, args...))
	f.mu.Unlock()
}
func (f *fakeInjector) MoveAbs(x, y int)         { f.add("move %d,%d", x, y) }
func (f *fakeInjector) Button(b uint8, d bool)   { f.add("button %d %v", b, d) }
func (f *fakeInjector) Wheel(dx, dy int)         { f.add("wheel %d,%d", dx, dy) }
func (f *fakeInjector) Key(code uint16, s uint8) { f.add("key %d %d", code, s) }
func (f *fakeInjector) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.log)
}
func (f *fakeInjector) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.log
	f.log = nil
	return out
}

// ---------------------------------------------------------------------------
// A viewer for the tests

type testViewer struct {
	t    *testing.T
	conn Conn
	msgs chan []byte // everything the source sent, in order
	pic  *image.RGBA // what the viewer shows
	meta metaInfo
}

type update struct {
	seq    uint32
	rects  []image.Rectangle
	format uint8
}

func startSource(t *testing.T, desk *fakeDesk, inj *fakeInjector) *testViewer {
	t.Helper()
	a, b := newPipe()
	src := &Source{Windows: desk, Injector: inj, Name: "win", OS: "windows", Fast: 5 * time.Millisecond, Slow: 20 * time.Millisecond}
	done := make(chan struct{})
	go func() { src.Serve(a); close(done) }()
	t.Cleanup(func() {
		b.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("the source did not stop")
		}
	})
	v := &testViewer{t: t, conn: b, msgs: make(chan []byte, 1024)}
	go func() {
		defer close(v.msgs)
		for {
			msg, err := b.Recv()
			if err != nil {
				return
			}
			v.msgs <- msg
		}
	}()
	return v
}

// Recv, Send and Close make the viewer itself a Conn that reads from the
// queue, for recvRect.
func (v *testViewer) Recv() ([]byte, error) {
	select {
	case msg, ok := <-v.msgs:
		if !ok {
			return nil, errors.New("closed")
		}
		return msg, nil
	case <-time.After(5 * time.Second):
		return nil, errors.New("timeout")
	}
}
func (v *testViewer) Send(b []byte) error { return v.conn.Send(b) }
func (v *testViewer) Close() error        { return v.conn.Close() }

// recv returns the next message, or nil when none arrives in time.
func (v *testViewer) recv(wait time.Duration) []byte {
	select {
	case msg := <-v.msgs:
		return msg
	case <-time.After(wait):
		return nil
	}
}

// next reads one complete update and applies it to the viewer's picture.
// It returns nil when nothing arrives in time.
func (v *testViewer) next(wait time.Duration) *update {
	v.t.Helper()
	var up *update
	for {
		msg := v.recv(wait)
		if msg == nil {
			if up != nil {
				v.t.Fatal("an update stopped half way")
			}
			return nil
		}
		switch msg[0] {
		case mMeta:
			if err := json.Unmarshal(msg[1:], &v.meta); err != nil {
				v.t.Fatal(err)
			}
			if v.pic == nil || v.pic.Rect.Dx() != v.meta.W || v.pic.Rect.Dy() != v.meta.H {
				v.pic = image.NewRGBA(image.Rect(0, 0, v.meta.W, v.meta.H))
			}
		case mRect:
			h, data, err := recvRect(v, msg)
			if err != nil {
				v.t.Fatal(err)
			}
			var img image.Image
			if h.format == fmtJPEG {
				img, err = jpeg.Decode(bytes.NewReader(data))
			} else {
				img, err = png.Decode(bytes.NewReader(data))
			}
			if err != nil {
				v.t.Fatalf("a rectangle does not decode: %v", err)
			}
			if img.Bounds().Dx() != h.w || img.Bounds().Dy() != h.h {
				v.t.Fatalf("rectangle announced as %dx%d, picture is %v", h.w, h.h, img.Bounds())
			}
			if up == nil {
				up = &update{seq: h.seq, format: h.format}
			}
			if up.seq != h.seq || up.format != h.format {
				v.t.Fatalf("mixed update: %+v then %+v", *up, h)
			}
			r := image.Rect(h.x, h.y, h.x+h.w, h.y+h.h)
			up.rects = append(up.rects, r)
			for y := 0; y < h.h; y++ {
				for x := 0; x < h.w; x++ {
					v.pic.Set(h.x+x, h.y+y, img.At(img.Bounds().Min.X+x, img.Bounds().Min.Y+y))
				}
			}
			if h.last {
				return up
			}
		case mEnd:
			v.t.Fatalf("the source ended: %s", msg[1:])
		}
	}
}

func (v *testViewer) ack(seq uint32) {
	v.conn.Send(be.AppendUint32([]byte{mAck}, seq))
}

func (v *testViewer) start() {
	v.conn.Send(be.AppendUint64([]byte{mStart}, 7))
}

// sharp brings the viewer to the point where it has the exact picture.
func (v *testViewer) sharp(desk *fakeDesk) {
	v.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		up := v.next(2 * time.Second)
		if up == nil {
			break
		}
		v.ack(up.seq)
		if v.same(desk) {
			return
		}
	}
	v.t.Fatal("the viewer never got the exact picture")
}

func (v *testViewer) same(desk *fakeDesk) bool {
	desk.mu.Lock()
	defer desk.mu.Unlock()
	return v.pic != nil && bytes.Equal(v.pic.Pix, desk.pix)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// ---------------------------------------------------------------------------

func TestWindowListAndPicture(t *testing.T) {
	desk := newDesk(300, 200)
	v := startSource(t, desk, &fakeInjector{})

	v.conn.Send([]byte{mList})
	msg := v.recv(3 * time.Second)
	if msg == nil || msg[0] != mWindows {
		t.Fatalf("no window list: %v", msg)
	}
	var list listReply
	if err := json.Unmarshal(msg[1:], &list); err != nil {
		t.Fatal(err)
	}
	if list.Name != "win" || list.OS != "windows" || len(list.Windows) != 1 || list.Windows[0].Title != "Notes" {
		t.Fatalf("list = %+v", list)
	}

	// The first picture comes at once and whole; lossy, to be quick.
	v.start()
	up := v.next(3 * time.Second)
	if up == nil {
		t.Fatal("no picture")
	}
	if v.meta.Title != "Notes" || v.meta.W != 300 || v.meta.H != 200 || v.meta.Name != "win" {
		t.Errorf("meta = %+v", v.meta)
	}
	if len(up.rects) != 1 || up.rects[0] != image.Rect(0, 0, 300, 200) || up.format != fmtJPEG {
		t.Errorf("first update = %+v", *up)
	}
	v.ack(up.seq)

	// Once nothing moves, the same area follows without loss.
	up = v.next(3 * time.Second)
	if up == nil || up.format != fmtPNG {
		t.Fatalf("no sharp picture after the first one: %+v", up)
	}
	v.ack(up.seq)
	if !v.same(desk) {
		t.Fatal("the viewer's picture differs from the window")
	}
	// And then nothing, as long as the window does not change.
	if up := v.next(150 * time.Millisecond); up != nil {
		t.Errorf("an update although nothing changed: %+v", *up)
	}
}

func TestOnlyWhatChangedIsSent(t *testing.T) {
	desk := newDesk(300, 200)
	v := startSource(t, desk, &fakeInjector{})
	v.start()
	v.sharp(desk)

	// A few pixels change: one tile is sent, exact.
	desk.paint(image.Rect(100, 70, 110, 80), [3]byte{250, 10, 10})
	up := v.next(3 * time.Second)
	if up == nil {
		t.Fatal("the change was not sent")
	}
	if len(up.rects) != 1 || up.rects[0] != image.Rect(96, 64, 128, 96) || up.format != fmtPNG {
		t.Errorf("update = %+v, want one 32x32 tile at 96,64 as PNG", *up)
	}
	v.ack(up.seq)
	if !v.same(desk) {
		t.Error("the viewer's picture differs from the window after a small change")
	}

	// Two separate spots: two rectangles in one update. The bottom-right
	// tile is cut off by the picture's edge.
	desk.paint(image.Rect(0, 0, 5, 5), [3]byte{1, 2, 3})
	desk.paint(image.Rect(295, 195, 300, 200), [3]byte{4, 5, 6})
	up = v.next(3 * time.Second)
	if up == nil || len(up.rects) != 2 || up.rects[0] != image.Rect(0, 0, 32, 32) || up.rects[1] != image.Rect(288, 192, 300, 200) {
		t.Errorf("update = %+v, want the first and the last tile", up)
	}
	v.ack(up.seq)
	if !v.same(desk) {
		t.Error("the viewer's picture differs from the window after two changes")
	}

	// A large change travels lossy first and is sharpened afterwards.
	desk.paint(image.Rect(0, 0, 300, 150), [3]byte{30, 120, 200})
	up = v.next(3 * time.Second)
	if up == nil || up.format != fmtJPEG {
		t.Fatalf("a large change must be sent as JPEG: %+v", up)
	}
	v.ack(up.seq)
	v.sharp(desk)
}

// The source waits for the viewer before it sends more: a slow viewer or a
// slow network gets fewer pictures, not a growing queue of old ones.
func TestNextPictureWaitsForTheViewer(t *testing.T) {
	desk := newDesk(128, 96)
	v := startSource(t, desk, &fakeInjector{})
	v.start()
	first := v.next(3 * time.Second)
	if first == nil {
		t.Fatal("no picture")
	}
	desk.paint(image.Rect(0, 0, 10, 10), [3]byte{9, 9, 9})
	if up := v.next(200 * time.Millisecond); up != nil {
		t.Fatalf("update %d arrived before update %d was confirmed", up.seq, first.seq)
	}
	v.ack(first.seq)
	if up := v.next(3 * time.Second); up == nil || up.seq != first.seq+1 {
		t.Fatalf("no update after the confirmation: %+v", up)
	}
}

func TestWindowChangesSizeAndTitle(t *testing.T) {
	desk := newDesk(128, 96)
	v := startSource(t, desk, &fakeInjector{})
	v.start()
	v.sharp(desk)

	desk.mu.Lock()
	desk.w, desk.h, desk.title = 160, 64, "Notes - changed"
	desk.pix = make([]byte, 160*64*4)
	for i := 3; i < len(desk.pix); i += 4 {
		desk.pix[i] = 0xFF
	}
	desk.mu.Unlock()
	v.sharp(desk)
	if v.meta.W != 160 || v.meta.H != 64 || v.meta.Title != "Notes - changed" {
		t.Errorf("meta = %+v", v.meta)
	}
}

func TestClosedWindowEndsTheView(t *testing.T) {
	desk := newDesk(64, 64)
	v := startSource(t, desk, &fakeInjector{})
	v.start()
	up := v.next(3 * time.Second)
	v.ack(up.seq)
	desk.mu.Lock()
	desk.gone = true
	desk.mu.Unlock()
	for {
		msg := v.recv(3 * time.Second)
		if msg == nil {
			t.Fatal("the viewer was not told that the window is gone")
		}
		if msg[0] == mEnd {
			break
		}
		if msg[0] == mRect {
			h, _, err := recvRect(v, msg)
			if err != nil {
				t.Fatal(err)
			}
			if h.last {
				v.ack(h.seq)
			}
		}
	}

	// A window that does not exist is refused straight away.
	other := startSource(t, newDesk(64, 64), &fakeInjector{})
	other.conn.Send(be.AppendUint64([]byte{mStart}, 99))
	if msg := other.recv(3 * time.Second); msg == nil || msg[0] != mEnd {
		t.Errorf("unknown window: %v", msg)
	}
}

func TestInputLandsInTheWindow(t *testing.T) {
	desk := newDesk(640, 480) // at 300,200 on the desktop
	inj := &fakeInjector{}
	v := startSource(t, desk, inj)
	pos := func(typ byte, x, y uint16, more ...byte) []byte { return append(appendPos(nil, typ, x, y), more...) }

	// Before a window is shown, input goes nowhere.
	v.conn.Send(pos(mPointer, 100, 100))
	v.conn.Send([]byte{mKey, 0, keys.A, keys.Down})
	v.start()
	up := v.next(3 * time.Second)
	v.ack(up.seq)
	if got := inj.take(); len(got) != 0 {
		t.Fatalf("input before the window was shown: %v", got)
	}

	// Positions are relative to the picture: its corners and its middle.
	v.conn.Send(pos(mPointer, 0, 0))
	v.conn.Send(pos(mPointer, 0xFFFF, 0xFFFF))
	v.conn.Send(pos(mPointer, 0x8000, 0x8000))
	eventually(t, "three moves", func() bool { return inj.count() >= 3 })
	want := []string{"move 300,200", "move 939,679", "move 619,439"}
	if got := inj.take(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("moves = %v, want %v", got, want)
	}

	// A key press while another window is in front there: the window is
	// raised first, and the key is typed only because that worked.
	v.conn.Send([]byte{mKey, 0, keys.A, keys.Down})
	v.conn.Send([]byte{mKey, 0, keys.A, keys.Down}) // the browser repeats held keys as key-down
	v.conn.Send([]byte{mKey, 0, keys.A, keys.Up})
	v.conn.Send([]byte{mKey, 0, keys.A, keys.Up}) // a second release is ignored
	eventually(t, "the key", func() bool { return inj.count() >= 3 })
	want = []string{"key 30 1", "key 30 2", "key 30 0"}
	if got := inj.take(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
	desk.mu.Lock()
	raises := desk.raises
	desk.mu.Unlock()
	if raises != 1 {
		t.Errorf("the window was raised %d times, want once", raises)
	}

	// A click goes to its position; wheel steps too.
	v.conn.Send(pos(mButton, 0, 0, keys.ButtonLeft, 1))
	v.conn.Send(pos(mButton, 0, 0, keys.ButtonLeft, 0))
	v.conn.Send(pos(mWheel, 0, 0, 0, 0, 0xFF, 0x88)) // dy = -120
	eventually(t, "click and wheel", func() bool { return inj.count() >= 6 })
	want = []string{"move 300,200", "button 1 true", "move 300,200", "button 1 false", "move 300,200", "wheel 0,-120"}
	if got := inj.take(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("click = %v, want %v", got, want)
	}

	// The window moves on the desktop: positions follow.
	desk.mu.Lock()
	desk.x, desk.y = 10, 20
	desk.mu.Unlock()
	eventually(t, "the new position", func() bool {
		v.conn.Send(pos(mPointer, 0, 0))
		time.Sleep(10 * time.Millisecond)
		got := inj.take()
		return len(got) > 0 && got[len(got)-1] == "move 10,20"
	})

	// Whatever is still held when the viewer goes away is released.
	v.conn.Send([]byte{mKey, 0, keys.A, keys.Down})
	v.conn.Send(pos(mButton, 0, 0, keys.ButtonRight, 1))
	eventually(t, "key and button down", func() bool { return inj.count() >= 3 })
	inj.take()
	v.conn.Close()
	eventually(t, "the release", func() bool { return inj.count() >= 2 })
	got := fmt.Sprint(inj.take())
	if got != "[key 30 0 button 2 false]" {
		t.Errorf("released %v, want the key and the button", got)
	}
	eventually(t, "the capturer to be closed", func() bool { desk.mu.Lock(); defer desk.mu.Unlock(); return desk.closed })
}

// A key is never typed into some other window of that computer.
func TestKeysAreDroppedWhenTheWindowCannotComeToFront(t *testing.T) {
	desk := newDesk(64, 64)
	desk.raised = false // raising does not help
	inj := &fakeInjector{}
	v := startSource(t, desk, inj)
	v.start()
	up := v.next(3 * time.Second)
	v.ack(up.seq)

	v.conn.Send([]byte{mKey, 0, keys.A, keys.Down})
	v.conn.Send([]byte{mKey, 0, keys.A, keys.Up})
	v.conn.Send(appendPos(nil, mPointer, 0, 0)) // a marker: it arrives after the keys were handled
	eventually(t, "the marker", func() bool { return inj.count() >= 1 })
	if got := fmt.Sprint(inj.take()); got != "[move 300,200]" {
		t.Errorf("got %v, want only the cursor move", got)
	}
	// A click is still passed on: it is what brings a window to the front.
	v.conn.Send(append(appendPos(nil, mButton, 0, 0), keys.ButtonLeft, 1))
	eventually(t, "the click", func() bool { return inj.count() >= 2 })
}
