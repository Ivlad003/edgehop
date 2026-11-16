//go:build linux

package input

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
)

// These tests talk to the real kernel input layer: they create virtual
// devices through /dev/uinput and read /dev/input/event*. They need root (or
// the permissions from the README) and are therefore opt-in:
//
//	go test -c -o live.test ./internal/input/
//	sudo EDGEHOP_LIVE=1 ./live.test -test.run Live -test.v
//
// Nothing here depends on a desktop being present, so it runs on a headless
// CI machine.

func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("EDGEHOP_LIVE") != "1" {
		t.Skip("set EDGEHOP_LIVE=1 to test against the real kernel input layer (needs root)")
	}
	if _, err := os.Stat("/dev/uinput"); err != nil {
		t.Skipf("/dev/uinput is not available here: %v", err)
	}
}

func deviceName(path string) string {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return ""
	}
	defer syscall.Close(fd)
	buf := make([]byte, 256)
	ioctl(fd, eviocRead(0x06, len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	return strings.TrimRight(string(buf), "\x00")
}

// findNode waits for the newest /dev/input/eventN with the given device name.
func findNode(t *testing.T, name string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		best, bestN := "", -1
		paths, _ := filepath.Glob("/dev/input/event*")
		for _, p := range paths {
			n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(p), "event"))
			if err == nil && n > bestN && deviceName(p) == name {
				best, bestN = p, n
			}
		}
		if best != "" {
			return best
		}
		if time.Now().After(deadline) {
			t.Fatalf("the device %q did not appear in /dev/input", name)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type ev struct {
	Type  uint16
	Code  uint16
	Value int32
}

func (e ev) String() string {
	return fmt.Sprintf("{type %d code %d value %d}", e.Type, e.Code, e.Value)
}

// nodeReader reads what a device delivers to the rest of the system.
type nodeReader struct {
	t  *testing.T
	fd int
}

func openReader(t *testing.T, path string) *nodeReader {
	t.Helper()
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { syscall.Close(fd) })
	return &nodeReader{t: t, fd: fd}
}

// collect returns the events that arrive until the device has been quiet for
// the given time. Synchronisation markers are left out.
func (r *nodeReader) collect(quiet time.Duration) []ev {
	var out []ev
	buf := make([]byte, evSize*64)
	deadline := time.Now().Add(quiet)
	for time.Now().Before(deadline) {
		n, _ := syscall.Read(r.fd, buf)
		if n <= 0 {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		for off := 0; off+evSize <= n; off += evSize {
			e := (*inputEvent)(unsafe.Pointer(&buf[off]))
			if e.Type == evKey || e.Type == evRel || e.Type == evAbs {
				out = append(out, ev{e.Type, e.Code, e.Value})
			}
		}
		deadline = time.Now().Add(quiet)
	}
	return out
}

func expectEvs(t *testing.T, what string, got, want []ev) {
	t.Helper()
	if len(got) == len(want) {
		same := true
		for i := range got {
			if got[i] != want[i] {
				same = false
				break
			}
		}
		if same {
			return
		}
	}
	t.Errorf("%s:\n got %v\nwant %v", what, got, want)
}

// expectCaptured waits for exactly these events from the capture.
func expectCaptured(t *testing.T, what string, ch <-chan Event, want ...Event) {
	t.Helper()
	var got []Event
	timeout := time.After(3 * time.Second)
	for len(got) < len(want) {
		select {
		case e := <-ch:
			got = append(got, e)
		case <-timeout:
			t.Fatalf("%s: timed out; got %+v, want %+v", what, got, want)
		}
	}
	// Anything extra within a short moment is an error too.
	select {
	case e := <-ch:
		got = append(got, e)
	case <-time.After(100 * time.Millisecond):
	}
	if len(got) != len(want) {
		t.Fatalf("%s:\n got %+v\nwant %+v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: event %d:\n got %+v\nwant %+v", what, i, got[i], want[i])
		}
	}
}

func key(code uint16, value int32) inputEvent {
	return inputEvent{Type: evKey, Code: code, Value: value}
}

func rel(code uint16, value int32) inputEvent {
	return inputEvent{Type: evRel, Code: code, Value: value}
}

// TestLiveInjector checks that what the injector asks for is what the kernel
// delivers to the desktop.
func TestLiveInjector(t *testing.T) {
	requireLive(t)
	inj, err := NewInjector(Options{Width: 1280, Height: 720})
	if err != nil {
		t.Fatal(err)
	}
	defer inj.Close()
	if w, h := inj.Size(); w != 1280 || h != 720 {
		t.Fatalf("size = %dx%d", w, h)
	}
	kbd := openReader(t, findNode(t, virtualPrefix+" keyboard"))
	ptr := openReader(t, findNode(t, virtualPrefix+" pointer"))
	time.Sleep(200 * time.Millisecond)

	// Every key edgehop knows, pressed and released. They are read back in
	// small batches: the kernel keeps only 64 unread events per reader.
	var got, want []ev
	codes := keys.Codes()
	for i, code := range codes {
		inj.Key(code, keys.Down)
		inj.Key(code, keys.Repeat) // must be dropped: the desktop repeats by itself
		inj.Key(code, keys.Up)
		want = append(want, ev{evKey, code, 1}, ev{evKey, code, 0})
		if i%8 == 7 || i == len(codes)-1 {
			got = append(got, kbd.collect(40*time.Millisecond)...)
		}
	}
	expectEvs(t, fmt.Sprintf("%d keys", len(codes)), got, want)
	t.Logf("injection: %d keys delivered by the kernel exactly as sent", len(codes))

	inj.MoveAbs(100, 200)
	inj.MoveAbs(1279, 0)
	inj.MoveAbs(5000, 5000) // clamped to the bottom right corner
	expectEvs(t, "absolute motion", ptr.collect(200*time.Millisecond), []ev{
		{evAbs, absX, 100}, {evAbs, absY, 200},
		{evAbs, absX, 1279}, {evAbs, absY, 0},
		{evAbs, absY, 719},
	})

	for b, code := range map[uint8]uint16{
		keys.ButtonLeft: btnLeft, keys.ButtonRight: btnRight, keys.ButtonMiddle: btnMiddle,
		keys.ButtonBack: btnSide, keys.ButtonForward: btnExtra,
	} {
		inj.Button(b, true)
		inj.Button(b, false)
		got := ptr.collect(150 * time.Millisecond)
		expectEvs(t, fmt.Sprintf("button %d", b), got, []ev{{evKey, code, 1}, {evKey, code, 0}})
	}

	inj.Wheel(0, 120)  // one notch up
	inj.Wheel(0, -60)  // half a notch down: high resolution only
	inj.Wheel(0, -60)  // the second half completes a classic notch
	inj.Wheel(-240, 0) // two notches left
	expectEvs(t, "wheel", ptr.collect(200*time.Millisecond), []ev{
		{evRel, relWheelHiRes, 120}, {evRel, relWheel, 1},
		{evRel, relWheelHiRes, -60},
		{evRel, relWheelHiRes, -60}, {evRel, relWheel, -1},
		{evRel, relHWheelHiRes, -240}, {evRel, relHWheel, -2},
	})

	// Keys and buttons still held are released when the cursor leaves.
	inj.Key(keys.LeftShift, keys.Down)
	inj.Button(keys.ButtonLeft, true)
	kbd.collect(100 * time.Millisecond)
	ptr.collect(100 * time.Millisecond)
	inj.ReleaseAll()
	expectEvs(t, "ReleaseAll keys", kbd.collect(150*time.Millisecond), []ev{{evKey, keys.LeftShift, 0}})
	expectEvs(t, "ReleaseAll buttons", ptr.collect(150*time.Millisecond), []ev{{evKey, btnLeft, 0}})
}

// TestLiveCapture plugs two imitation "physical" devices into the kernel and
// checks that the capture grabs them, passes their input through in local
// mode, detects the screen edge, and swallows and reports input in remote mode.
func TestLiveCapture(t *testing.T) {
	requireLive(t)

	var keyBits []int
	for _, c := range keys.Codes() {
		keyBits = append(keyBits, int(c))
	}
	physKbd, err := createUinput("edgehop-ci physical keyboard", 0x101, keyBits, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer physKbd.close()
	physMouse, err := createUinput("edgehop-ci physical mouse", 0x102,
		[]int{btnLeft, btnRight, btnMiddle, btnSide, btnExtra},
		[]int{relX, relY, relWheel, relHWheel, relWheelHiRes, relHWheelHiRes}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer physMouse.close()
	findNode(t, "edgehop-ci physical keyboard")
	findNode(t, "edgehop-ci physical mouse")

	ci, err := NewCapture(Options{Width: 1000, Height: 500, PointerSpeed: 1, PointerAccel: 0})
	if err != nil {
		t.Fatal(err)
	}
	c := ci.(*linuxCapture)
	defer c.Close()
	desc := c.Describe()
	if !strings.Contains(desc, "edgehop-ci physical keyboard") || !strings.Contains(desc, "edgehop-ci physical mouse") {
		t.Fatalf("the imitation devices were not grabbed: %s", desc)
	}
	t.Logf("capture: %s", desc)

	// What the local desktop gets to see.
	outKbd := openReader(t, findNode(t, virtualPrefix+" keyboard"))
	outPtr := openReader(t, findNode(t, virtualPrefix+" pointer"))
	time.Sleep(200 * time.Millisecond)
	outPtr.collect(100 * time.Millisecond)

	// --- local mode: everything passes through, nothing is reported -----------
	physKbd.write(key(keys.A, 1))
	physKbd.write(key(keys.A, 2)) // kernel auto-repeat is not passed on
	physKbd.write(key(keys.A, 0))
	expectEvs(t, "local typing", outKbd.collect(150*time.Millisecond), []ev{{evKey, keys.A, 1}, {evKey, keys.A, 0}})

	// The cursor starts in the middle of the 1000x500 desktop.
	physMouse.write(rel(relX, 10), rel(relY, -5))
	expectEvs(t, "local motion", outPtr.collect(150*time.Millisecond), []ev{{evAbs, absX, 510}, {evAbs, absY, 245}})

	physMouse.write(key(btnLeft, 1))
	physMouse.write(key(btnLeft, 0))
	physMouse.write(rel(relWheelHiRes, 120), rel(relWheel, 1))
	expectEvs(t, "local click and wheel", outPtr.collect(150*time.Millisecond), []ev{
		{evKey, btnLeft, 1}, {evKey, btnLeft, 0},
		{evRel, relWheelHiRes, 120}, {evRel, relWheel, 1},
	})
	select {
	case e := <-c.Events():
		t.Fatalf("local mode reported an event: %+v", e)
	default:
	}

	// A drag must not trigger the edge.
	c.SetArmedEdges(layout.Right.Bit())
	physMouse.write(key(btnLeft, 1))
	physMouse.write(rel(relX, 3000))
	physMouse.write(key(btnLeft, 0))
	expectEvs(t, "drag to the edge", outPtr.collect(150*time.Millisecond), []ev{
		{evKey, btnLeft, 1}, {evAbs, absX, 999}, {evKey, btnLeft, 0},
	})
	select {
	case e := <-c.Events():
		t.Fatalf("the edge fired during a drag: %+v", e)
	default:
	}

	// --- the edge --------------------------------------------------------------
	// The cursor is 245 pixels below the top corner of the 500 pixel edge.
	edge := Event{Kind: EvEdge, Side: layout.Right, Frac: 245.0 / 499.0, Corner: 245, Span: 500}
	physMouse.write(rel(relX, 1))
	expectCaptured(t, "edge", c.Events(), edge)

	// With Ctrl held, the edge event says so.
	physKbd.write(key(keys.LeftCtrl, 1))
	expectEvs(t, "ctrl in local mode", outKbd.collect(150*time.Millisecond), []ev{{evKey, keys.LeftCtrl, 1}})
	withCtrl := edge
	withCtrl.Mods = keys.ModCtrl
	physMouse.write(rel(relX, 1))
	expectCaptured(t, "edge with ctrl held", c.Events(), withCtrl)

	// --- hotkeys in local mode -------------------------------------------------
	const two = keys.Num1 + 1 // the key "2"
	c.SetHotkeys([]Hotkey{
		{Hotkey: mustHotkey(t, "ctrl+alt+shift+backspace"), RemoteOnly: true},
		{Hotkey: mustHotkey(t, "ctrl+alt+2")},
	})
	// Ctrl+Alt+2: reported once, and the 2 reaches nobody - not as a press,
	// not as auto-repeat, not as a release. Alt was pressed before anyone
	// could know what it was for, so the desktop has seen it.
	physKbd.write(key(keys.LeftAlt, 1))
	physKbd.write(key(two, 1))
	physKbd.write(key(two, 2))
	physKbd.write(key(two, 0))
	expectCaptured(t, "hotkey in local mode", c.Events(), Event{Kind: EvHotkey, Code: 1})
	expectEvs(t, "the last key of a hotkey is hidden from the local desktop", outKbd.collect(150*time.Millisecond), []ev{{evKey, keys.LeftAlt, 1}})

	// Pressed while a mouse button is held, the hotkey says that too.
	physMouse.write(key(btnLeft, 1))
	expectEvs(t, "button down", outPtr.collect(150*time.Millisecond), []ev{{evKey, btnLeft, 1}})
	physKbd.write(key(two, 1))
	physKbd.write(key(two, 0))
	expectCaptured(t, "hotkey during a drag", c.Events(), Event{Kind: EvHotkey, Code: 1, Drag: true})
	physMouse.write(key(btnLeft, 0))
	expectEvs(t, "button up", outPtr.collect(150*time.Millisecond), []ev{{evKey, btnLeft, 0}})

	// The release hotkey only counts while another computer has the cursor:
	// here Ctrl+Alt+Shift+Backspace is typed like anything else.
	physKbd.write(key(keys.LeftShift, 1))
	physKbd.write(key(keys.Backspace, 1))
	physKbd.write(key(keys.Backspace, 0))
	physKbd.write(key(keys.LeftShift, 0))
	physKbd.write(key(keys.LeftAlt, 0))
	physKbd.write(key(keys.LeftCtrl, 0))
	expectEvs(t, "a remote-only hotkey in local mode is ordinary typing", outKbd.collect(150*time.Millisecond), []ev{
		{evKey, keys.LeftShift, 1}, {evKey, keys.Backspace, 1}, {evKey, keys.Backspace, 0},
		{evKey, keys.LeftShift, 0}, {evKey, keys.LeftAlt, 0}, {evKey, keys.LeftCtrl, 0},
	})
	select {
	case e := <-c.Events():
		t.Fatalf("a remote-only hotkey was reported in local mode: %+v", e)
	default:
	}
	// Ctrl is up again: the edge is a plain edge.
	physMouse.write(rel(relX, 1))
	expectCaptured(t, "edge after ctrl was released", c.Events(), edge)

	// Shift goes down locally, then the cursor crosses over.
	physKbd.write(key(keys.LeftShift, 1))
	expectEvs(t, "shift before the switch", outKbd.collect(150*time.Millisecond), []ev{{evKey, keys.LeftShift, 1}})
	c.Grab(false)

	// --- remote mode: swallowed and reported -----------------------------------
	physKbd.write(key(keys.B, 1))
	physKbd.write(key(keys.B, 2))
	physKbd.write(key(keys.B, 0))
	expectCaptured(t, "remote typing", c.Events(),
		Event{Kind: EvKey, Code: keys.B, State: keys.Down},
		Event{Kind: EvKey, Code: keys.B, State: keys.Repeat},
		Event{Kind: EvKey, Code: keys.B, State: keys.Up},
	)
	physMouse.write(rel(relX, 7), rel(relY, 3))
	physMouse.write(key(btnRight, 1))
	physMouse.write(key(btnRight, 0))
	physMouse.write(rel(relWheelHiRes, -120), rel(relWheel, -1))
	physMouse.write(rel(relHWheelHiRes, 240), rel(relHWheel, 2))
	expectCaptured(t, "remote mouse", c.Events(),
		// Shift is physically held (since before the switch): the movement says so.
		Event{Kind: EvMotion, DX: 7, DY: 3, Mods: keys.ModShift},
		Event{Kind: EvButton, Code: keys.ButtonRight, State: 1},
		Event{Kind: EvButton, Code: keys.ButtonRight, State: 0},
		Event{Kind: EvWheel, WY: -120},
		Event{Kind: EvWheel, WX: 240},
	)
	expectEvs(t, "nothing reaches the local desktop in remote mode (keyboard)", outKbd.collect(150*time.Millisecond), nil)
	expectEvs(t, "nothing reaches the local desktop in remote mode (pointer)", outPtr.collect(150*time.Millisecond), nil)

	// --- hotkeys in remote mode ------------------------------------------------
	// With the Shift from before, Ctrl and Alt pressed here, Backspace
	// completes the release hotkey. Ctrl and Alt are forwarded like any key;
	// Backspace is not, and neither is the 2 of the other hotkey.
	physKbd.write(key(keys.LeftCtrl, 1))
	physKbd.write(key(keys.LeftAlt, 1))
	physKbd.write(key(keys.Backspace, 1))
	physKbd.write(key(keys.Backspace, 2))
	physKbd.write(key(keys.Backspace, 0))
	physKbd.write(key(two, 1))
	physKbd.write(key(two, 0))
	physKbd.write(key(keys.LeftAlt, 0))
	physKbd.write(key(keys.LeftCtrl, 0))
	expectCaptured(t, "hotkeys in remote mode", c.Events(),
		Event{Kind: EvKey, Code: keys.LeftCtrl, State: keys.Down},
		Event{Kind: EvKey, Code: keys.LeftAlt, State: keys.Down},
		Event{Kind: EvHotkey, Code: 0},
		Event{Kind: EvHotkey, Code: 1},
		Event{Kind: EvKey, Code: keys.LeftAlt, State: keys.Up},
		Event{Kind: EvKey, Code: keys.LeftCtrl, State: keys.Up},
	)
	expectEvs(t, "hotkeys in remote mode leave the local desktop alone", outKbd.collect(150*time.Millisecond), nil)

	// The shift that was pressed locally is released locally, not remotely.
	physKbd.write(key(keys.LeftShift, 0))
	expectEvs(t, "local release of a key held across the switch", outKbd.collect(150*time.Millisecond), []ev{{evKey, keys.LeftShift, 0}})
	select {
	case e := <-c.Events():
		t.Fatalf("the release of a locally held key was forwarded: %+v", e)
	default:
	}

	// --- back to local: the cursor appears where it re-enters --------------------
	c.Release(layout.Right, 0.5, true)
	expectEvs(t, "re-entry position", outPtr.collect(150*time.Millisecond), []ev{{evAbs, absX, 997}, {evAbs, absY, 250}})
	physKbd.write(key(keys.Enter, 1))
	physKbd.write(key(keys.Enter, 0))
	expectEvs(t, "local typing after returning", outKbd.collect(150*time.Millisecond), []ev{{evKey, keys.Enter, 1}, {evKey, keys.Enter, 0}})
}
