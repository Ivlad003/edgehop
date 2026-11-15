//go:build windows

package wincap

import (
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Ivlad003/edgehop/internal/input"
	"github.com/Ivlad003/edgehop/internal/keys"
)

// A window of our own to take pictures of: a plain rectangle in one colour.

var (
	tRegisterClassExW    = user32.NewProc("RegisterClassExW")
	tCreateWindowExW     = user32.NewProc("CreateWindowExW")
	tDefWindowProcW      = user32.NewProc("DefWindowProcW")
	tGetMessageW         = user32.NewProc("GetMessageW")
	tTranslateMessage    = user32.NewProc("TranslateMessage")
	tDispatchMessageW    = user32.NewProc("DispatchMessageW")
	tPostMessageW        = user32.NewProc("PostMessageW")
	tPostQuitMessage     = user32.NewProc("PostQuitMessage")
	tBeginPaint          = user32.NewProc("BeginPaint")
	tEndPaint            = user32.NewProc("EndPaint")
	tGetClientRect       = user32.NewProc("GetClientRect")
	tFillRect            = user32.NewProc("FillRect")
	tShowWindow          = user32.NewProc("ShowWindow")
	tClientToScreen      = user32.NewProc("ClientToScreen")
	tCreateSolidBrush    = gdi32.NewProc("CreateSolidBrush")
	tGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	testColor            = [3]byte{200, 40, 90} // red, green, blue
	testClicks           = make(chan [2]int, 16)
	testKeys             = make(chan uint16, 64)
	testBrush            uintptr
	testProc             = syscall.NewCallback(testWndProc)
	testClassCreated     bool
	testClassName, _     = syscall.UTF16PtrFromString("edgehopWincapTest")
	testWindowTitle      = "edgehop wincap test"
	testWindowTitle16, _ = syscall.UTF16PtrFromString(testWindowTitle)
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type paintStruct struct {
	hdc       uintptr
	erase     int32
	paint     rect
	restore   int32
	incUpdate int32
	reserved  [32]byte
}

type testMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      [2]int32
}

func fill(hwnd, hdc uintptr) {
	var rc rect
	tGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	tFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), testBrush)
}

func testWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case 0x000F: // WM_PAINT
		var ps paintStruct
		hdc, _, _ := tBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		fill(hwnd, hdc)
		tEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case 0x0318: // WM_PRINTCLIENT
		fill(hwnd, wParam)
		return 0
	case 0x0014: // WM_ERASEBKGND
		return 1
	case 0x0201: // WM_LBUTTONDOWN: report where, in screen coordinates
		pt := [2]int32{int32(int16(lParam)), int32(int16(lParam >> 16))}
		tClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&pt)))
		select {
		case testClicks <- [2]int{int(pt[0]), int(pt[1])}:
		default:
		}
		return 0
	case 0x0100: // WM_KEYDOWN
		select {
		case testKeys <- uint16(wParam):
		default:
		}
		return 0
	case 0x0002: // WM_DESTROY
		tPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := tDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}

// openTestWindow shows a window at x,y and returns its handle; it is closed
// when the test ends.
func openTestWindow(t *testing.T, x, y, w, h int) uintptr {
	t.Helper()
	ready := make(chan uintptr, 1)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread() // a window belongs to the thread that made it
		defer runtime.UnlockOSThread()
		defer close(done)
		inst, _, _ := tGetModuleHandleW.Call(0)
		if !testClassCreated {
			testBrush, _, _ = tCreateSolidBrush.Call(uintptr(testColor[0]) | uintptr(testColor[1])<<8 | uintptr(testColor[2])<<16)
			wc := wndClassEx{wndProc: testProc, instance: inst, className: testClassName}
			wc.size = uint32(unsafe.Sizeof(wc))
			if r, _, err := tRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
				t.Errorf("RegisterClassEx: %v", err)
				ready <- 0
				return
			}
			testClassCreated = true
		}
		const overlapped = 0x00CF0000
		ex, show := uintptr(0x08000000), uintptr(4) // WS_EX_NOACTIVATE, SW_SHOWNOACTIVATE
		if live() {
			ex, show = 0, 5 // an ordinary window, shown and activated
		}
		hwnd, _, err := tCreateWindowExW.Call(ex, uintptr(unsafe.Pointer(testClassName)), uintptr(unsafe.Pointer(testWindowTitle16)),
			overlapped, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, inst, 0)
		if hwnd == 0 {
			t.Errorf("CreateWindowEx: %v", err)
		}
		if hwnd != 0 {
			tShowWindow.Call(hwnd, show)
		}
		ready <- hwnd
		if hwnd == 0 {
			return
		}
		var m testMsg
		for {
			r, _, _ := tGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			tTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			tDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	hwnd := <-ready
	if hwnd == 0 {
		t.FailNow()
	}
	t.Cleanup(func() {
		tPostMessageW.Call(hwnd, 0x0010, 0, 0) // WM_CLOSE
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})
	return hwnd
}

func pixel(f *Frame, x, y int) [3]byte {
	i := (y*f.W + x) * 4
	return [3]byte{f.Pix[i], f.Pix[i+1], f.Pix[i+2]}
}

func live() bool { return os.Getenv("EDGEHOP_LIVE") == "1" }

// The window is listed, and its picture shows what the window draws.
func TestWindowIsListedAndCaptured(t *testing.T) {
	showOwn = true
	defer func() { showOwn = false }()

	// Away from the visible desktop unless this is a live run: the test must
	// not flash a window in front of whoever is using the computer.
	x, y := 120, 120
	if !live() {
		vx, _, _ := pGetSystemMetrics.Call(76)
		vw, _, _ := pGetSystemMetrics.Call(78)
		x = int(int32(vx)) + int(int32(vw)) + 200
	}
	hwnd := openTestWindow(t, x, y, 420, 320)

	var sys System
	var found *Window
	deadline := time.Now().Add(3 * time.Second)
	for found == nil && time.Now().Before(deadline) {
		wins, err := sys.List()
		if err != nil {
			t.Fatal(err)
		}
		for i := range wins {
			if wins[i].ID == uint64(hwnd) {
				found = &wins[i]
			}
		}
		if found == nil {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if found == nil {
		t.Fatal("the test window is not in the list")
	}
	if found.Title != testWindowTitle || found.W < 380 || found.W > 420 || found.H < 290 || found.H > 320 {
		t.Errorf("listed as %+v", *found)
	}

	c, err := sys.Open(found.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var f Frame
	ok := false
	for i := 0; i < 50 && !ok; i++ { // the first picture can come before the first paint
		if err := c.Capture(&f); err != nil {
			t.Fatal(err)
		}
		ok = pixel(&f, f.W/2, f.H-40) == testColor
		if !ok {
			time.Sleep(40 * time.Millisecond)
		}
	}
	if f.W != found.W || f.H != found.H || len(f.Pix) != f.W*f.H*4 {
		t.Errorf("picture %dx%d (%d bytes), listed %dx%d", f.W, f.H, len(f.Pix), found.W, found.H)
	}
	if !ok {
		t.Fatalf("the middle of the picture is %v, want the window's colour %v", pixel(&f, f.W/2, f.H-40), testColor)
	}
	if f.Title != testWindowTitle {
		t.Errorf("title = %q", f.Title)
	}
	ox, oy := origin()
	if f.X != x+((420-f.W)/2)-ox || f.Y != y-oy {
		t.Errorf("picture placed at %d,%d; the window is at %d,%d with a %d pixel border", f.X, f.Y, x-ox, y-oy, (420-f.W)/2)
	}

	// A window that is gone says so.
	tPostMessageW.Call(hwnd, 0x0010, 0, 0)
	gone := false
	for i := 0; i < 100 && !gone; i++ {
		gone = c.Capture(&f) == ErrGone
		time.Sleep(20 * time.Millisecond)
	}
	if !gone {
		t.Error("capturing a closed window must report ErrGone")
	}
}

// A click placed through the picture's coordinates lands in the window.
// Needs a real desktop and moves the real cursor: live runs only.
func TestLiveClickReachesTheWindow(t *testing.T) {
	if !live() {
		t.Skip("set EDGEHOP_LIVE=1 to run (moves the cursor and clicks)")
	}
	showOwn = true
	defer func() { showOwn = false }()
	hwnd := openTestWindow(t, 160, 140, 420, 320)
	var sys System
	c, err := sys.Open(uint64(hwnd))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var f Frame
	if err := c.Capture(&f); err != nil {
		t.Fatal(err)
	}
	inj, err := input.NewInjector(input.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer inj.Close()
	c.Raise()
	time.Sleep(300 * time.Millisecond)

	px, py := f.W/2, f.H-60 // a point inside the window, in picture pixels
	inj.MoveAbs(f.X+px, f.Y+py)
	time.Sleep(100 * time.Millisecond)
	inj.Button(keys.ButtonLeft, true)
	inj.Button(keys.ButtonLeft, false)
	ox, oy := origin()
	select {
	case got := <-testClicks:
		if got[0] != f.X+px+ox || got[1] != f.Y+py+oy {
			t.Errorf("the click arrived at %v, want %d,%d", got, f.X+px+ox, f.Y+py+oy)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the click did not reach the window")
	}
	// The window is in use now, so its picture is taken from the screen -
	// the way that also shows its menus and dialogs.
	if !c.Front() {
		t.Error("the window did not become the foreground window after the click")
	}
	if err := c.Capture(&f); err != nil {
		t.Fatal(err)
	}
	if got := pixel(&f, f.W/2, f.H-40); got != testColor {
		t.Errorf("picture from the screen: the window's colour is %v, want %v", got, testColor)
	}
	inj.Key(keys.A, keys.Down)
	inj.Key(keys.A, keys.Up)
	select {
	case vk := <-testKeys:
		if vk != 'A' {
			t.Errorf("key %#x arrived, want 'A'", vk)
		}
	case <-time.After(3 * time.Second):
		t.Error("the key press did not reach the window")
	}
}

// Asking for the dragged window is harmless when nothing is being dragged.
func TestDraggedWithoutADrag(t *testing.T) {
	var sys System
	if w, ok := sys.Dragged(); ok && live() {
		t.Errorf("a dragged window was reported although nothing is dragged: %+v", w)
	}
}

// While the user drags a window by its title bar, Dragged names it; and a
// window that hangs over the edge of the screen is put back when raised.
// Moves the real cursor: live runs only.
func TestLiveDraggedWindowIsRecognised(t *testing.T) {
	if !live() {
		t.Skip("set EDGEHOP_LIVE=1 to run (moves the cursor and drags a window)")
	}
	showOwn = true
	defer func() { showOwn = false }()
	hwnd := openTestWindow(t, 200, 180, 420, 320)
	var sys System
	c, err := sys.Open(uint64(hwnd))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var f Frame
	if err := c.Capture(&f); err != nil {
		t.Fatal(err)
	}
	inj, err := input.NewInjector(input.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer inj.Close()
	c.Raise()
	time.Sleep(300 * time.Millisecond)

	// Press on the title bar and move: Windows starts moving the window.
	x, y := f.X+f.W/2, f.Y+12
	inj.MoveAbs(x, y)
	time.Sleep(100 * time.Millisecond)
	inj.Button(keys.ButtonLeft, true)
	found := false
	for i := 1; i <= 60 && !found; i++ {
		inj.MoveAbs(x+i*4, y+i*2)
		time.Sleep(30 * time.Millisecond)
		if w, ok := sys.Dragged(); ok {
			found = true
			if w.ID != uint64(hwnd) || w.Title != testWindowTitle || w.W != f.W || w.H != f.H {
				t.Errorf("dragged window reported as %+v, want the test window %dx%d", w, f.W, f.H)
			}
		}
	}
	inj.Button(keys.ButtonLeft, false)
	if !found {
		t.Fatal("the window being dragged was not recognised")
	}
	time.Sleep(200 * time.Millisecond)
	if w, ok := sys.Dragged(); ok {
		t.Errorf("still reported as dragged after the button was released: %+v", w)
	}
	var moved Frame
	if err := c.Capture(&moved); err != nil {
		t.Fatal(err)
	}
	if moved.X == f.X && moved.Y == f.Y {
		t.Error("the drag did not move the window")
	}

	// Push it half off the right edge, then raise it: it comes back whole.
	vx, _, _ := pGetSystemMetrics.Call(76)
	vw, _, _ := pGetSystemMetrics.Call(78)
	right := int(int32(vx)) + int(int32(vw))
	pSetWindowPos.Call(hwnd, 0, uintptr(right-150), 200, 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
	time.Sleep(100 * time.Millisecond)
	c.Raise()
	time.Sleep(200 * time.Millisecond)
	whole, _ := windowRect(hwnd)
	if vis := visibleRect(hwnd, whole); int(vis.right) > right {
		t.Errorf("after raising, the window still ends at x=%d, beyond the screen edge %d", vis.right, right)
	}
}
