//go:build darwin

package wincap

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/Ivlad003/edgehop/internal/input"
)

type cgRect struct{ X, Y, W, H float64 }

var (
	macOnce sync.Once
	macErr  error

	cfArrayGetCount        func(arr uintptr) int
	cfArrayGetValueAtIndex func(arr uintptr, i int) uintptr
	cfDictionaryGetValue   func(dict, key uintptr) uintptr
	cfNumberGetValue       func(num uintptr, typ int, out unsafe.Pointer) bool
	cfStringGetCString     func(str uintptr, buf *byte, size int, encoding uint32) bool
	cfStringGetLength      func(str uintptr) int
	cfRelease              func(obj uintptr)

	cgWindowListCopyWindowInfo func(option, relativeTo uint32) uintptr
	cgRectFromDictionary       func(dict uintptr, out *cgRect) bool
	cgWindowListCreateImage    func(bounds cgRect, listOption, window, imageOption uint32) uintptr
	cgImageRelease             func(img uintptr)
	cgColorSpaceCreateRGB      func() uintptr
	cgColorSpaceRelease        func(space uintptr)
	cgBitmapContextCreate      func(data unsafe.Pointer, w, h, bitsPerComponent, bytesPerRow uint, space uintptr, info uint32) uintptr
	cgContextDrawImage         func(ctx uintptr, r cgRect, img uintptr)
	cgContextRelease           func(ctx uintptr)
	cgPreflightScreenCapture   func() bool
	cgRequestScreenCapture     func() bool

	keyNumber, keyLayer, keyOwnerName, keyName, keyBounds, keyOwnerPID uintptr

	clsRunningApp, clsPool objc.Class
	selAppWithPID          = objc.RegisterName("runningApplicationWithProcessIdentifier:")
	selActivate            = objc.RegisterName("activateWithOptions:")
	selAlloc               = objc.RegisterName("alloc")
	selInit                = objc.RegisterName("init")
	selDrain               = objc.RegisterName("drain")

	askedOnce sync.Once
)

const (
	listOnScreenOnly   = 1 << 0
	listAboveWindow    = 1 << 1
	listIncludingOne   = 1 << 3
	listNoDesktop      = 1 << 4
	imageIgnoreFraming = 1 << 0 // without the shadow
	imageNominalRes    = 1 << 4 // one pixel per point, also on Retina displays
	numberSInt64       = 4
	encodingUTF8       = 0x08000100
	alphaNoneSkipLast  = 5 // RGBX in memory
	activateIgnoring   = 1 << 1
	maxTitle           = 200
	minListedSize      = 40
	maxWindowPixels    = 8192
)

const macPermissionHelp = `macOS has not allowed edgehop to record the screen yet, so it cannot show this computer's windows.

Open System Settings > Privacy & Security > Screen & System Audio Recording
and enable the program you start edgehop from (Terminal, iTerm, ... or edgehop
itself), then start edgehop again.`

func macInit() error {
	macOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				macErr = fmt.Errorf("cannot load the macOS window functions: %v", r)
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
		purego.RegisterLibFunc(&cfArrayGetCount, cf, "CFArrayGetCount")
		purego.RegisterLibFunc(&cfArrayGetValueAtIndex, cf, "CFArrayGetValueAtIndex")
		purego.RegisterLibFunc(&cfDictionaryGetValue, cf, "CFDictionaryGetValue")
		purego.RegisterLibFunc(&cfNumberGetValue, cf, "CFNumberGetValue")
		purego.RegisterLibFunc(&cfStringGetCString, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&cfStringGetLength, cf, "CFStringGetLength")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")

		purego.RegisterLibFunc(&cgWindowListCopyWindowInfo, cg, "CGWindowListCopyWindowInfo")
		purego.RegisterLibFunc(&cgRectFromDictionary, cg, "CGRectMakeWithDictionaryRepresentation")
		purego.RegisterLibFunc(&cgImageRelease, cg, "CGImageRelease")
		purego.RegisterLibFunc(&cgColorSpaceCreateRGB, cg, "CGColorSpaceCreateDeviceRGB")
		purego.RegisterLibFunc(&cgColorSpaceRelease, cg, "CGColorSpaceRelease")
		purego.RegisterLibFunc(&cgBitmapContextCreate, cg, "CGBitmapContextCreate")
		purego.RegisterLibFunc(&cgContextDrawImage, cg, "CGContextDrawImage")
		purego.RegisterLibFunc(&cgContextRelease, cg, "CGContextRelease")

		optional := func(fptr any, name string) {
			if addr, err := purego.Dlsym(cg, name); err == nil && addr != 0 {
				purego.RegisterFunc(fptr, addr)
			}
		}
		// Apple steers programs towards ScreenCaptureKit; on a system where
		// the older function is gone, the feature is simply not available.
		optional(&cgWindowListCreateImage, "CGWindowListCreateImage")
		optional(&cgPreflightScreenCapture, "CGPreflightScreenCaptureAccess")
		optional(&cgRequestScreenCapture, "CGRequestScreenCaptureAccess")

		deref := func(name string) uintptr {
			addr, err := purego.Dlsym(cg, name)
			if err != nil || addr == 0 {
				return 0
			}
			return *(*uintptr)(*(*unsafe.Pointer)(unsafe.Pointer(&addr)))
		}
		keyNumber, keyLayer = deref("kCGWindowNumber"), deref("kCGWindowLayer")
		keyOwnerName, keyName = deref("kCGWindowOwnerName"), deref("kCGWindowName")
		keyBounds, keyOwnerPID = deref("kCGWindowBounds"), deref("kCGWindowOwnerPID")
		if keyNumber == 0 || keyLayer == 0 || keyBounds == 0 || keyOwnerPID == 0 {
			macErr = errors.New("the macOS window list is not available")
			return
		}

		// AppKit, to bring a program to the front.
		if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL); err == nil {
			clsRunningApp = objc.GetClass("NSRunningApplication")
			clsPool = objc.GetClass("NSAutoreleasePool")
		}
	})
	return macErr
}

// Supported reports whether windows can be listed and captured here.
func Supported() error {
	if err := macInit(); err != nil {
		return err
	}
	if cgWindowListCreateImage == nil {
		return errors.New("this macOS version no longer offers the function edgehop uses to take pictures of windows")
	}
	return nil
}

// allowed reports whether macOS lets this program record the screen, and
// asks the user for it once if not.
func allowed() bool {
	if cgPreflightScreenCapture == nil || cgPreflightScreenCapture() {
		return true
	}
	askedOnce.Do(func() {
		if cgRequestScreenCapture != nil {
			cgRequestScreenCapture()
		}
	})
	return cgPreflightScreenCapture()
}

func number(dict, key uintptr) (int64, bool) {
	v := cfDictionaryGetValue(dict, key)
	if v == 0 {
		return 0, false
	}
	var n int64
	if !cfNumberGetValue(v, numberSInt64, unsafe.Pointer(&n)) {
		return 0, false
	}
	return n, true
}

func text(dict, key uintptr) string {
	if key == 0 {
		return ""
	}
	v := cfDictionaryGetValue(dict, key)
	if v == 0 {
		return ""
	}
	n := cfStringGetLength(v)
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n*4+1)
	if !cfStringGetCString(v, &buf[0], len(buf), encodingUTF8) {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			buf = buf[:i]
			break
		}
	}
	s := string(buf)
	if r := []rune(s); len(r) > maxTitle {
		s = string(r[:maxTitle])
	}
	return s
}

type macWindow struct {
	id     uint32
	pid    int32
	layer  int64
	bounds cgRect
	owner  string
	name   string
}

// windows reads the window list: every on-screen window from front to back,
// or just the one with the given id.
func windows(option, id uint32) []macWindow {
	arr := cgWindowListCopyWindowInfo(option, id)
	if arr == 0 {
		return nil
	}
	defer cfRelease(arr)
	n := cfArrayGetCount(arr)
	out := make([]macWindow, 0, n)
	for i := 0; i < n; i++ {
		d := cfArrayGetValueAtIndex(arr, i)
		if d == 0 {
			continue
		}
		var w macWindow
		num, ok := number(d, keyNumber)
		if !ok {
			continue
		}
		w.id = uint32(num)
		w.layer, _ = number(d, keyLayer)
		pid, _ := number(d, keyOwnerPID)
		w.pid = int32(pid)
		if b := cfDictionaryGetValue(d, keyBounds); b == 0 || !cgRectFromDictionary(b, &w.bounds) {
			continue
		}
		w.owner, w.name = text(d, keyOwnerName), text(d, keyName)
		out = append(out, w)
	}
	return out
}

func (w macWindow) title() string {
	if w.name != "" {
		return w.name
	}
	return w.owner
}

// List returns the windows that can be shown remotely: ordinary windows that
// are on the screen right now.
func (System) List() ([]Window, error) {
	if err := Supported(); err != nil {
		return nil, err
	}
	if !allowed() {
		return nil, errors.New(macPermissionHelp)
	}
	self := int32(os.Getpid())
	var out []Window
	for _, w := range windows(listOnScreenOnly|listNoDesktop, 0) {
		if w.layer != 0 || w.pid == self || w.owner == "" {
			continue
		}
		if w.bounds.W < minListedSize || w.bounds.H < minListedSize {
			continue
		}
		if w.name == "" && (w.bounds.W < 120 || w.bounds.H < 120) {
			continue // helper windows without a name
		}
		out = append(out, Window{
			ID: uint64(w.id), Title: w.title(), App: w.owner,
			W: int(math.Round(w.bounds.W)), H: int(math.Round(w.bounds.H)),
		})
	}
	return out, nil
}

type macCapturer struct {
	id    uint32
	pid   int32
	space uintptr
}

// Open prepares capturing the window with the given id.
func (System) Open(id uint64) (Capturer, error) {
	if err := Supported(); err != nil {
		return nil, err
	}
	if !allowed() {
		return nil, errors.New(macPermissionHelp)
	}
	one := windows(listIncludingOne, uint32(id))
	if len(one) == 0 {
		return nil, ErrGone
	}
	return &macCapturer{id: uint32(id), pid: one[0].pid, space: cgColorSpaceCreateRGB()}, nil
}

func (c *macCapturer) Close() {
	if c.space != 0 {
		cgColorSpaceRelease(c.space)
		c.space = 0
	}
}

// origin is the top-left corner of the whole desktop: input.Injector counts
// from there.
func origin() (float64, float64) {
	mons := input.Monitors()
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
	return float64(x), float64(y)
}

func (c *macCapturer) Capture(f *Frame) error {
	one := windows(listIncludingOne, c.id)
	if len(one) == 0 {
		return ErrGone
	}
	b := one[0].bounds
	w, h := int(math.Round(b.W)), int(math.Round(b.H))
	if w <= 0 || h <= 0 || w > maxWindowPixels || h > maxWindowPixels {
		return fmt.Errorf("the window has an unusable size (%dx%d)", w, h)
	}
	// A window in use is taken together with whatever is above it: its
	// menus, pop-ups and sheets are windows of their own. A window in the
	// background is taken alone, so it shows even while others cover it.
	area, option := cgRect{math.Inf(1), math.Inf(1), 0, 0}, uint32(listIncludingOne) // CGRectNull: just the window
	if c.Front() {
		area, option = b, listIncludingOne|listAboveWindow
	}
	img := cgWindowListCreateImage(area, option, c.id, imageIgnoreFraming|imageNominalRes)
	if img == 0 {
		if len(windows(listIncludingOne, c.id)) == 0 {
			return ErrGone
		}
		return errors.New("macOS returned no picture of the window")
	}
	defer cgImageRelease(img)

	f.Pix = grow(f.Pix, w*h*4)
	clear(f.Pix)
	ctx := cgBitmapContextCreate(unsafe.Pointer(&f.Pix[0]), uint(w), uint(h), 8, uint(w*4), c.space, alphaNoneSkipLast)
	if ctx == 0 {
		return errors.New("cannot create a bitmap for the window's picture")
	}
	cgContextDrawImage(ctx, cgRect{0, 0, float64(w), float64(h)}, img)
	cgContextRelease(ctx)
	for i := 3; i < len(f.Pix); i += 4 {
		f.Pix[i] = 0xFF
	}
	ox, oy := origin()
	f.W, f.H = w, h
	f.X, f.Y = int(math.Round(b.X-ox)), int(math.Round(b.Y-oy))
	f.Title = one[0].title()
	return nil
}

// Front reports whether the user works in this window's program: the
// frontmost ordinary window is this one or, say, one of its dialogs.
func (c *macCapturer) Front() bool {
	for _, w := range windows(listOnScreenOnly|listNoDesktop, 0) {
		if w.layer == 0 {
			return w.id == c.id || w.pid == c.pid
		}
	}
	return false
}

// Raise brings the window's program to the front. macOS offers no simple
// way to raise one particular window of another program, so a window hidden
// behind another window of the same program stays there.
func (c *macCapturer) Raise() {
	if clsRunningApp == 0 || clsPool == 0 {
		return
	}
	pool := objc.ID(clsPool).Send(selAlloc).Send(selInit)
	defer pool.Send(selDrain)
	app := objc.ID(clsRunningApp).Send(selAppWithPID, c.pid)
	if app != 0 {
		app.Send(selActivate, activateIgnoring)
	}
}

// Resize and Hide are not available on macOS yet.
func (c *macCapturer) Resize(w, h int) {}
func (c *macCapturer) Hide(on bool)    {}

// RestoreHidden has nothing to do on this system.
func RestoreHidden() int { return 0 }

// Dragged returns the window the user is moving with the mouse right now.
// This system offers no way to tell; Frontmost is the next best thing, as a
// window that is being moved is in front of the others.
func (System) Dragged() (Window, bool) { return Window{}, false }

// Frontmost returns the window the user works in right now, if it is one
// that can be shown remotely: the list comes from front to back.
func (s System) Frontmost() (Window, bool) {
	wins, err := s.List()
	if err != nil || len(wins) == 0 {
		return Window{}, false
	}
	return wins[0], true
}
