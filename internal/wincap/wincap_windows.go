//go:build windows

package wincap

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/Ivlad003/edgehop/internal/input"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pEnumWindows              = user32.NewProc("EnumWindows")
	pIsWindow                 = user32.NewProc("IsWindow")
	pIsWindowVisible          = user32.NewProc("IsWindowVisible")
	pIsIconic                 = user32.NewProc("IsIconic")
	pIsHungAppWindow          = user32.NewProc("IsHungAppWindow")
	pGetWindowTextW           = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	pGetWindow                = user32.NewProc("GetWindow")
	pGetAncestor              = user32.NewProc("GetAncestor")
	pGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	pGetWindowRect            = user32.NewProc("GetWindowRect")
	pGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	pGetShellWindow           = user32.NewProc("GetShellWindow")
	pGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pShowWindow               = user32.NewProc("ShowWindow")
	pSetWindowPos             = user32.NewProc("SetWindowPos")
	pGetDC                    = user32.NewProc("GetDC")
	pReleaseDC                = user32.NewProc("ReleaseDC")
	pPrintWindow              = user32.NewProc("PrintWindow")
	pGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	pGetGUIThreadInfo         = user32.NewProc("GetGUIThreadInfo")
	pIsZoomed                 = user32.NewProc("IsZoomed")
	pMonitorFromWindow        = user32.NewProc("MonitorFromWindow")
	pGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pBitBlt             = gdi32.NewProc("BitBlt")
	pGdiFlush           = gdi32.NewProc("GdiFlush")

	pDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")

	pOpenProcess                = kernel32.NewProc("OpenProcess")
	pQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	pCloseHandle                = kernel32.NewProc("CloseHandle")
	pGetCurrentProcessId        = kernel32.NewProc("GetCurrentProcessId")
)

const (
	gwOwner         = 4
	gaRootOwner     = 3
	wsExToolWindow  = 0x00000080
	wsExAppWindow   = 0x00040000
	wsExTopmost     = 0x00000008
	dwmExtFrame     = 9  // DWMWA_EXTENDED_FRAME_BOUNDS: the window without its invisible resize borders
	dwmCloaked      = 14 // DWMWA_CLOAKED: on another virtual desktop, or a suspended store app
	pwRenderFull    = 2  // PW_RENDERFULLCONTENT: also what the window draws with the graphics card
	srcCopy         = 0x00CC0020
	captureBlt      = 0x40000000
	swRestore       = 9
	swShowNoAct     = 4
	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoActivate   = 0x0010
	swpNoZOrder     = 0x0004
	guiInMoveSize   = 0x0002 // GUITHREADINFO: the thread is moving or sizing a window
	procQueryLtd    = 0x1000
	maxTitle        = 200
	minListedSize   = 40
	maxWindowPixels = 8192
)

var gwlExStyle = ^uintptr(19) // GWL_EXSTYLE, -20

type rect struct{ left, top, right, bottom int32 }

type bitmapInfoHeader struct {
	size          uint32
	width, height int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPPM, yPPM    int32
	clrUsed       uint32
	clrImportant  uint32
}

// Supported reports whether windows can be listed and captured here.
func Supported() error { return nil }

// showOwn lets the tests capture a window of their own process.
var showOwn bool

var (
	enumMu  sync.Mutex
	enumOut []uintptr
	enumCB  = syscall.NewCallback(func(hwnd, data uintptr) uintptr {
		enumOut = append(enumOut, hwnd)
		return 1
	})
)

func topLevelWindows() []uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumOut = nil
	pEnumWindows.Call(enumCB, 0)
	out := enumOut
	enumOut = nil
	return out
}

func call(p *syscall.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

func title(hwnd uintptr) string {
	n := call(pGetWindowTextLengthW, hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	got := call(pGetWindowTextW, hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	s := syscall.UTF16ToString(buf[:got])
	if r := []rune(s); len(r) > maxTitle {
		s = string(r[:maxTitle])
	}
	return s
}

func processID(hwnd uintptr) uint32 {
	var pid uint32
	call(pGetWindowThreadProcessId, hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}

// appName returns the program a process runs, without folder and ".exe".
func appName(pid uint32) string {
	h := call(pOpenProcess, procQueryLtd, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer pCloseHandle.Call(h)
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	if call(pQueryFullProcessImageNameW, h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))) == 0 {
		return ""
	}
	name := filepath.Base(syscall.UTF16ToString(buf[:size]))
	if strings.EqualFold(filepath.Ext(name), ".exe") {
		name = name[:len(name)-4]
	}
	return name
}

func windowRect(hwnd uintptr) (rect, bool) {
	var r rect
	ok := call(pGetWindowRect, hwnd, uintptr(unsafe.Pointer(&r))) != 0
	return r, ok
}

// visibleRect is the window as the user sees it. Since Windows 10 a window
// is a few pixels larger than it looks: invisible borders to grab for
// resizing.
func visibleRect(hwnd uintptr, whole rect) rect {
	var r rect
	if pDwmGetWindowAttribute.Find() != nil {
		return whole
	}
	hr := call(pDwmGetWindowAttribute, hwnd, dwmExtFrame, uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r))
	if hr != 0 || r.right <= r.left || r.bottom <= r.top {
		return whole
	}
	if r.left < whole.left {
		r.left = whole.left
	}
	if r.top < whole.top {
		r.top = whole.top
	}
	if r.right > whole.right {
		r.right = whole.right
	}
	if r.bottom > whole.bottom {
		r.bottom = whole.bottom
	}
	return r
}

func cloaked(hwnd uintptr) bool {
	if pDwmGetWindowAttribute.Find() != nil {
		return false
	}
	var v uint32
	hr := call(pDwmGetWindowAttribute, hwnd, dwmCloaked, uintptr(unsafe.Pointer(&v)), 4)
	return hr == 0 && v != 0
}

// List returns the windows that can be shown remotely: what the taskbar
// would show, more or less.
func (System) List() ([]Window, error) {
	self := uint32(call(pGetCurrentProcessId))
	shell := call(pGetShellWindow)
	var out []Window
	for _, hwnd := range topLevelWindows() {
		if hwnd == shell || call(pIsWindowVisible, hwnd) == 0 || call(pGetWindow, hwnd, gwOwner) != 0 {
			continue
		}
		ex := call(pGetWindowLongPtrW, hwnd, gwlExStyle)
		if ex&wsExToolWindow != 0 && ex&wsExAppWindow == 0 {
			continue
		}
		if cloaked(hwnd) {
			continue
		}
		pid := processID(hwnd)
		if pid == self && !showOwn {
			continue
		}
		name := title(hwnd)
		if name == "" {
			continue
		}
		w := Window{ID: uint64(hwnd), Title: name, App: appName(pid)}
		if call(pIsIconic, hwnd) == 0 {
			whole, ok := windowRect(hwnd)
			if !ok {
				continue
			}
			r := visibleRect(hwnd, whole)
			w.W, w.H = int(r.right-r.left), int(r.bottom-r.top)
			if w.W < minListedSize || w.H < minListedSize {
				continue
			}
		}
		out = append(out, w)
	}
	return out, nil
}

// guiThreadInfo is GUITHREADINFO.
type guiThreadInfo struct {
	size      uint32
	flags     uint32
	active    uintptr
	focus     uintptr
	capture   uintptr
	menuOwner uintptr
	moveSize  uintptr
	caret     uintptr
	caretRect rect
}

// Dragged returns the window the user is moving with the mouse right now,
// if any: Windows knows which window is in its "move" mode.
func (System) Dragged() (Window, bool) {
	var gi guiThreadInfo
	gi.size = uint32(unsafe.Sizeof(gi))
	if call(pGetGUIThreadInfo, 0, uintptr(unsafe.Pointer(&gi))) == 0 { // 0: the thread the user works in
		return Window{}, false
	}
	hwnd := gi.moveSize
	if gi.flags&guiInMoveSize == 0 || hwnd == 0 || call(pIsWindowVisible, hwnd) == 0 {
		return Window{}, false
	}
	pid := processID(hwnd)
	if pid == uint32(call(pGetCurrentProcessId)) && !showOwn {
		return Window{}, false
	}
	whole, ok := windowRect(hwnd)
	if !ok {
		return Window{}, false
	}
	r := visibleRect(hwnd, whole)
	return Window{ID: uint64(hwnd), Title: title(hwnd), App: appName(pid), W: int(r.right - r.left), H: int(r.bottom - r.top)}, true
}

// monitorInfo is MONITORINFO.
type monitorInfo struct {
	size    uint32
	monitor rect
	work    rect
	flags   uint32
}

// fit moves a window that hangs over the edge of its monitor back onto it:
// only what is on the screen can be clicked.
func fit(hwnd uintptr) {
	if call(pIsZoomed, hwnd) != 0 || call(pIsIconic, hwnd) != 0 {
		return
	}
	whole, ok := windowRect(hwnd)
	if !ok {
		return
	}
	vis := visibleRect(hwnd, whole)
	mi := monitorInfo{size: uint32(unsafe.Sizeof(monitorInfo{}))}
	mon := call(pMonitorFromWindow, hwnd, 2) // MONITOR_DEFAULTTONEAREST
	if mon == 0 || call(pGetMonitorInfoW, mon, uintptr(unsafe.Pointer(&mi))) == 0 {
		return
	}
	var dx, dy int32
	if vis.right > mi.work.right {
		dx = mi.work.right - vis.right
	}
	if vis.left+dx < mi.work.left {
		dx = mi.work.left - vis.left
	}
	if vis.bottom > mi.work.bottom {
		dy = mi.work.bottom - vis.bottom
	}
	if vis.top+dy < mi.work.top {
		dy = mi.work.top - vis.top
	}
	if dx == 0 && dy == 0 {
		return
	}
	pSetWindowPos.Call(hwnd, 0, uintptr(whole.left+dx), uintptr(whole.top+dy), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
}

type winCapturer struct {
	hwnd   uintptr
	dc     uintptr // memory device context the window is printed into
	bmp    uintptr
	oldBmp uintptr
	bits   unsafe.Pointer // BGRA, top-down
	w, h   int
}

// Open prepares capturing the window with the given id.
func (System) Open(id uint64) (Capturer, error) {
	hwnd := uintptr(id)
	if call(pIsWindow, hwnd) == 0 {
		return nil, ErrGone
	}
	if processID(hwnd) == uint32(call(pGetCurrentProcessId)) && !showOwn {
		return nil, errors.New("edgehop does not show its own windows")
	}
	dc := call(pCreateCompatibleDC, 0)
	if dc == 0 {
		return nil, errors.New("cannot create a drawing context")
	}
	return &winCapturer{hwnd: hwnd, dc: dc}, nil
}

func (c *winCapturer) freeBitmap() {
	if c.bmp != 0 {
		pSelectObject.Call(c.dc, c.oldBmp)
		pDeleteObject.Call(c.bmp)
		c.bmp, c.bits = 0, nil
	}
}

func (c *winCapturer) Close() {
	c.freeBitmap()
	if c.dc != 0 {
		pDeleteDC.Call(c.dc)
		c.dc = 0
	}
}

func (c *winCapturer) resize(w, h int) error {
	if c.bmp != 0 && c.w == w && c.h == h {
		return nil
	}
	c.freeBitmap()
	hdr := bitmapInfoHeader{
		size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), width: int32(w), height: int32(-h), // negative: rows from the top
		planes: 1, bitCount: 32,
	}
	var bits unsafe.Pointer
	bmp := call(pCreateDIBSection, c.dc, uintptr(unsafe.Pointer(&hdr)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return fmt.Errorf("cannot create a %dx%d bitmap", w, h)
	}
	c.oldBmp = call(pSelectObject, c.dc, bmp)
	c.bmp, c.bits, c.w, c.h = bmp, bits, w, h
	return nil
}

// origin is the top-left corner of the whole desktop: input.Injector counts
// from there.
func origin() (int, int) {
	mons := input.Monitors() // also makes the process see real pixels on scaled displays
	if len(mons) == 0 {
		return 0, 0
	}
	x, y := mons[0].X, mons[0].Y
	for _, m := range mons[1:] {
		if m.X < x {
			x = m.X
		}
		if m.Y < y {
			y = m.Y
		}
	}
	return x, y
}

// onDesktop reports whether r lies completely on the desktop.
func onDesktop(r rect) bool {
	metric := func(i uintptr) int32 { return int32(call(pGetSystemMetrics, i)) }
	x, y := metric(76), metric(77) // SM_XVIRTUALSCREEN, SM_YVIRTUALSCREEN
	return r.left >= x && r.top >= y && r.right <= x+metric(78) && r.bottom <= y+metric(79)
}

func (c *winCapturer) Capture(f *Frame) error {
	if call(pIsWindow, c.hwnd) == 0 {
		return ErrGone
	}
	ox, oy := origin()
	if call(pIsIconic, c.hwnd) != 0 {
		// A minimised window has no picture; put it back without taking the
		// keyboard away from whatever is in use on that computer.
		pShowWindow.Call(c.hwnd, swShowNoAct)
	}
	whole, ok := windowRect(c.hwnd)
	if !ok {
		return ErrGone
	}
	w, h := int(whole.right-whole.left), int(whole.bottom-whole.top)
	if w <= 0 || h <= 0 || w > maxWindowPixels || h > maxWindowPixels {
		return fmt.Errorf("the window has an unusable size (%dx%d)", w, h)
	}
	if err := c.resize(w, h); err != nil {
		return err
	}
	// A window in use is taken from the screen: its menus, drop-down lists
	// and dialogs are windows of their own and would be missing from a
	// picture of the window alone. A window in the background is asked to
	// draw itself, so it shows even while other windows cover it.
	// (Only a window that is completely on the screen can be taken from it.)
	vis := visibleRect(c.hwnd, whole)
	fromScreen := c.Front() && onDesktop(vis)
	printed := false
	if !fromScreen && call(pIsHungAppWindow, c.hwnd) == 0 { // a frozen program never answers
		printed = call(pPrintWindow, c.hwnd, c.dc, pwRenderFull) != 0
	}
	if !printed {
		screen := call(pGetDC, 0)
		call(pBitBlt, c.dc, 0, 0, uintptr(w), uintptr(h), screen, uintptr(whole.left), uintptr(whole.top), srcCopy|captureBlt)
		pReleaseDC.Call(0, screen)
	}
	pGdiFlush.Call()

	cx, cy := int(vis.left-whole.left), int(vis.top-whole.top)
	cw, ch := int(vis.right-vis.left), int(vis.bottom-vis.top)
	src := unsafe.Slice((*byte)(c.bits), w*h*4)
	f.Pix = grow(f.Pix, cw*ch*4)
	f.W, f.H = cw, ch
	f.X, f.Y = int(vis.left)-ox, int(vis.top)-oy
	f.Title = title(c.hwnd)
	for y := 0; y < ch; y++ {
		s := src[((cy+y)*w+cx)*4 : ((cy+y)*w+cx+cw)*4]
		d := f.Pix[y*cw*4 : (y+1)*cw*4]
		for i := 0; i+3 < len(s); i += 4 { // BGRA -> RGBA; the alpha Windows leaves behind means nothing
			d[i], d[i+1], d[i+2], d[i+3] = s[i+2], s[i+1], s[i], 0xFF
		}
	}
	return nil
}

// Front reports whether the user works in this window or in something that
// belongs to it, such as one of its dialogs.
func (c *winCapturer) Front() bool {
	fg := call(pGetForegroundWindow)
	return fg != 0 && (fg == c.hwnd || call(pGetAncestor, fg, gaRootOwner) == c.hwnd)
}

func (c *winCapturer) Raise() {
	if call(pIsIconic, c.hwnd) != 0 {
		pShowWindow.Call(c.hwnd, swRestore)
	}
	fit(c.hwnd)
	// Windows rarely lets a background program take the keyboard focus, but
	// it does let it reorder windows: lift this one above the others, and
	// the click that follows activates it like any click would.
	if call(pGetWindowLongPtrW, c.hwnd, gwlExStyle)&wsExTopmost == 0 {
		const flags = swpNoMove | swpNoSize | swpNoActivate
		pSetWindowPos.Call(c.hwnd, ^uintptr(0), 0, 0, 0, 0, flags) // HWND_TOPMOST
		pSetWindowPos.Call(c.hwnd, ^uintptr(1), 0, 0, 0, 0, flags) // HWND_NOTOPMOST
	}
	pSetForegroundWindow.Call(c.hwnd)
}
