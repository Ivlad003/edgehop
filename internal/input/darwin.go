//go:build darwin

package input

import (
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/ebitengine/purego"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
	"github.com/Ivlad003/edgehop/internal/logx"
)

// macOS backend.
//
// CoreGraphics is called directly through purego, so the program builds
// without cgo (and cross-compiles from any other system).
//
// Capture is an event tap. In remote mode the tap swallows every event and
// reports mouse movement from the events' delta fields. The cursor is hidden
// and frozen in place (mouse and cursor are dissociated), so it cannot wander
// around this screen while the mouse is steering another computer.
//
// Injection posts CoreGraphics events. Modifier state and click counts are
// tracked here because synthetic events do not get them automatically.
//
// Both need the "Accessibility" permission (System Settings > Privacy &
// Security); capture also needs "Input Monitoring".

const (
	cgHIDEventTap     = 0
	cgSessionEventTap = 1

	evtLeftDown      = 1
	evtLeftUp        = 2
	evtRightDown     = 3
	evtRightUp       = 4
	evtMouseMoved    = 5
	evtLeftDragged   = 6
	evtRightDragged  = 7
	evtKeyDown       = 10
	evtKeyUp         = 11
	evtFlagsChanged  = 12
	evtScrollWheel   = 22
	evtOtherDown     = 25
	evtOtherUp       = 26
	evtOtherDragged  = 27
	evtTapOffTimeout = 0xFFFFFFFE
	evtTapOffInput   = 0xFFFFFFFF

	fldClickState   = 1
	fldButtonNumber = 3
	fldDeltaX       = 4
	fldDeltaY       = 5
	fldAutorepeat   = 8
	fldKeycode      = 9
	fldScrollIsCont = 88
	fldScrollFixed1 = 93
	fldScrollFixed2 = 94
	fldScrollPoint1 = 96
	fldScrollPoint2 = 97

	flagCapsLock = 0x00010000
	flagShift    = 0x00020000
	flagControl  = 0x00040000
	flagOption   = 0x00080000
	flagCommand  = 0x00100000

	macCapsLock = 0x39
	macFn       = 0x3F

	scrollUnitPixel   = 0
	pixelsPerNotch    = 40.0 // how far one wheel notch scrolls
	hidSystemState    = 1
	cfStringUTF8      = 0x08000100
	doubleClickWindow = 500 * time.Millisecond
)

type cgPoint struct{ X, Y float64 }
type cgRect struct{ X, Y, W, H float64 }

var (
	macOnce sync.Once
	macErr  error

	cgEventTapCreate            func(tap, place, options uint32, mask uint64, callback, info uintptr) uintptr
	cgEventTapEnable            func(tap uintptr, enable bool)
	cgEventGetIntegerValueField func(ev uintptr, field uint32) int64
	cgEventSetIntegerValueField func(ev uintptr, field uint32, value int64)
	cgEventGetFlags             func(ev uintptr) uint64
	cgEventSetFlags             func(ev uintptr, flags uint64)
	cgEventGetLocation          func(ev uintptr) cgPoint
	cgEventSourceCreate         func(state int32) uintptr
	cgEventCreateKeyboardEvent  func(src uintptr, keycode uint16, down bool) uintptr
	// CGPoint is passed as two doubles, which is how the ABI passes it.
	cgEventCreateMouseEvent        func(src uintptr, typ uint32, x, y float64, button uint32) uintptr
	cgEventCreateScrollWheelEvent2 func(src uintptr, units, count uint32, w1, w2, w3 int32) uintptr
	cgEventPost                    func(tap uint32, ev uintptr)
	cgWarpMouseCursorPosition      func(x, y float64) int32
	cgAssociateMouseAndCursor      func(connected uint32) int32
	cgMainDisplayID                func() uint32
	cgDisplayHideCursor            func(display uint32) int32
	cgDisplayShowCursor            func(display uint32) int32
	cgGetActiveDisplayList         func(max uint32, displays *uint32, count *uint32) int32
	cgDisplayBounds                func(display uint32) cgRect
	cgDisplayPixelsWide            func(display uint32) uintptr
	cgDisplayPixelsHigh            func(display uint32) uintptr

	cfMachPortCreateRunLoopSource func(alloc, port uintptr, order int) uintptr
	cfRunLoopGetCurrent           func() uintptr
	cfRunLoopAddSource            func(rl, source, mode uintptr)
	cfRunLoopRun                  func()
	cfRelease                     func(obj uintptr)
	cfStringCreateWithCString     func(alloc uintptr, s string, encoding uint32) uintptr
	cfRunLoopCommonModes          uintptr
	cfBooleanTrue                 uintptr

	// Optional symbols; nil when the system does not have them.
	cgRequestPostEventAccess   func() bool
	cgRequestListenEventAccess func() bool
	cgsDefaultConnection       func() int32
	cgsSetConnectionProperty   func(cid, target int32, key, value uintptr) int32
	// How long macOS ignores the real mouse and keyboard after the cursor
	// was moved or an event was posted by a program.
	cgSetSuppressionInterval       func(seconds float64) int32
	cgSourceSetSuppressionInterval func(src uintptr, seconds float64)
	cgSourceSetSuppressionFilter   func(src uintptr, filter, state uint32)
)

func macInit() error {
	macOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				macErr = fmt.Errorf("cannot load CoreGraphics: %v", r)
			}
		}()
		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			macErr = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			macErr = err
			return
		}
		purego.RegisterLibFunc(&cgEventTapCreate, cg, "CGEventTapCreate")
		purego.RegisterLibFunc(&cgEventTapEnable, cg, "CGEventTapEnable")
		purego.RegisterLibFunc(&cgEventGetIntegerValueField, cg, "CGEventGetIntegerValueField")
		purego.RegisterLibFunc(&cgEventSetIntegerValueField, cg, "CGEventSetIntegerValueField")
		purego.RegisterLibFunc(&cgEventGetFlags, cg, "CGEventGetFlags")
		purego.RegisterLibFunc(&cgEventSetFlags, cg, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventGetLocation, cg, "CGEventGetLocation")
		purego.RegisterLibFunc(&cgEventSourceCreate, cg, "CGEventSourceCreate")
		purego.RegisterLibFunc(&cgEventCreateKeyboardEvent, cg, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgEventCreateMouseEvent, cg, "CGEventCreateMouseEvent")
		purego.RegisterLibFunc(&cgEventCreateScrollWheelEvent2, cg, "CGEventCreateScrollWheelEvent2")
		purego.RegisterLibFunc(&cgEventPost, cg, "CGEventPost")
		purego.RegisterLibFunc(&cgWarpMouseCursorPosition, cg, "CGWarpMouseCursorPosition")
		purego.RegisterLibFunc(&cgAssociateMouseAndCursor, cg, "CGAssociateMouseAndMouseCursorPosition")
		purego.RegisterLibFunc(&cgMainDisplayID, cg, "CGMainDisplayID")
		purego.RegisterLibFunc(&cgDisplayHideCursor, cg, "CGDisplayHideCursor")
		purego.RegisterLibFunc(&cgDisplayShowCursor, cg, "CGDisplayShowCursor")
		purego.RegisterLibFunc(&cgGetActiveDisplayList, cg, "CGGetActiveDisplayList")
		purego.RegisterLibFunc(&cgDisplayBounds, cg, "CGDisplayBounds")
		purego.RegisterLibFunc(&cgDisplayPixelsWide, cg, "CGDisplayPixelsWide")
		purego.RegisterLibFunc(&cgDisplayPixelsHigh, cg, "CGDisplayPixelsHigh")

		purego.RegisterLibFunc(&cfMachPortCreateRunLoopSource, cf, "CFMachPortCreateRunLoopSource")
		purego.RegisterLibFunc(&cfRunLoopGetCurrent, cf, "CFRunLoopGetCurrent")
		purego.RegisterLibFunc(&cfRunLoopAddSource, cf, "CFRunLoopAddSource")
		purego.RegisterLibFunc(&cfRunLoopRun, cf, "CFRunLoopRun")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&cfStringCreateWithCString, cf, "CFStringCreateWithCString")

		deref := func(lib uintptr, name string) uintptr {
			addr, err := purego.Dlsym(lib, name)
			if err != nil || addr == 0 {
				return 0
			}
			return *(*uintptr)(ptr(addr))
		}
		cfRunLoopCommonModes = deref(cf, "kCFRunLoopCommonModes")
		cfBooleanTrue = deref(cf, "kCFBooleanTrue")
		if cfRunLoopCommonModes == 0 {
			macErr = errors.New("kCFRunLoopCommonModes not found")
			return
		}

		optional := func(fptr any, name string) {
			if addr, err := purego.Dlsym(cg, name); err == nil && addr != 0 {
				purego.RegisterFunc(fptr, addr)
			}
		}
		optional(&cgRequestPostEventAccess, "CGRequestPostEventAccess")
		optional(&cgRequestListenEventAccess, "CGRequestListenEventAccess")
		optional(&cgsDefaultConnection, "_CGSDefaultConnection")
		optional(&cgsSetConnectionProperty, "CGSSetConnectionProperty")
		optional(&cgSetSuppressionInterval, "CGSetLocalEventsSuppressionInterval")
		optional(&cgSourceSetSuppressionInterval, "CGEventSourceSetLocalEventsSuppressionInterval")
		optional(&cgSourceSetSuppressionFilter, "CGEventSourceSetLocalEventsFilterDuringSuppressionState")
	})
	return macErr
}

const macPermissionHelp = `macOS has not allowed edgehop to control the mouse and keyboard yet.

Open System Settings > Privacy & Security and enable the program you start
edgehop from (Terminal, iTerm, ... or edgehop itself) under BOTH
  - Accessibility
  - Input Monitoring
then start edgehop again.`

// Monitors returns the active displays in global display coordinates.
func Monitors() []layout.Rect {
	if macInit() != nil {
		return nil
	}
	var ids [16]uint32
	var n uint32
	var out []layout.Rect
	if cgGetActiveDisplayList(uint32(len(ids)), &ids[0], &n) == 0 {
		for i := 0; i < int(n) && i < len(ids); i++ {
			r := cgDisplayBounds(ids[i])
			plausible := r.W >= 100 && r.W <= 100000 && r.H >= 100 && r.H <= 100000 &&
				math.Abs(r.X) < 1e6 && math.Abs(r.Y) < 1e6
			if !plausible {
				out = nil
				break
			}
			out = append(out, layout.Rect{X: int(r.X), Y: int(r.Y), W: int(r.W), H: int(r.H)})
		}
	}
	if len(out) == 0 {
		id := cgMainDisplayID()
		out = []layout.Rect{{X: 0, Y: 0, W: int(cgDisplayPixelsWide(id)), H: int(cgDisplayPixelsHigh(id))}}
	}
	return out
}

// SelfTest is only implemented on Windows.
func SelfTest(out io.Writer, grab bool) error {
	return errors.New("the self test is only available on Windows")
}

// warp moves the cursor without generating events. Re-associating mouse and
// cursor right afterwards cancels the quarter second during which macOS
// would otherwise ignore the mouse.
func warp(x, y float64) {
	cgWarpMouseCursorPosition(x, y)
	cgAssociateMouseAndCursor(1)
}

// allowBackgroundCursorHiding lets a process without a window hide the
// cursor. It relies on an undocumented call and is skipped if unavailable.
func allowBackgroundCursorHiding() {
	if cgsDefaultConnection == nil || cgsSetConnectionProperty == nil || cfBooleanTrue == 0 {
		return
	}
	key := cfStringCreateWithCString(0, "SetsCursorInBackground", cfStringUTF8)
	if key == 0 {
		return
	}
	cid := cgsDefaultConnection()
	cgsSetConnectionProperty(cid, cid, key, cfBooleanTrue)
	cfRelease(key)
}

// ---------------------------------------------------------------------------
// Capture

type macCapture struct {
	events chan Event
	hide   bool
	tap    uintptr

	mu           sync.Mutex
	remote       bool
	hidden       bool
	armed        uint8
	desk         layout.Desktop
	deskAt       time.Time
	buttons      uint32
	localButtons uint32
	kt           keyTracker
	keyb         keyboard
	modDown      map[uint16]bool
}

var (
	macActive *macCapture
	macTapCB  uintptr
	macCBOnce sync.Once
)

// NewCapture installs the event tap.
func NewCapture(opts Options) (Capture, error) {
	if err := macInit(); err != nil {
		return nil, err
	}
	if macActive != nil {
		return nil, errors.New("input capture is already running in this process")
	}
	if cgRequestListenEventAccess != nil {
		cgRequestListenEventAccess()
	}
	if cgRequestPostEventAccess != nil {
		cgRequestPostEventAccess()
	}
	c := &macCapture{
		events:  make(chan Event, 4096),
		hide:    opts.HideCursor,
		modDown: map[uint16]bool{},
	}
	c.desk = layout.NewDesktop(Monitors())
	c.deskAt = time.Now()
	macCBOnce.Do(func() {
		macTapCB = purego.NewCallback(func(proxy, etype, event, info uintptr) uintptr {
			c := macActive
			if c != nil && c.handle(uint32(etype), event) {
				return 0 // swallow
			}
			return event
		})
	})
	ready := make(chan error, 1)
	go c.loop(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	if c.hide {
		allowBackgroundCursorHiding()
	}
	return c, nil
}

func (c *macCapture) loop(ready chan<- error) {
	runtime.LockOSThread()
	var mask uint64
	for _, t := range []uint{evtLeftDown, evtLeftUp, evtRightDown, evtRightUp, evtMouseMoved,
		evtLeftDragged, evtRightDragged, evtKeyDown, evtKeyUp, evtFlagsChanged,
		evtScrollWheel, evtOtherDown, evtOtherUp, evtOtherDragged} {
		mask |= 1 << t
	}
	macActive = c
	tap := cgEventTapCreate(cgHIDEventTap, 0, 0, mask, macTapCB, 0)
	if tap == 0 {
		tap = cgEventTapCreate(cgSessionEventTap, 0, 0, mask, macTapCB, 0)
	}
	if tap == 0 {
		macActive = nil
		ready <- errors.New(macPermissionHelp)
		return
	}
	c.tap = tap
	source := cfMachPortCreateRunLoopSource(0, tap, 0)
	cfRunLoopAddSource(cfRunLoopGetCurrent(), source, cfRunLoopCommonModes)
	cgEventTapEnable(tap, true)
	ready <- nil
	cfRunLoopRun()
}

// handle processes one tapped event; it returns true to swallow it.
func (c *macCapture) handle(etype uint32, ev uintptr) bool {
	if etype == evtTapOffTimeout || etype == evtTapOffInput {
		cgEventTapEnable(c.tap, true) // macOS switched the tap off; switch it back on
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.remote && (etype == evtKeyDown || etype == evtFlagsChanged) {
		// In local mode macOS follows the keyboard too, and every key event
		// says which modifier keys it takes for held.
		c.keyb.keep(macMods(cgEventGetFlags(ev)))
	}
	switch etype {
	case evtMouseMoved, evtLeftDragged, evtRightDragged, evtOtherDragged:
		if c.remote {
			// The cursor is frozen (see Grab), so the event's delta fields
			// are the mouse movement. The cursor is never moved while
			// remote: a warp makes macOS pause mouse input and distorts the
			// next movement, which shows up as a cursor that sticks and jerks.
			dx := cgEventGetIntegerValueField(ev, fldDeltaX)
			dy := cgEventGetIntegerValueField(ev, fldDeltaY)
			if dx != 0 || dy != 0 {
				emit(c.events, Event{Kind: EvMotion, DX: float64(dx), DY: float64(dy), Mods: c.keyb.mods()})
			}
			return true
		}
		if c.buttons == 0 && c.armed != 0 {
			if time.Since(c.deskAt) > 3*time.Second {
				c.desk = layout.NewDesktop(Monitors())
				c.deskAt = time.Now()
			}
			p := cgEventGetLocation(ev)
			x, y := int(math.Floor(p.X)), int(math.Floor(p.Y))
			if side, ok := layout.FirstSide(c.desk.Edges(x, y) & c.armed); ok {
				sys := macMods(cgEventGetFlags(ev))
				c.keyb.keep(sys)
				corner, span := c.desk.Corner(side, x, y)
				emit(c.events, Event{Kind: EvEdge, Side: side, Frac: c.desk.Frac(side, x, y),
					Mods: sys | c.keyb.mods(), Corner: corner, Span: span})
			}
		}
		return false
	case evtLeftDown:
		return c.button(keys.ButtonLeft, true)
	case evtLeftUp:
		return c.button(keys.ButtonLeft, false)
	case evtRightDown:
		return c.button(keys.ButtonRight, true)
	case evtRightUp:
		return c.button(keys.ButtonRight, false)
	case evtOtherDown, evtOtherUp:
		b := uint16(keys.ButtonMiddle)
		switch cgEventGetIntegerValueField(ev, fldButtonNumber) {
		case 3:
			b = keys.ButtonBack
		case 4:
			b = keys.ButtonForward
		}
		return c.button(b, etype == evtOtherDown)
	case evtScrollWheel:
		if !c.remote {
			return false
		}
		var wx, wy float64
		if cgEventGetIntegerValueField(ev, fldScrollIsCont) != 0 { // trackpad: pixels
			wy = float64(cgEventGetIntegerValueField(ev, fldScrollPoint1)) * 120 / pixelsPerNotch
			wx = float64(cgEventGetIntegerValueField(ev, fldScrollPoint2)) * 120 / pixelsPerNotch
		} else { // wheel: lines in 16.16 fixed point
			wy = float64(cgEventGetIntegerValueField(ev, fldScrollFixed1)) / 65536 * 120
			wx = float64(cgEventGetIntegerValueField(ev, fldScrollFixed2)) / 65536 * 120
		}
		// CoreGraphics counts "left" as positive; the wire counts "right".
		emit(c.events, Event{Kind: EvWheel, WX: int32(math.Round(-wx)), WY: int32(math.Round(wy))})
		return true
	case evtKeyDown, evtKeyUp:
		code := keys.FromMac(uint16(cgEventGetIntegerValueField(ev, fldKeycode)))
		return c.keyEvent(code, etype == evtKeyDown)
	case evtFlagsChanged:
		kc := uint16(cgEventGetIntegerValueField(ev, fldKeycode))
		flags := cgEventGetFlags(ev)
		if kc == macFn {
			return c.remote
		}
		if kc == macCapsLock {
			// One event per press; forward it as a tap.
			sw := c.keyEvent(keys.CapsLock, true)
			c.keyEvent(keys.CapsLock, false)
			return sw
		}
		var generic uint64
		switch kc {
		case 0x38, 0x3C:
			generic = flagShift
		case 0x3B, 0x3E:
			generic = flagControl
		case 0x3A, 0x3D:
			generic = flagOption
		case 0x36, 0x37:
			generic = flagCommand
		default:
			return c.remote
		}
		// A modifier event does not say "down" or "up". If the flag is clear
		// both keys of the pair are up; otherwise this key changed state.
		down := !c.modDown[kc]
		if flags&generic == 0 {
			down = false
		}
		c.modDown[kc] = down
		return c.keyEvent(keys.FromMac(kc), down)
	}
	return c.remote
}

// macMods turns the modifier flags of an event into keys.Mod* bits.
func macMods(flags uint64) uint8 {
	var m uint8
	if flags&flagControl != 0 {
		m |= keys.ModCtrl
	}
	if flags&flagOption != 0 {
		m |= keys.ModAlt
	}
	if flags&flagShift != 0 {
		m |= keys.ModShift
	}
	if flags&flagCommand != 0 {
		m |= keys.ModMeta
	}
	return m
}

func (c *macCapture) keyEvent(code uint16, down bool) bool {
	if hit, eat := c.keyb.onKey(c.remote, code, down); eat {
		if hit >= 0 {
			emit(c.events, Event{Kind: EvHotkey, Code: uint16(hit), Drag: c.buttons != 0})
		}
		return true
	}
	forward, state, swallow := c.kt.onKey(c.remote, code, down)
	if forward {
		emit(c.events, Event{Kind: EvKey, Code: code, State: state})
	}
	return swallow
}

func (c *macCapture) button(b uint16, down bool) bool {
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
		c.localButtons &^= bit
		return false // pressed locally before the switch: release it locally
	}
	st := uint8(0)
	if down {
		st = 1
	}
	emit(c.events, Event{Kind: EvButton, Code: b, State: st})
	return true
}

func (c *macCapture) Events() <-chan Event { return c.events }

func (c *macCapture) Grab(home bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remote {
		return
	}
	// Hide the cursor and freeze it where it is. While frozen, mouse events
	// keep arriving with their movement in the delta fields, and no local
	// application sees the cursor move. (Same recipe as Deskflow.) A Release
	// without a warp finds the cursor at this very place, so there is
	// nothing to remember for home.
	if c.hide {
		cgDisplayHideCursor(cgMainDisplayID())
		c.hidden = true
	}
	if cgSetSuppressionInterval != nil {
		cgSetSuppressionInterval(0.0001)
	}
	cgAssociateMouseAndCursor(0)
	c.remote = true
}

func (c *macCapture) Release(side layout.Side, frac float64, doWarp bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.remote = false
	c.kt.clearRemote()
	c.desk = layout.NewDesktop(Monitors())
	c.deskAt = time.Now()
	// Give the mouse its cursor back, and do not let macOS ignore the mouse
	// for a moment after the cursor is repositioned.
	cgAssociateMouseAndCursor(1)
	if cgSetSuppressionInterval != nil {
		cgSetSuppressionInterval(0)
	}
	if doWarp {
		x, y := c.desk.Entry(side, frac, 2)
		warp(float64(x), float64(y))
	}
	if c.hidden {
		cgDisplayShowCursor(cgMainDisplayID())
		c.hidden = false
	}
}

func (c *macCapture) SetArmedEdges(mask uint8) {
	c.mu.Lock()
	c.armed = mask
	c.mu.Unlock()
}

func (c *macCapture) SetHotkeys(list []Hotkey) { c.keyb.setHotkeys(list) }

func (c *macCapture) Size() (int, int) {
	d := layout.NewDesktop(Monitors())
	return d.Bounds.W, d.Bounds.H
}

func (c *macCapture) Describe() string { return "macOS event tap" }

func (c *macCapture) Close() {
	c.mu.Lock()
	wasRemote := c.remote
	c.mu.Unlock()
	if wasRemote {
		c.Release(0, 0, false)
	}
	if c.tap != 0 {
		cgEventTapEnable(c.tap, false)
	}
}

// ---------------------------------------------------------------------------
// Injection

type macInjector struct {
	mu      sync.Mutex
	src     uintptr
	bounds  layout.Rect
	x, y    float64 // last position, global display coordinates
	buttons uint32
	flags   uint64
	modDown map[uint16]bool
	down    [keys.MaxCode]bool
	remX    float64
	remY    float64

	clickAt    time.Time
	clickBtn   uint8
	clickX     float64
	clickY     float64
	clickCount int64
}

// NewInjector returns the CoreGraphics event injector.
func NewInjector(opts Options) (Injector, error) {
	if err := macInit(); err != nil {
		return nil, err
	}
	if cgRequestPostEventAccess != nil && !cgRequestPostEventAccess() {
		logx.Warnf("%s\n(edgehop keeps running; input starts working once the permission is granted.)", macPermissionHelp)
	}
	m := &macInjector{modDown: map[uint16]bool{}}
	m.src = cgEventSourceCreate(hidSystemState)
	// By default macOS ignores the computer's own mouse and keyboard for a
	// quarter of a second after every posted event. Turn that off, so this
	// computer stays usable with its own devices while it is being controlled.
	if m.src != 0 {
		if cgSourceSetSuppressionInterval != nil {
			cgSourceSetSuppressionInterval(m.src, 0)
		}
		if cgSourceSetSuppressionFilter != nil {
			const permitAll = 0x7                             // local mouse, local keyboard and system events
			cgSourceSetSuppressionFilter(m.src, permitAll, 0) // during the suppression interval
			cgSourceSetSuppressionFilter(m.src, permitAll, 1) // during a remote mouse drag
		}
	}
	m.bounds = layout.NewDesktop(Monitors()).Bounds
	return m, nil
}

func (m *macInjector) Size() (int, int) {
	b := layout.NewDesktop(Monitors()).Bounds
	m.mu.Lock()
	m.bounds = b
	m.mu.Unlock()
	return b.W, b.H
}

func (m *macInjector) Describe() string { return "macOS CoreGraphics events" }
func (m *macInjector) Close()           { m.ReleaseAll() }

func (m *macInjector) post(ev uintptr) {
	if ev == 0 {
		return
	}
	cgEventSetFlags(ev, m.flags)
	cgEventPost(cgHIDEventTap, ev)
	cfRelease(ev)
}

func (m *macInjector) MoveAbs(x, y int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	gx := float64(m.bounds.X + min(max(x, 0), max(m.bounds.W-1, 0)))
	gy := float64(m.bounds.Y + min(max(y, 0), max(m.bounds.H-1, 0)))
	// While a button is held, macOS expects "dragged" instead of "moved".
	typ, btn := uint32(evtMouseMoved), uint32(0)
	switch {
	case m.buttons&(1<<keys.ButtonLeft) != 0:
		typ = evtLeftDragged
	case m.buttons&(1<<keys.ButtonRight) != 0:
		typ, btn = evtRightDragged, 1
	case m.buttons != 0:
		typ, btn = evtOtherDragged, 2
	}
	ev := cgEventCreateMouseEvent(m.src, typ, gx, gy, btn)
	if ev != 0 {
		cgEventSetIntegerValueField(ev, fldDeltaX, int64(math.Round(gx-m.x)))
		cgEventSetIntegerValueField(ev, fldDeltaY, int64(math.Round(gy-m.y)))
	}
	m.x, m.y = gx, gy
	m.post(ev)
}

func (m *macInjector) Button(b uint8, down bool) {
	var typ, btn uint32
	switch b {
	case keys.ButtonLeft:
		typ, btn = evtLeftUp, 0
		if down {
			typ = evtLeftDown
		}
	case keys.ButtonRight:
		typ, btn = evtRightUp, 1
		if down {
			typ = evtRightDown
		}
	case keys.ButtonMiddle, keys.ButtonBack, keys.ButtonForward:
		typ, btn = evtOtherUp, uint32(b)-1 // 2 = middle, 3 = back, 4 = forward
		if down {
			typ = evtOtherDown
		}
	default:
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if down {
		m.buttons |= 1 << b
		// Double and triple clicks must be announced in the event itself.
		now := time.Now()
		if b == m.clickBtn && now.Sub(m.clickAt) < doubleClickWindow &&
			math.Abs(m.x-m.clickX) <= 4 && math.Abs(m.y-m.clickY) <= 4 {
			m.clickCount++
		} else {
			m.clickCount = 1
		}
		m.clickBtn, m.clickAt, m.clickX, m.clickY = b, now, m.x, m.y
	} else {
		m.buttons &^= 1 << b
	}
	ev := cgEventCreateMouseEvent(m.src, typ, m.x, m.y, btn)
	if ev != 0 {
		cgEventSetIntegerValueField(ev, fldClickState, m.clickCount)
	}
	m.post(ev)
}

func (m *macInjector) Wheel(dx, dy int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.remY += float64(dy) * pixelsPerNotch / 120
	m.remX += float64(dx) * pixelsPerNotch / 120
	py, px := math.Trunc(m.remY), math.Trunc(m.remX)
	if py == 0 && px == 0 {
		return
	}
	m.remY -= py
	m.remX -= px
	// CoreGraphics counts "left" as positive on the horizontal axis.
	m.post(cgEventCreateScrollWheelEvent2(m.src, scrollUnitPixel, 2, int32(py), int32(-px), 0))
}

func (m *macInjector) Key(code uint16, state uint8) {
	kc, ok := keys.ToMac(code)
	if !ok || code >= keys.MaxCode {
		return
	}
	down := state != keys.Up
	m.mu.Lock()
	defer m.mu.Unlock()
	if kc == macCapsLock {
		// Caps lock toggles on the press; send a complete tap for it.
		if state == keys.Down {
			m.flags ^= flagCapsLock
			m.post(cgEventCreateKeyboardEvent(m.src, kc, true))
			m.post(cgEventCreateKeyboardEvent(m.src, kc, false))
		}
		return
	}
	m.down[code] = down

	var mod uint64
	switch kc {
	case 0x38, 0x3C:
		mod = flagShift
	case 0x3B, 0x3E:
		mod = flagControl
	case 0x3A, 0x3D:
		mod = flagOption
	case 0x36, 0x37:
		mod = flagCommand
	}
	if mod != 0 {
		m.modDown[kc] = down
		pair := map[uint64][2]uint16{
			flagShift: {0x38, 0x3C}, flagControl: {0x3B, 0x3E},
			flagOption: {0x3A, 0x3D}, flagCommand: {0x36, 0x37},
		}[mod]
		if m.modDown[pair[0]] || m.modDown[pair[1]] {
			m.flags |= mod
		} else {
			m.flags &^= mod
		}
	}
	ev := cgEventCreateKeyboardEvent(m.src, kc, down)
	if ev != 0 && state == keys.Repeat {
		cgEventSetIntegerValueField(ev, fldAutorepeat, 1)
	}
	m.post(ev)
}

func (m *macInjector) ReleaseAll() {
	m.mu.Lock()
	var codes []uint16
	for code, d := range m.down {
		if d {
			codes = append(codes, uint16(code))
		}
	}
	buttons := m.buttons
	m.mu.Unlock()
	for _, code := range codes {
		m.Key(code, keys.Up)
	}
	for b := uint8(1); b <= keys.ButtonForward; b++ {
		if buttons&(1<<b) != 0 {
			m.Button(b, false)
		}
	}
}
