//go:build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"edgehop/internal/keys"
	"edgehop/internal/layout"
)

// Linux backend.
//
// Everything goes through the kernel's evdev and uinput interfaces, so it
// behaves the same under X11 and Wayland (which offers no portable way to
// read the cursor position or to inject input).
//
// Injection creates two virtual devices: a keyboard and an absolute pointer
// (like the tablet device of a virtual machine), whose coordinates map onto
// the whole desktop.
//
// Capture takes exclusive hold of the physical mice and keyboards and feeds
// their events back through the same kind of virtual devices while the
// cursor is on this computer. Because edgehop positions the cursor itself, it
// always knows where it is - that is what makes edge detection possible on
// Wayland. The price is that pointer speed on a Linux *server* is set by
// edgehop (pointer_speed / pointer_accel) rather than by the desktop settings.

const (
	evSyn = 0x00
	evKey = 0x01
	evRel = 0x02
	evAbs = 0x03

	relX           = 0x00
	relY           = 0x01
	relHWheel      = 0x06
	relWheel       = 0x08
	relWheelHiRes  = 0x0b
	relHWheelHiRes = 0x0c

	absX = 0x00
	absY = 0x01

	btnLeft    = 0x110
	btnRight   = 0x111
	btnMiddle  = 0x112
	btnSide    = 0x113
	btnExtra   = 0x114
	btnForward = 0x115
	btnBack    = 0x116

	keyBitsLen = 96 // bytes, enough for KEY_MAX (0x2ff)

	uiSetEvBit   = 0x40045564
	uiSetKeyBit  = 0x40045565
	uiSetRelBit  = 0x40045566
	uiSetAbsBit  = 0x40045567
	uiDevCreate  = 0x5501
	uiDevDestroy = 0x5502
	eviocGrab    = 0x40044590

	busVirtual    = 0x06
	virtualPrefix = "edgehop virtual"
)

// eviocRead builds an EVIOCG* request number (_IOC(_IOC_READ, 'E', nr, size)).
func eviocRead(nr, size int) uintptr {
	return 2<<30 | uintptr(size)<<16 | 'E'<<8 | uintptr(nr)
}

func ioctl(fd int, req, arg uintptr) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, arg); errno != 0 {
		return errno
	}
	return nil
}

type inputEvent struct {
	Time  syscall.Timeval
	Type  uint16
	Code  uint16
	Value int32
}

const evSize = int(unsafe.Sizeof(inputEvent{}))

// ---------------------------------------------------------------------------
// uinput: virtual devices

type uinputUserDev struct {
	Name         [80]byte
	Bustype      uint16
	Vendor       uint16
	Product      uint16
	Version      uint16
	FFEffectsMax uint32
	Absmax       [64]int32
	Absmin       [64]int32
	Absfuzz      [64]int32
	Absflat      [64]int32
}

type uinputDev struct {
	mu sync.Mutex
	fd int
}

const accessHelp = `edgehop needs access to /dev/uinput and /dev/input/event*.
Either run it with sudo, or give your user access once:

  sudo usermod -aG input $USER
  echo 'KERNEL=="uinput", GROUP="input", MODE="0660", OPTIONS+="static_node=uinput"' | sudo tee /etc/udev/rules.d/60-edgehop.rules
  sudo udevadm control --reload-rules && sudo udevadm trigger

then log out and back in.`

func createUinput(name string, product uint16, keyBits, relBits []int, absMax map[int]int32) (*uinputDev, error) {
	fd, err := syscall.Open("/dev/uinput", syscall.O_WRONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		switch {
		case errors.Is(err, syscall.ENOENT):
			return nil, errors.New("/dev/uinput does not exist - load the kernel module with: sudo modprobe uinput")
		case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
			return nil, fmt.Errorf("no permission to open /dev/uinput\n\n%s", accessHelp)
		}
		return nil, fmt.Errorf("cannot open /dev/uinput: %w", err)
	}
	fail := func(err error) (*uinputDev, error) {
		syscall.Close(fd)
		return nil, fmt.Errorf("cannot create the virtual input device %q: %w", name, err)
	}
	setBits := func(evType int, req uintptr, bits []int) error {
		if len(bits) == 0 {
			return nil
		}
		if err := ioctl(fd, uiSetEvBit, uintptr(evType)); err != nil {
			return err
		}
		for _, b := range bits {
			if err := ioctl(fd, req, uintptr(b)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := setBits(evKey, uiSetKeyBit, keyBits); err != nil {
		return fail(err)
	}
	if err := setBits(evRel, uiSetRelBit, relBits); err != nil {
		return fail(err)
	}
	var ud uinputUserDev
	copy(ud.Name[:len(ud.Name)-1], name)
	ud.Bustype = busVirtual
	ud.Vendor = 0xED6E
	ud.Product = product
	ud.Version = 1
	if len(absMax) > 0 {
		axes := make([]int, 0, len(absMax))
		for axis, maxVal := range absMax {
			axes = append(axes, axis)
			ud.Absmax[axis] = maxVal
		}
		if err := setBits(evAbs, uiSetAbsBit, axes); err != nil {
			return fail(err)
		}
	}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(&ud)), int(unsafe.Sizeof(ud)))
	if _, err := syscall.Write(fd, raw); err != nil {
		return fail(err)
	}
	if err := ioctl(fd, uiDevCreate, 0); err != nil {
		return fail(err)
	}
	return &uinputDev{fd: fd}, nil
}

func newVirtualKeyboard() (*uinputDev, error) {
	bits := make([]int, 0, 248)
	for k := 1; k <= 248; k++ {
		bits = append(bits, k)
	}
	return createUinput(virtualPrefix+" keyboard", 1, bits, nil, nil)
}

func newVirtualPointer(w, h int) (*uinputDev, error) {
	return createUinput(virtualPrefix+" pointer", 2,
		[]int{btnLeft, btnRight, btnMiddle, btnSide, btnExtra},
		[]int{relWheel, relHWheel, relWheelHiRes, relHWheelHiRes},
		map[int]int32{absX: int32(w - 1), absY: int32(h - 1)})
}

// write sends the events followed by a SYN_REPORT.
func (d *uinputDev) write(evs ...inputEvent) {
	var arr [8]inputEvent
	n := copy(arr[:len(arr)-1], evs)
	arr[n] = inputEvent{Type: evSyn}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(&arr[0])), (n+1)*evSize)
	d.mu.Lock()
	syscall.Write(d.fd, raw)
	d.mu.Unlock()
}

func (d *uinputDev) close() {
	if d == nil {
		return
	}
	ioctl(d.fd, uiDevDestroy, 0)
	syscall.Close(d.fd)
}

func buttonToEvdev(b uint8) uint16 {
	switch b {
	case keys.ButtonLeft:
		return btnLeft
	case keys.ButtonRight:
		return btnRight
	case keys.ButtonMiddle:
		return btnMiddle
	case keys.ButtonBack:
		return btnSide
	case keys.ButtonForward:
		return btnExtra
	}
	return 0
}

func evdevToButton(code uint16) uint16 {
	switch code {
	case btnLeft:
		return keys.ButtonLeft
	case btnRight:
		return keys.ButtonRight
	case btnMiddle:
		return keys.ButtonMiddle
	case btnSide, btnBack:
		return keys.ButtonBack
	case btnExtra, btnForward:
		return keys.ButtonForward
	}
	return 0
}

// wheelEvents turns a scroll amount in 1/120 notches into the high
// resolution event plus, when a whole notch has accumulated, the classic one.
func wheelEvents(evs []inputEvent, delta int, rem *int, hiRes, legacy uint16) []inputEvent {
	if delta == 0 {
		return evs
	}
	evs = append(evs, inputEvent{Type: evRel, Code: hiRes, Value: int32(delta)})
	*rem += delta
	if notches := *rem / 120; notches != 0 {
		*rem -= notches * 120
		evs = append(evs, inputEvent{Type: evRel, Code: legacy, Value: int32(notches)})
	}
	return evs
}

// ---------------------------------------------------------------------------
// Desktop size

var (
	xrandrRe = regexp.MustCompile(`current\s+(\d+)\s*x\s*(\d+)`)
	modeRe   = regexp.MustCompile(`^(\d+)x(\d+)`)
)

func detectSize(opts Options) (w, h int, source string) {
	if opts.Width > 0 && opts.Height > 0 {
		return opts.Width, opts.Height, "screen_size in the config"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "xrandr", "--current").Output(); err == nil {
		if m := xrandrRe.FindSubmatch(out); m != nil {
			w, _ = strconv.Atoi(string(m[1]))
			h, _ = strconv.Atoi(string(m[2]))
			if w >= 100 && h >= 100 {
				return w, h, "xrandr"
			}
		}
	}
	// Kernel mode setting: assume connected monitors stand side by side.
	statuses, _ := filepath.Glob("/sys/class/drm/card*-*/status")
	w, h = 0, 0
	for _, st := range statuses {
		data, err := os.ReadFile(st)
		if err != nil || strings.TrimSpace(string(data)) != "connected" {
			continue
		}
		modes, err := os.ReadFile(filepath.Join(filepath.Dir(st), "modes"))
		if err != nil {
			continue
		}
		if m := modeRe.FindSubmatch(modes); m != nil {
			mw, _ := strconv.Atoi(string(m[1]))
			mh, _ := strconv.Atoi(string(m[2]))
			w += mw
			h = max(h, mh)
		}
	}
	if w >= 100 && h >= 100 {
		return w, h, "kernel display modes"
	}
	return 1920, 1080, "a guess - set screen_size in the config"
}

// Monitors returns the desktop as one rectangle; Linux gives no portable
// per-monitor geometry.
func Monitors() []layout.Rect {
	w, h, _ := detectSize(Options{})
	return []layout.Rect{{X: 0, Y: 0, W: w, H: h}}
}

// SelfTest is only implemented on Windows.
func SelfTest(out io.Writer, grab bool) error {
	return errors.New("the self test is only available on Windows")
}

// ---------------------------------------------------------------------------
// Injection

type linuxInjector struct {
	mu       sync.Mutex
	kbd, ptr *uinputDev
	w, h     int
	source   string
	down     [keys.MaxCode]bool
	buttons  uint32
	remX     int
	remY     int
}

// NewInjector creates the virtual keyboard and pointer.
func NewInjector(opts Options) (Injector, error) {
	w, h, source := detectSize(opts)
	kbd, err := newVirtualKeyboard()
	if err != nil {
		return nil, err
	}
	ptrDev, err := newVirtualPointer(w, h)
	if err != nil {
		kbd.close()
		return nil, err
	}
	return &linuxInjector{kbd: kbd, ptr: ptrDev, w: w, h: h, source: source}, nil
}

func (l *linuxInjector) Size() (int, int) { return l.w, l.h }

func (l *linuxInjector) Describe() string {
	return fmt.Sprintf("Linux uinput virtual devices; desktop %dx%d from %s", l.w, l.h, l.source)
}

func (l *linuxInjector) Close() {
	l.ReleaseAll()
	l.kbd.close()
	l.ptr.close()
}

func (l *linuxInjector) MoveAbs(x, y int) {
	x = min(max(x, 0), l.w-1)
	y = min(max(y, 0), l.h-1)
	l.ptr.write(
		inputEvent{Type: evAbs, Code: absX, Value: int32(x)},
		inputEvent{Type: evAbs, Code: absY, Value: int32(y)})
}

func (l *linuxInjector) Button(b uint8, down bool) {
	code := buttonToEvdev(b)
	if code == 0 {
		return
	}
	l.mu.Lock()
	if down {
		l.buttons |= 1 << b
	} else {
		l.buttons &^= 1 << b
	}
	l.mu.Unlock()
	v := int32(0)
	if down {
		v = 1
	}
	l.ptr.write(inputEvent{Type: evKey, Code: code, Value: v})
}

func (l *linuxInjector) Wheel(dx, dy int) {
	l.mu.Lock()
	var buf [4]inputEvent
	evs := wheelEvents(buf[:0], dy, &l.remY, relWheelHiRes, relWheel)
	evs = wheelEvents(evs, dx, &l.remX, relHWheelHiRes, relHWheel)
	l.mu.Unlock()
	if len(evs) > 0 {
		l.ptr.write(evs...)
	}
}

func (l *linuxInjector) Key(code uint16, state uint8) {
	if code == 0 || code >= keys.MaxCode || state == keys.Repeat {
		return // the desktop generates key repeat by itself
	}
	l.mu.Lock()
	l.down[code] = state != keys.Up
	l.mu.Unlock()
	l.kbd.write(inputEvent{Type: evKey, Code: code, Value: int32(state)})
}

func (l *linuxInjector) ReleaseAll() {
	l.mu.Lock()
	var codes []uint16
	for code, d := range l.down {
		if d {
			codes = append(codes, uint16(code))
		}
	}
	buttons := l.buttons
	l.mu.Unlock()
	for _, code := range codes {
		l.Key(code, keys.Up)
	}
	for b := uint8(1); b <= keys.ButtonForward; b++ {
		if buttons&(1<<b) != 0 {
			l.Button(b, false)
		}
	}
}

// ---------------------------------------------------------------------------
// Capture

type evDevice struct {
	path  string
	name  string
	fd    int
	hires bool // reports high resolution wheel events
}

type linuxCapture struct {
	events chan Event
	closed chan struct{}
	source string
	speed  float64
	accel  float64
	w, h   int

	kbd, ptr *uinputDev

	mu           sync.Mutex
	devs         map[string]*evDevice
	remote       bool
	armed        uint8
	x, y         float64
	sentX, sentY int
	buttons      uint32
	localButtons uint32
	kt           keyTracker
	remX, remY   int
}

// NewCapture grabs the physical mice and keyboards.
func NewCapture(opts Options) (Capture, error) {
	w, h, source := detectSize(opts)
	c := &linuxCapture{
		events: make(chan Event, 4096),
		closed: make(chan struct{}),
		source: source,
		speed:  opts.PointerSpeed,
		accel:  opts.PointerAccel,
		w:      w, h: h,
		x: float64(w) / 2, y: float64(h) / 2,
		sentX: -1, sentY: -1,
		devs: map[string]*evDevice{},
	}
	if c.speed <= 0 {
		c.speed = 1
	}
	if c.accel < 0 {
		c.accel = 0
	}
	var err error
	if c.kbd, err = newVirtualKeyboard(); err != nil {
		return nil, err
	}
	if c.ptr, err = newVirtualPointer(w, h); err != nil {
		c.kbd.close()
		return nil, err
	}
	// Give the desktop a moment to notice the virtual devices before any
	// physical device is taken away from it.
	time.Sleep(300 * time.Millisecond)
	n, denied := c.scan()
	if n == 0 {
		c.kbd.close()
		c.ptr.close()
		if denied {
			return nil, fmt.Errorf("no permission to read the input devices in /dev/input\n\n%s", accessHelp)
		}
		return nil, errors.New("no mouse or keyboard found in /dev/input")
	}
	c.mu.Lock()
	c.syncPointer()
	c.mu.Unlock()
	go c.rescanLoop()
	return c, nil
}

func getBits(fd, evType, size int) []byte {
	buf := make([]byte, size)
	if err := ioctl(fd, eviocRead(0x20+evType, size), uintptr(unsafe.Pointer(&buf[0]))); err != nil {
		return nil
	}
	return buf
}

func hasBit(bits []byte, n int) bool {
	return n/8 < len(bits) && bits[n/8]&(1<<(n%8)) != 0
}

// openDevice opens and grabs an input device if it is a mouse or a keyboard.
// It returns nil, nil for devices edgehop is not interested in.
func openDevice(path string) (*evDevice, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	nameBuf := make([]byte, 256)
	ioctl(fd, eviocRead(0x06, len(nameBuf)), uintptr(unsafe.Pointer(&nameBuf[0])))
	name := strings.TrimRight(string(nameBuf), "\x00")
	keyBits := getBits(fd, evKey, keyBitsLen)
	relBits := getBits(fd, evRel, 2)
	isMouse := hasBit(relBits, relX) && hasBit(relBits, relY) && hasBit(keyBits, btnLeft)
	isKeyboard := hasBit(keyBits, keys.A) && hasBit(keyBits, keys.Space) && hasBit(keyBits, keys.Enter)
	if strings.HasPrefix(name, virtualPrefix) || (!isMouse && !isKeyboard) {
		syscall.Close(fd)
		return nil, nil
	}
	// Never grab a device while one of its keys is held down (for example the
	// Enter that started this program): the desktop would never see the
	// release and the key would repeat forever.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for {
		state := make([]byte, keyBitsLen)
		ioctl(fd, eviocRead(0x18, len(state)), uintptr(unsafe.Pointer(&state[0])))
		held := false
		for _, b := range state {
			if b != 0 {
				held = true
				break
			}
		}
		if !held {
			break
		}
		if time.Now().After(deadline) {
			syscall.Close(fd)
			return nil, nil // try again at the next scan
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := ioctl(fd, eviocGrab, 1); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("cannot grab %s (%s): %w", path, name, err)
	}
	return &evDevice{path: path, name: name, fd: fd, hires: hasBit(relBits, relWheelHiRes)}, nil
}

// scan picks up devices that are not grabbed yet (also used for hot-plug:
// a Bluetooth mouse shows up as a new device every time it reconnects).
func (c *linuxCapture) scan() (grabbed int, denied bool) {
	paths, _ := filepath.Glob("/dev/input/event*")
	for _, p := range paths {
		c.mu.Lock()
		_, have := c.devs[p]
		c.mu.Unlock()
		if have {
			continue
		}
		d, err := openDevice(p)
		if err != nil {
			if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
				denied = true
			}
			continue
		}
		if d == nil {
			continue
		}
		c.mu.Lock()
		c.devs[p] = d
		c.mu.Unlock()
		go c.read(d)
	}
	c.mu.Lock()
	grabbed = len(c.devs)
	c.mu.Unlock()
	return grabbed, denied
}

func (c *linuxCapture) rescanLoop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			c.scan()
		}
	}
}

func (c *linuxCapture) read(d *evDevice) {
	buf := make([]byte, evSize*64)
	var dx, dy, wx, wy int32
	flush := func() {
		if dx != 0 || dy != 0 {
			c.motion(dx, dy)
		}
		if wx != 0 || wy != 0 {
			c.wheel(wx, wy)
		}
		dx, dy, wx, wy = 0, 0, 0, 0
	}
	for {
		n, err := syscall.Read(d.fd, buf)
		if err == syscall.EINTR {
			continue
		}
		if err != nil || n <= 0 {
			break // unplugged, or the capture was closed
		}
		for off := 0; off+evSize <= n; off += evSize {
			ev := (*inputEvent)(unsafe.Pointer(&buf[off]))
			switch ev.Type {
			case evRel:
				switch ev.Code {
				case relX:
					dx += ev.Value
				case relY:
					dy += ev.Value
				case relWheel:
					if !d.hires {
						wy += ev.Value * 120
					}
				case relHWheel:
					if !d.hires {
						wx += ev.Value * 120
					}
				case relWheelHiRes:
					wy += ev.Value
				case relHWheelHiRes:
					wx += ev.Value
				}
			case evKey:
				flush() // keep clicks in order with the motion before them
				c.keyOrButton(ev.Code, ev.Value)
			case evSyn:
				flush()
			}
		}
	}
	c.mu.Lock()
	if c.devs[d.path] == d {
		delete(c.devs, d.path)
	}
	c.mu.Unlock()
	syscall.Close(d.fd)
}

// syncPointer moves the desktop cursor to c.x, c.y. Callers hold c.mu.
func (c *linuxCapture) syncPointer() {
	x, y := int(math.Round(c.x)), int(math.Round(c.y))
	if x == c.sentX && y == c.sentY {
		return
	}
	c.sentX, c.sentY = x, y
	c.ptr.write(
		inputEvent{Type: evAbs, Code: absX, Value: int32(x)},
		inputEvent{Type: evAbs, Code: absY, Value: int32(y)})
}

func (c *linuxCapture) motion(dx, dy int32) {
	// A simple acceleration curve: slow movements are 1:1, fast ones are
	// amplified by up to (1 + accel).
	fx, fy := float64(dx), float64(dy)
	gain := c.speed * (1 + c.accel*math.Min(math.Hypot(fx, fy), 30)/30)
	fx *= gain
	fy *= gain

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remote {
		emit(c.events, Event{Kind: EvMotion, DX: fx, DY: fy})
		return
	}
	nx, ny := c.x+fx, c.y+fy
	maxX, maxY := float64(c.w-1), float64(c.h-1)
	if c.buttons == 0 && c.armed != 0 {
		var mask uint8
		if nx < 0 {
			mask |= layout.Left.Bit()
		}
		if nx > maxX {
			mask |= layout.Right.Bit()
		}
		if ny < 0 {
			mask |= layout.Top.Bit()
		}
		if ny > maxY {
			mask |= layout.Bottom.Bit()
		}
		if side, ok := layout.FirstSide(mask & c.armed); ok {
			frac := 0.5
			if side == layout.Left || side == layout.Right {
				if maxY > 0 {
					frac = math.Max(0, math.Min(ny, maxY)) / maxY
				}
			} else if maxX > 0 {
				frac = math.Max(0, math.Min(nx, maxX)) / maxX
			}
			emit(c.events, Event{Kind: EvEdge, Side: side, Frac: frac})
		}
	}
	c.x = math.Max(0, math.Min(nx, maxX))
	c.y = math.Max(0, math.Min(ny, maxY))
	c.syncPointer()
}

func (c *linuxCapture) wheel(wx, wy int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remote {
		emit(c.events, Event{Kind: EvWheel, WX: wx, WY: wy})
		return
	}
	var buf [4]inputEvent
	evs := wheelEvents(buf[:0], int(wy), &c.remY, relWheelHiRes, relWheel)
	evs = wheelEvents(evs, int(wx), &c.remX, relHWheelHiRes, relHWheel)
	if len(evs) > 0 {
		c.ptr.write(evs...)
	}
}

func (c *linuxCapture) keyOrButton(code uint16, value int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if code >= 0x100 {
		b := evdevToButton(code)
		if b == 0 || value == 2 {
			return
		}
		down := value != 0
		bit := uint32(1) << b
		if down {
			c.buttons |= bit
		} else {
			c.buttons &^= bit
		}
		local := !c.remote
		if !c.remote {
			if down {
				c.localButtons |= bit
			} else {
				c.localButtons &^= bit
			}
		} else if !down && c.localButtons&bit != 0 {
			c.localButtons &^= bit
			local = true // pressed locally before the switch: release it locally
		}
		if local {
			c.ptr.write(inputEvent{Type: evKey, Code: buttonToEvdev(uint8(b)), Value: value})
			return
		}
		emit(c.events, Event{Kind: EvButton, Code: b, State: uint8(value)})
		return
	}
	if value == 2 && !c.remote {
		return // kernel auto-repeat; the desktop repeats keys by itself
	}
	forward, state, swallow := c.kt.onKey(c.remote, code, value != 0)
	if forward {
		emit(c.events, Event{Kind: EvKey, Code: code, State: state})
	}
	if !swallow && value != 2 {
		c.kbd.write(inputEvent{Type: evKey, Code: code, Value: value})
	}
}

func (c *linuxCapture) Events() <-chan Event { return c.events }

func (c *linuxCapture) Grab() {
	c.mu.Lock()
	c.remote = true
	c.mu.Unlock()
}

func (c *linuxCapture) Release(side layout.Side, frac float64, warp bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.remote = false
	c.kt.clearRemote()
	if warp {
		x, y := layout.EntryPoint(c.w, c.h, side, frac, 2)
		c.x, c.y = float64(x), float64(y)
		c.syncPointer()
	}
}

func (c *linuxCapture) SetArmedEdges(mask uint8) {
	c.mu.Lock()
	c.armed = mask
	c.mu.Unlock()
}

func (c *linuxCapture) Size() (int, int) { return c.w, c.h }

func (c *linuxCapture) Describe() string {
	c.mu.Lock()
	names := make([]string, 0, len(c.devs))
	for _, d := range c.devs {
		names = append(names, d.name)
	}
	c.mu.Unlock()
	return fmt.Sprintf("Linux evdev + uinput; desktop %dx%d from %s; devices: %s",
		c.w, c.h, c.source, strings.Join(names, ", "))
}

func (c *linuxCapture) Close() {
	select {
	case <-c.closed:
		return
	default:
		close(c.closed)
	}
	c.mu.Lock()
	for _, d := range c.devs {
		ioctl(d.fd, eviocGrab, 0) // hand the device back to the desktop
	}
	c.mu.Unlock()
	c.kbd.close()
	c.ptr.close()
}
