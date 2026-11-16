//go:build windows

package input

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
)

// Windows backend.
//
// Capture uses low-level mouse and keyboard hooks. In remote mode the hooks
// swallow every event; the cursor is parked in the middle of the primary
// monitor and each mouse move is reported as the distance from the parked
// cursor to where Windows wanted to put it. That distance already includes
// the user's pointer speed and acceleration settings, so the remote cursor
// feels exactly like the local one.
//
// Injection uses SendInput with absolute coordinates over the virtual desktop
// and hardware scan codes for keys.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pSetWindowsHookEx           = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx        = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx             = user32.NewProc("CallNextHookEx")
	pGetMessage                 = user32.NewProc("GetMessageW")
	pPeekMessage                = user32.NewProc("PeekMessageW")
	pTranslateMessage           = user32.NewProc("TranslateMessage")
	pDispatchMessage            = user32.NewProc("DispatchMessageW")
	pPostThreadMessage          = user32.NewProc("PostThreadMessageW")
	pGetCursorPos               = user32.NewProc("GetCursorPos")
	pSetCursorPos               = user32.NewProc("SetCursorPos")
	pGetCursorInfo              = user32.NewProc("GetCursorInfo")
	pGetSystemMetrics           = user32.NewProc("GetSystemMetrics")
	pEnumDisplayMonitors        = user32.NewProc("EnumDisplayMonitors")
	pSendInput                  = user32.NewProc("SendInput")
	pMapVirtualKey              = user32.NewProc("MapVirtualKeyW")
	pRegisterClassEx            = user32.NewProc("RegisterClassExW")
	pCreateWindowEx             = user32.NewProc("CreateWindowExW")
	pDefWindowProc              = user32.NewProc("DefWindowProcW")
	pDestroyWindow              = user32.NewProc("DestroyWindow")
	pSetWindowPos               = user32.NewProc("SetWindowPos")
	pShowWindow                 = user32.NewProc("ShowWindow")
	pSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	pSetCursor                  = user32.NewProc("SetCursor")
	pSetTimer                   = user32.NewProc("SetTimer")
	pSetProcessDpiAwarenessCtx  = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDPIAware         = user32.NewProc("SetProcessDPIAware")
	pGetModuleHandle            = kernel32.NewProc("GetModuleHandleW")
	pGetCurrentThreadID         = kernel32.NewProc("GetCurrentThreadId")
	pGetCurrentProcess          = kernel32.NewProc("GetCurrentProcess")
	pSetPriorityClass           = kernel32.NewProc("SetPriorityClass")
)

const (
	whKeyboardLL = 13
	whMouseLL    = 14

	wmSetCursor   = 0x0020
	wmTimer       = 0x0113
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmMButtonDown = 0x0207
	wmMButtonUp   = 0x0208
	wmMouseWheel  = 0x020A
	wmXButtonDown = 0x020B
	wmXButtonUp   = 0x020C
	wmMouseHWheel = 0x020E
	wmApp         = 0x8000
	wmGrab        = wmApp + 1
	wmRelease     = wmApp + 2
	wmStop        = wmApp + 3

	llkhfExtended = 0x01
	llkhfUp       = 0x80

	smCXScreen        = 0
	smCYScreen        = 1
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfMove        = 0x0001
	mouseeventfLeftDown    = 0x0002
	mouseeventfLeftUp      = 0x0004
	mouseeventfRightDown   = 0x0008
	mouseeventfRightUp     = 0x0010
	mouseeventfMiddleDown  = 0x0020
	mouseeventfMiddleUp    = 0x0040
	mouseeventfXDown       = 0x0080
	mouseeventfXUp         = 0x0100
	mouseeventfWheel       = 0x0800
	mouseeventfHWheel      = 0x1000
	mouseeventfVirtualDesk = 0x4000
	mouseeventfAbsolute    = 0x8000

	keyeventfExtended = 0x0001
	keyeventfKeyUp    = 0x0002
	keyeventfScanCode = 0x0008

	wsPopup         = 0x80000000
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsExLayered     = 0x00080000
	wsExNoActivate  = 0x08000000
	swpNoActivate   = 0x0010
	swpShowWindow   = 0x0040
	lwaAlpha        = 0x2
	shieldSize      = 32
	aboveNormalPrio = 0x8000
)

type winPoint struct{ x, y int32 }
type winRect struct{ left, top, right, bottom int32 }

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      winPoint
	private uint32
}

type msllHook struct {
	pt        winPoint
	mouseData uint32
	flags     uint32
	time      uint32
	extra     uintptr
}

type kbdllHook struct {
	vk, scan, flags, time uint32
	extra                 uintptr
}

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

type cursorInfo struct {
	size    uint32
	flags   uint32
	hCursor uintptr
	pos     winPoint
}

type mouseInput struct {
	typ uint32
	mi  struct {
		dx, dy    int32
		mouseData uint32
		flags     uint32
		time      uint32
		extra     uintptr
	}
}

type keybdInput struct {
	typ uint32
	ki  struct {
		vk, scan uint16
		flags    uint32
		time     uint32
		extra    uintptr
	}
	_ [8]byte // INPUT is a union; pad to the size of its largest member
}

var processOnce sync.Once

// initProcess makes coordinates mean physical pixels on every monitor and
// raises the priority a little so input stays smooth when the machine is busy.
func initProcess() {
	processOnce.Do(func() {
		ok := false
		if pSetProcessDpiAwarenessCtx.Find() == nil {
			r, _, _ := pSetProcessDpiAwarenessCtx.Call(^uintptr(3)) // per-monitor aware v2
			ok = r != 0
		}
		if !ok {
			pSetProcessDPIAware.Call()
		}
		h, _, _ := pGetCurrentProcess.Call()
		pSetPriorityClass.Call(h, aboveNormalPrio)
	})
}

func metric(index uintptr) int {
	r, _, _ := pGetSystemMetrics.Call(index)
	return int(int32(r))
}

func virtualScreen() layout.Rect {
	return layout.Rect{
		X: metric(smXVirtualScreen), Y: metric(smYVirtualScreen),
		W: metric(smCXVirtualScreen), H: metric(smCYVirtualScreen),
	}
}

var (
	enumMu  sync.Mutex
	enumOut []layout.Rect
	enumCB  = syscall.NewCallback(func(hmon, hdc, rect, data uintptr) uintptr {
		r := (*winRect)(ptr(rect))
		enumOut = append(enumOut, layout.Rect{
			X: int(r.left), Y: int(r.top), W: int(r.right - r.left), H: int(r.bottom - r.top),
		})
		return 1
	})
)

// Monitors returns the monitors of this computer in desktop coordinates.
func Monitors() []layout.Rect {
	initProcess()
	enumMu.Lock()
	defer enumMu.Unlock()
	enumOut = nil
	pEnumDisplayMonitors.Call(0, 0, enumCB, 0)
	out := enumOut
	enumOut = nil
	if len(out) == 0 {
		out = []layout.Rect{virtualScreen()}
	}
	return out
}

func setCursorPos(x, y int) {
	pSetCursorPos.Call(uintptr(int32(x)), uintptr(int32(y)))
}

func cursorPos() winPoint {
	var p winPoint
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p
}

// ---------------------------------------------------------------------------
// Capture

type winCapture struct {
	events  chan Event
	hide    bool
	tid     uint32
	armed   atomic.Uint32
	stopped chan struct{}

	// The fields below belong to the hook thread.
	remote       bool
	desk         layout.Desktop
	park         winPoint
	buttons      uint32 // physically held
	localButtons uint32 // pressed while in local mode
	kt           keyTracker
	hkKey        uintptr
	hkMouse      uintptr
	shield       uintptr
}

// Hook procedures get no user pointer, so the active capture is global.
var (
	activeMu      sync.Mutex
	activeCapture *winCapture

	mouseCB = syscall.NewCallback(func(code, wparam, lparam uintptr) uintptr {
		if c := activeCapture; c != nil && int32(code) >= 0 {
			if c.mouse(uint32(wparam), (*msllHook)(ptr(lparam))) {
				return 1
			}
		}
		r, _, _ := pCallNextHookEx.Call(0, code, wparam, lparam)
		return r
	})
	keyCB = syscall.NewCallback(func(code, wparam, lparam uintptr) uintptr {
		if c := activeCapture; c != nil && int32(code) >= 0 {
			if c.key((*kbdllHook)(ptr(lparam))) {
				return 1
			}
		}
		r, _, _ := pCallNextHookEx.Call(0, code, wparam, lparam)
		return r
	})
	shieldCB = syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
		if msg == wmSetCursor {
			pSetCursor.Call(0) // no cursor while the pointer rests on the shield
			return 1
		}
		r, _, _ := pDefWindowProc.Call(hwnd, msg, wparam, lparam)
		return r
	})
)

// NewCapture installs the hooks.
func NewCapture(opts Options) (Capture, error) {
	initProcess()
	activeMu.Lock()
	defer activeMu.Unlock()
	if activeCapture != nil {
		return nil, errors.New("input capture is already running in this process")
	}
	c := &winCapture{
		events:  make(chan Event, 4096),
		hide:    opts.HideCursor,
		stopped: make(chan struct{}),
	}
	ready := make(chan error, 1)
	go c.loop(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return c, nil
}

func (c *winCapture) loop(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(c.stopped)

	tid, _, _ := pGetCurrentThreadID.Call()
	c.tid = uint32(tid)
	var m winMsg
	pPeekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 0) // creates the message queue
	c.desk = layout.NewDesktop(Monitors())
	hmod, _, _ := pGetModuleHandle.Call(0)

	activeCapture = c
	defer func() { activeCapture = nil }()

	var err error
	c.hkKey, _, err = pSetWindowsHookEx.Call(whKeyboardLL, keyCB, hmod, 0)
	if c.hkKey == 0 {
		ready <- fmt.Errorf("cannot install the keyboard hook: %v", err)
		return
	}
	defer pUnhookWindowsHookEx.Call(c.hkKey)
	c.hkMouse, _, err = pSetWindowsHookEx.Call(whMouseLL, mouseCB, hmod, 0)
	if c.hkMouse == 0 {
		ready <- fmt.Errorf("cannot install the mouse hook: %v", err)
		return
	}
	defer pUnhookWindowsHookEx.Call(c.hkMouse)
	if c.hide {
		c.createShield(hmod)
		if c.shield != 0 {
			defer pDestroyWindow.Call(c.shield)
		}
	}
	pSetTimer.Call(0, 0, 3000, 0) // refresh the monitor list now and then
	ready <- nil

	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		switch {
		case m.hwnd == 0 && m.message == wmGrab:
			c.doGrab()
		case m.hwnd == 0 && m.message == wmRelease:
			c.doRelease(layout.Side(m.wParam&0xFF), float64(m.lParam)/1e6, m.wParam&0x100 != 0)
		case m.hwnd == 0 && m.message == wmStop:
			if c.remote {
				c.doRelease(0, 0, false)
			}
			return
		case m.hwnd == 0 && m.message == wmTimer:
			if !c.remote {
				c.desk = layout.NewDesktop(Monitors())
			}
		default:
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
	}
}

// createShield makes a tiny, practically invisible window. In remote mode it
// sits under the parked cursor and tells Windows to draw no cursor there.
func (c *winCapture) createShield(hmod uintptr) {
	name, _ := syscall.UTF16PtrFromString("edgehopCursorShield")
	wc := wndClassEx{wndProc: shieldCB, instance: hmod, className: name}
	wc.size = uint32(unsafe.Sizeof(wc))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))) // fails harmlessly if already registered
	hwnd, _, _ := pCreateWindowEx.Call(
		wsExTopmost|wsExToolWindow|wsExLayered|wsExNoActivate,
		uintptr(unsafe.Pointer(name)), 0, wsPopup,
		0, 0, shieldSize, shieldSize, 0, 0, hmod, 0)
	if hwnd == 0 {
		return
	}
	pSetLayeredWindowAttributes.Call(hwnd, 0, 1, lwaAlpha)
	c.shield = hwnd
}

func (c *winCapture) doGrab() {
	if c.remote {
		return
	}
	c.desk = layout.NewDesktop(Monitors())
	c.park = winPoint{int32(metric(smCXScreen) / 2), int32(metric(smCYScreen) / 2)}
	if c.shield != 0 {
		pSetWindowPos.Call(c.shield, ^uintptr(0), // HWND_TOPMOST
			uintptr(c.park.x-shieldSize/2), uintptr(c.park.y-shieldSize/2), shieldSize, shieldSize,
			swpNoActivate|swpShowWindow)
	}
	setCursorPos(int(c.park.x), int(c.park.y))
	c.remote = true
}

func (c *winCapture) doRelease(side layout.Side, frac float64, warp bool) {
	c.remote = false
	c.kt.clearRemote()
	if c.shield != 0 {
		pShowWindow.Call(c.shield, 0)
	}
	c.desk = layout.NewDesktop(Monitors())
	if warp {
		x, y := c.desk.Entry(side, frac, 2)
		setCursorPos(x, y)
	}
}

// mouse handles one low-level mouse event; it returns true to swallow it.
func (c *winCapture) mouse(msg uint32, ms *msllHook) bool {
	switch msg {
	case wmMouseMove:
		if !c.remote {
			if c.buttons == 0 {
				if armed := uint8(c.armed.Load()); armed != 0 {
					x, y := int(ms.pt.x), int(ms.pt.y)
					if side, ok := layout.FirstSide(c.desk.Edges(x, y) & armed); ok {
						emit(c.events, Event{Kind: EvEdge, Side: side, Frac: c.desk.Frac(side, x, y)})
					}
				}
			}
			return false
		}
		// ms.pt is where Windows wants to move the cursor; because the event
		// is swallowed the cursor stays put, so the difference is the motion.
		cur := cursorPos()
		dx, dy := ms.pt.x-cur.x, ms.pt.y-cur.y
		if dx != 0 || dy != 0 {
			emit(c.events, Event{Kind: EvMotion, DX: float64(dx), DY: float64(dy)})
		}
		if cur != c.park {
			setCursorPos(int(c.park.x), int(c.park.y))
		}
		return true
	case wmLButtonDown:
		return c.button(keys.ButtonLeft, true)
	case wmLButtonUp:
		return c.button(keys.ButtonLeft, false)
	case wmRButtonDown:
		return c.button(keys.ButtonRight, true)
	case wmRButtonUp:
		return c.button(keys.ButtonRight, false)
	case wmMButtonDown:
		return c.button(keys.ButtonMiddle, true)
	case wmMButtonUp:
		return c.button(keys.ButtonMiddle, false)
	case wmXButtonDown, wmXButtonUp:
		b := uint16(keys.ButtonBack)
		if ms.mouseData>>16 == 2 {
			b = keys.ButtonForward
		}
		return c.button(b, msg == wmXButtonDown)
	case wmMouseWheel:
		if c.remote {
			emit(c.events, Event{Kind: EvWheel, WY: int32(int16(ms.mouseData >> 16))})
		}
	case wmMouseHWheel:
		if c.remote {
			emit(c.events, Event{Kind: EvWheel, WX: int32(int16(ms.mouseData >> 16))})
		}
	}
	return c.remote
}

func (c *winCapture) button(b uint16, down bool) bool {
	bit := uint32(1) << b
	if down {
		c.buttons |= bit
	} else {
		c.buttons &^= bit
	}
	if !c.remote {
		if down {
			c.localButtons |= bit
		} else {
			c.localButtons &^= bit
		}
		return false
	}
	if !down && c.localButtons&bit != 0 {
		// Pressed on the local desktop just before the switch: it must also
		// be released there.
		c.localButtons &^= bit
		return false
	}
	st := uint8(0)
	if down {
		st = 1
	}
	emit(c.events, Event{Kind: EvButton, Code: b, State: st})
	return true
}

// key handles one low-level keyboard event; it returns true to swallow it.
func (c *winCapture) key(kb *kbdllHook) bool {
	if kb.scan == 0x21D {
		// The fake left Ctrl that Windows inserts in front of AltGr.
		return c.remote
	}
	scan := kb.scan
	if scan == 0 {
		r, _, _ := pMapVirtualKey.Call(uintptr(kb.vk), 0)
		scan = uint32(r)
	}
	code := keys.FromWindows(kb.vk, scan, kb.flags&llkhfExtended != 0)
	forward, state, swallow := c.kt.onKey(c.remote, code, kb.flags&llkhfUp == 0)
	if forward {
		emit(c.events, Event{Kind: EvKey, Code: code, State: state})
	}
	return swallow
}

func (c *winCapture) post(msg uint32, wparam, lparam uintptr) {
	pPostThreadMessage.Call(uintptr(c.tid), uintptr(msg), wparam, lparam)
}

func (c *winCapture) Events() <-chan Event { return c.events }
func (c *winCapture) Grab()                { c.post(wmGrab, 0, 0) }

func (c *winCapture) Release(side layout.Side, frac float64, warp bool) {
	w := uintptr(side)
	if warp {
		w |= 0x100
	}
	c.post(wmRelease, w, uintptr(frac*1e6))
}

func (c *winCapture) SetArmedEdges(mask uint8) { c.armed.Store(uint32(mask)) }

func (c *winCapture) Size() (int, int) {
	v := virtualScreen()
	return v.W, v.H
}

func (c *winCapture) Describe() string {
	return "Windows low-level mouse and keyboard hooks"
}

func (c *winCapture) Close() {
	c.post(wmStop, 0, 0)
	select {
	case <-c.stopped:
	case <-time.After(2 * time.Second):
	}
}

// ---------------------------------------------------------------------------
// Injection

type winInjector struct {
	mu      sync.Mutex
	down    [keys.MaxCode]bool
	buttons uint32
}

// NewInjector returns the SendInput based injector.
func NewInjector(opts Options) (Injector, error) {
	initProcess()
	return &winInjector{}, nil
}

func (w *winInjector) Size() (int, int) {
	v := virtualScreen()
	return v.W, v.H
}

func (w *winInjector) Describe() string { return "Windows SendInput" }
func (w *winInjector) Close()           { w.ReleaseAll() }

func sendMouse(dx, dy int32, data, flags uint32) {
	var in mouseInput
	in.typ = inputMouse
	in.mi.dx, in.mi.dy = dx, dy
	in.mi.mouseData = data
	in.mi.flags = flags
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

// normalize converts a pixel to the 0..65535 range SendInput expects, such
// that Windows maps it back to exactly that pixel.
func normalize(v, size int) int32 {
	if v < 0 {
		v = 0
	} else if v > size-1 {
		v = size - 1
	}
	return int32((v*65536 + size - 1) / size)
}

func (w *winInjector) MoveAbs(x, y int) {
	v := virtualScreen()
	if v.W <= 0 || v.H <= 0 {
		return
	}
	sendMouse(normalize(x, v.W), normalize(y, v.H), 0,
		mouseeventfMove|mouseeventfAbsolute|mouseeventfVirtualDesk)
}

func (w *winInjector) Button(b uint8, down bool) {
	var flags, data uint32
	pick := func(d, u uint32) uint32 {
		if down {
			return d
		}
		return u
	}
	switch b {
	case keys.ButtonLeft:
		flags = pick(mouseeventfLeftDown, mouseeventfLeftUp)
	case keys.ButtonRight:
		flags = pick(mouseeventfRightDown, mouseeventfRightUp)
	case keys.ButtonMiddle:
		flags = pick(mouseeventfMiddleDown, mouseeventfMiddleUp)
	case keys.ButtonBack:
		flags, data = pick(mouseeventfXDown, mouseeventfXUp), 1
	case keys.ButtonForward:
		flags, data = pick(mouseeventfXDown, mouseeventfXUp), 2
	default:
		return
	}
	w.mu.Lock()
	if down {
		w.buttons |= 1 << b
	} else {
		w.buttons &^= 1 << b
	}
	w.mu.Unlock()
	sendMouse(0, 0, data, flags)
}

func (w *winInjector) Wheel(dx, dy int) {
	if dy != 0 {
		sendMouse(0, 0, uint32(int32(dy)), mouseeventfWheel)
	}
	if dx != 0 {
		sendMouse(0, 0, uint32(int32(dx)), mouseeventfHWheel)
	}
}

func (w *winInjector) Key(code uint16, state uint8) {
	scan, ext, vk := keys.ToWindows(code)
	if scan == 0 && vk == 0 {
		return
	}
	var in keybdInput
	in.typ = inputKeyboard
	if vk != 0 {
		in.ki.vk = vk
	} else {
		in.ki.scan = scan
		in.ki.flags = keyeventfScanCode
		if ext {
			in.ki.flags |= keyeventfExtended
		}
	}
	if state == keys.Up {
		in.ki.flags |= keyeventfKeyUp
	}
	w.mu.Lock()
	w.down[code] = state != keys.Up
	w.mu.Unlock()
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

func (w *winInjector) ReleaseAll() {
	w.mu.Lock()
	var codes []uint16
	for code, d := range w.down {
		if d {
			codes = append(codes, uint16(code))
		}
	}
	buttons := w.buttons
	w.mu.Unlock()
	for _, code := range codes {
		w.Key(code, keys.Up)
	}
	for b := uint8(1); b <= keys.ButtonForward; b++ {
		if buttons&(1<<b) != 0 {
			w.Button(b, false)
		}
	}
}

// ---------------------------------------------------------------------------
// Self test

// OwnInjection reports whether an injector can work next to a capture in the
// same program (Options.Own). Not on Windows yet: while the mouse steers
// another computer the cursor is parked in the middle of the screen, and
// would have to be in two places at once.
func OwnInjection() bool { return false }

// SelfTest checks the input backend on this computer. Without grab it only
// installs the hooks and lists the monitors. With grab it also switches to
// remote mode for about half a second. During that time it feeds synthetic
// mouse movement, every key, the mouse buttons and the wheel through Windows
// and verifies that the hooks swallow them (no application sees them) and
// report them correctly. The real cursor is put back afterwards.
func SelfTest(out io.Writer, grab bool) error {
	initProcess()
	desk := layout.NewDesktop(Monitors())
	fmt.Fprintf(out, "desktop: %dx%d at %d,%d\n", desk.Bounds.W, desk.Bounds.H, desk.Bounds.X, desk.Bounds.Y)
	for i, m := range desk.Monitors {
		fmt.Fprintf(out, "  monitor %d: %dx%d at %d,%d\n", i+1, m.W, m.H, m.X, m.Y)
	}
	ci, err := NewCapture(Options{HideCursor: true})
	if err != nil {
		return err
	}
	c := ci.(*winCapture)
	defer c.Close()
	fmt.Fprintln(out, "hooks: installed")
	time.Sleep(150 * time.Millisecond)
	if !grab {
		return nil
	}

	orig := cursorPos()
	park := winPoint{int32(metric(smCXScreen) / 2), int32(metric(smCYScreen) / 2)}
	c.Grab()
	time.Sleep(120 * time.Millisecond)
	afterGrab := cursorPos()
	var info cursorInfo
	info.size = uint32(unsafe.Sizeof(info))
	pGetCursorInfo.Call(uintptr(unsafe.Pointer(&info)))
	hidden := info.flags == 0 || info.hCursor == 0

	const moves, stepX, stepY = 5, 12, 7
	for i := 0; i < moves; i++ {
		sendMouse(stepX, stepY, 0, mouseeventfMove)
		time.Sleep(15 * time.Millisecond)
	}
	time.Sleep(60 * time.Millisecond)
	afterMoves := cursorPos()

	var n int
	var sx, sy float64
	for _, ev := range drainEvents(c.events) {
		if ev.Kind == EvMotion {
			n++
			sx += ev.DX
			sy += ev.DY
		}
	}
	restore := func() {
		c.Release(layout.Left, 0.5, false)
		time.Sleep(50 * time.Millisecond)
		setCursorPos(int(orig.x), int(orig.y))
	}
	fmt.Fprintf(out, "grab: cursor parked at %d,%d (expected %d,%d)\n", afterGrab.x, afterGrab.y, park.x, park.y)
	fmt.Fprintf(out, "grab: %d synthetic moves of %d,%d -> %d motion events, total %.0f,%.0f\n", moves, stepX, stepY, n, sx, sy)
	fmt.Fprintf(out, "grab: cursor after the moves %d,%d (must not have moved)\n", afterMoves.x, afterMoves.y)
	fmt.Fprintf(out, "grab: cursor hidden while remote: %v\n", hidden)
	switch {
	case afterGrab != park:
		restore()
		return errors.New("the cursor was not parked when entering remote mode")
	case n == 0:
		restore()
		return errors.New("no motion was captured in remote mode")
	case sx <= 0 || sy <= 0:
		restore()
		return errors.New("captured motion has the wrong direction")
	case afterMoves != park:
		restore()
		return errors.New("mouse movement was not swallowed in remote mode")
	}

	// Loop-back: everything the injector produces is swallowed by the hooks
	// (so no application sees it) and must come back as the same event.
	problems := injectLoopback(out, c, park)
	restore()
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(out, "  mismatch: %s\n", p)
		}
		return fmt.Errorf("%d injected events did not come back as expected", len(problems))
	}
	return nil
}

func drainEvents(ch chan Event) []Event {
	var out []Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// injectLoopback runs while the capture is in remote mode. Everything is
// injected in a few batches so the keyboard and mouse are held only briefly.
func injectLoopback(out io.Writer, c *winCapture, park winPoint) (problems []string) {
	inj := &winInjector{}
	collect := func() []Event {
		time.Sleep(40 * time.Millisecond)
		return drainEvents(c.events)
	}
	tap := func(kind EventKind, code uint16) [2]Event {
		return [2]Event{{Kind: kind, Code: code, State: 1}, {Kind: kind, Code: code, State: 0}}
	}
	// hasTap reports whether got contains the press directly followed by the release.
	hasTap := func(got []Event, want [2]Event) bool {
		for i := 0; i+1 < len(got); i++ {
			if got[i] == want[0] && got[i+1] == want[1] {
				return true
			}
		}
		return false
	}
	drainEvents(c.events)

	// One harmless key first: if the keyboard is not being swallowed, stop
	// before anything else is injected.
	inj.Key(keys.LeftShift, keys.Down)
	inj.Key(keys.LeftShift, keys.Up)
	if got := collect(); len(got) != 2 || !hasTap(got, tap(EvKey, keys.LeftShift)) {
		return []string{"left shift tap came back as " + describeEvents(got)}
	}

	var codes []uint16
	for code := uint16(1); code < keys.MaxCode; code++ {
		if scan, _, vk := keys.ToWindows(code); scan != 0 || vk != 0 {
			codes = append(codes, code)
			inj.Key(code, keys.Down)
			inj.Key(code, keys.Up)
		}
	}
	got := collect()
	for _, code := range codes {
		if !hasTap(got, tap(EvKey, code)) {
			problems = append(problems, fmt.Sprintf("key %s (%d) did not come back", keys.Name(code), code))
		}
	}
	if len(got) != 2*len(codes) {
		problems = append(problems, fmt.Sprintf("%d key events injected, %d captured", 2*len(codes), len(got)))
	}
	fmt.Fprintf(out, "loop-back: %d keys injected, %d key events captured\n", len(codes), len(got))

	for b := uint8(keys.ButtonLeft); b <= keys.ButtonForward; b++ {
		inj.Button(b, true)
		inj.Button(b, false)
	}
	inj.Wheel(0, 120)
	inj.Wheel(0, -360)
	inj.Wheel(-240, 0)
	got = collect()
	for b := uint16(keys.ButtonLeft); b <= keys.ButtonForward; b++ {
		if !hasTap(got, tap(EvButton, b)) {
			problems = append(problems, fmt.Sprintf("mouse button %d did not come back", b))
		}
	}
	wheel := []Event{{Kind: EvWheel, WY: 120}, {Kind: EvWheel, WY: -360}, {Kind: EvWheel, WX: -240}}
	if n := len(got); n != 13 || got[n-3] != wheel[0] || got[n-2] != wheel[1] || got[n-1] != wheel[2] {
		problems = append(problems, "buttons and wheel came back as: "+describeEvents(got))
	}
	fmt.Fprintf(out, "loop-back: 5 mouse buttons and 3 wheel steps injected, %d events captured\n", len(got))

	// Absolute positioning must be exact to the pixel everywhere.
	v := virtualScreen()
	targets := [][2]int{{0, 0}, {v.W - 1, v.H - 1}, {v.W - 1, 0}, {0, v.H - 1}, {v.W / 3, v.H / 3}, {1234 % v.W, 567 % v.H}, {v.W/2 + 1, v.H/2 + 1}}
	var want []Event
	for _, t := range targets {
		inj.MoveAbs(t[0], t[1])
		dx, dy := float64(v.X+t[0])-float64(park.x), float64(v.Y+t[1])-float64(park.y)
		if dx != 0 || dy != 0 {
			want = append(want, Event{Kind: EvMotion, DX: dx, DY: dy})
		}
	}
	got = collect()
	exact := 0
	for i, w := range want {
		if i < len(got) && got[i] == w {
			exact++
		}
	}
	if exact != len(want) || len(got) != len(want) {
		problems = append(problems, fmt.Sprintf("absolute moves: wanted offsets %s; got %s", describeEvents(want), describeEvents(got)))
	}
	fmt.Fprintf(out, "loop-back: %d/%d absolute cursor positions exact to the pixel\n", exact, len(want))
	return problems
}

func describeEvents(evs []Event) string {
	if len(evs) == 0 {
		return "nothing"
	}
	var parts []string
	for _, e := range evs {
		switch e.Kind {
		case EvKey:
			parts = append(parts, fmt.Sprintf("key %s state %d", keys.Name(e.Code), e.State))
		case EvButton:
			parts = append(parts, fmt.Sprintf("button %d state %d", e.Code, e.State))
		case EvWheel:
			parts = append(parts, fmt.Sprintf("wheel %d,%d", e.WX, e.WY))
		case EvMotion:
			parts = append(parts, fmt.Sprintf("motion %.0f,%.0f", e.DX, e.DY))
		default:
			parts = append(parts, fmt.Sprintf("event kind %d", e.Kind))
		}
	}
	return strings.Join(parts, "; ")
}
