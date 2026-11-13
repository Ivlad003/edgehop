package server_test

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivlad003/edgehop/internal/client"
	"github.com/Ivlad003/edgehop/internal/input"
	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
	"github.com/Ivlad003/edgehop/internal/logx"
	"github.com/Ivlad003/edgehop/internal/server"
)

func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Verbose() {
		logx.SetOutput(io.Discard)
	} else {
		logx.SetVerbose(true)
	}
	os.Exit(m.Run())
}

// fakeCapture stands in for the physical mouse and keyboard of the server.
type fakeCapture struct {
	ch chan input.Event

	mu       sync.Mutex
	grabs    int
	releases []string
	armed    uint8
}

func newFakeCapture() *fakeCapture { return &fakeCapture{ch: make(chan input.Event, 1024)} }

func (f *fakeCapture) Events() <-chan input.Event { return f.ch }
func (f *fakeCapture) Size() (int, int)           { return 1920, 1080 }
func (f *fakeCapture) Describe() string           { return "fake" }
func (f *fakeCapture) Close()                     {}
func (f *fakeCapture) Grab() {
	f.mu.Lock()
	f.grabs++
	f.mu.Unlock()
}
func (f *fakeCapture) Release(side layout.Side, frac float64, warp bool) {
	f.mu.Lock()
	if warp {
		f.releases = append(f.releases, fmt.Sprintf("warp %s %.2f", side, frac))
	} else {
		f.releases = append(f.releases, "nowarp")
	}
	f.mu.Unlock()
}
func (f *fakeCapture) SetArmedEdges(mask uint8) {
	f.mu.Lock()
	f.armed = mask
	f.mu.Unlock()
}
func (f *fakeCapture) state() (grabs int, releases []string, armed uint8) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.grabs, append([]string(nil), f.releases...), f.armed
}

func (f *fakeCapture) edge(side layout.Side, frac float64) {
	f.ch <- input.Event{Kind: input.EvEdge, Side: side, Frac: frac}
}
func (f *fakeCapture) move(dx, dy float64) {
	f.ch <- input.Event{Kind: input.EvMotion, DX: dx, DY: dy}
}
func (f *fakeCapture) key(code uint16, state uint8) {
	f.ch <- input.Event{Kind: input.EvKey, Code: code, State: state}
}
func (f *fakeCapture) button(b uint16, down bool) {
	st := uint8(0)
	if down {
		st = 1
	}
	f.ch <- input.Event{Kind: input.EvButton, Code: b, State: st}
}

// fakeInjector records what a client would do to its desktop.
type fakeInjector struct {
	w, h int

	mu     sync.Mutex
	x, y   int
	log    []string
	onMove func(x, y int)
}

func (f *fakeInjector) add(format string, args ...any) {
	f.log = append(f.log, fmt.Sprintf(format, args...))
}
func (f *fakeInjector) Size() (int, int) { return f.w, f.h }
func (f *fakeInjector) Describe() string { return "fake" }
func (f *fakeInjector) Close()           {}
func (f *fakeInjector) MoveAbs(x, y int) {
	f.mu.Lock()
	f.x, f.y = x, y
	cb := f.onMove
	f.mu.Unlock()
	if cb != nil {
		cb(x, y)
	}
}
func (f *fakeInjector) Button(b uint8, down bool) {
	f.mu.Lock()
	f.add("button %d %v at %d,%d", b, down, f.x, f.y)
	f.mu.Unlock()
}
func (f *fakeInjector) Wheel(dx, dy int) {
	f.mu.Lock()
	f.add("wheel %d %d", dx, dy)
	f.mu.Unlock()
}
func (f *fakeInjector) Key(code uint16, state uint8) {
	f.mu.Lock()
	f.add("key %s %d", keys.Name(code), state)
	f.mu.Unlock()
}
func (f *fakeInjector) ReleaseAll() {
	f.mu.Lock()
	f.add("releaseall")
	f.mu.Unlock()
}
func (f *fakeInjector) pos() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.x, f.y
}
func (f *fakeInjector) events() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.log, "; ")
}
func (f *fakeInjector) has(s string) bool { return strings.Contains(f.events(), s) }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

type rig struct {
	clients map[string]*client.Client
	t       *testing.T
	srv     *server.Server
	cap     *fakeCapture
	key     string
}

func startServer(t *testing.T, name string, row []string, screens map[string]server.ScreenOpts) *rig {
	t.Helper()
	return startServerWith(t, name, row, screens, false)
}

func startServerWith(t *testing.T, name string, row []string, screens map[string]server.ScreenOpts, autoScale bool, opts ...func(*server.Config)) *rig {
	t.Helper()
	l := layout.New()
	l.Add(name)
	l.AddRow(row)
	hk, err := keys.ParseHotkey("ctrl+alt+shift+backspace")
	if err != nil {
		t.Fatal(err)
	}
	fc := newFakeCapture()
	cfg := server.Config{
		Name: name, Listen: "127.0.0.1:0", Key: "test-key",
		Layout: l, Screens: screens, Hotkey: hk, AutoScale: autoScale,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	srv := server.New(cfg, fc)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Serve(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return &rig{t: t, srv: srv, cap: fc, key: "test-key", clients: map[string]*client.Client{}}
}

// connect starts a client and waits until the server sees it (with UDP up).
func (r *rig) connect(name string, w, h int) (*fakeInjector, context.CancelFunc) {
	r.t.Helper()
	return r.connectWith(name, w, h, nil)
}

// connectWith is connect with a hook to adjust the client configuration.
func (r *rig) connectWith(name string, w, h int, adjust func(*client.Config)) (*fakeInjector, context.CancelFunc) {
	r.t.Helper()
	inj := &fakeInjector{w: w, h: h}
	cc := client.Config{Name: name, Server: r.srv.Addr().String(), Key: r.key}
	if adjust != nil {
		adjust(&cc)
	}
	c := client.New(cc, inj)
	r.clients[name] = c
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	stop := func() {
		cancel()
		<-done
	}
	r.t.Cleanup(stop)
	eventually(r.t, name+" to connect", func() bool {
		for _, n := range r.srv.Connected() {
			if n == name {
				return r.srv.UDPReady(name)
			}
		}
		return false
	})
	return inj, stop
}

func (r *rig) waitFocus(want string) {
	r.t.Helper()
	eventually(r.t, fmt.Sprintf("focus = %q", want), func() bool { return r.srv.Focus() == want })
}

func waitPos(t *testing.T, inj *fakeInjector, x, y int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if gx, gy := inj.pos(); gx == x && gy == y {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	gx, gy := inj.pos()
	t.Fatalf("cursor at %d,%d, want %d,%d", gx, gy, x, y)
}

func TestCursorCrossesToClientAndBack(t *testing.T) {
	r := startServer(t, "desk", []string{"desk", "mac"}, nil)
	_, _, armed := r.cap.state()
	if armed != 0 {
		t.Fatalf("no edge may be armed before a client connects (mask %04b)", armed)
	}
	mac, _ := r.connect("mac", 1000, 500)
	eventually(t, "right edge armed", func() bool {
		_, _, a := r.cap.state()
		return a == layout.Right.Bit()
	})

	// The cursor hits the right edge of the server, half way down.
	r.cap.edge(layout.Right, 0.5)
	r.waitFocus("mac")
	if g, _, _ := r.cap.state(); g != 1 {
		t.Fatalf("grabs = %d, want 1", g)
	}
	waitPos(t, mac, 2, 250) // left edge of the client, same relative height

	// Motion is applied to the client's cursor.
	r.cap.move(100, 40)
	waitPos(t, mac, 102, 290)
	for i := 0; i < 50; i++ { // a burst, as a fast mouse produces
		r.cap.move(2, 1)
	}
	waitPos(t, mac, 202, 340)

	// Typing and clicking arrive in order, at the cursor position.
	r.cap.key(keys.A, keys.Down)
	r.cap.key(keys.A, keys.Repeat)
	r.cap.key(keys.A, keys.Up)
	r.cap.button(keys.ButtonLeft, true)
	r.cap.button(keys.ButtonLeft, false)
	r.cap.ch <- input.Event{Kind: input.EvWheel, WX: 0, WY: -120}
	eventually(t, "keys, click and wheel on the client", func() bool {
		return mac.has("key a 1; key a 2; key a 0; button 1 true at 202,340; button 1 false at 202,340; wheel 0 -120")
	})

	// While a button is held (a drag) the cursor must stay on this screen.
	r.cap.button(keys.ButtonLeft, true)
	r.cap.move(-5000, 0)
	waitPos(t, mac, 0, 340)
	if r.srv.Focus() != "mac" {
		t.Fatal("the cursor left the screen in the middle of a drag")
	}
	r.cap.button(keys.ButtonLeft, false)

	// Shift is still held when the cursor leaves: it must be released remotely.
	r.cap.key(keys.LeftShift, keys.Down)
	r.cap.move(-10, 0)
	r.waitFocus("")
	eventually(t, "release of held keys on the client", func() bool {
		return mac.has("key leftshift 1; key leftshift 0; releaseall")
	})
	_, rel, _ := r.cap.state()
	if len(rel) != 1 || rel[0] != "warp right 0.68" { // 340 / 499
		t.Fatalf("server release = %v, want [warp right 0.68]", rel)
	}

	// Input on the server's own desktop is not forwarded.
	before := mac.events()
	r.cap.move(50, 50)
	r.cap.key(keys.Enter, keys.Down)
	r.cap.key(keys.Enter, keys.Up)
	time.Sleep(80 * time.Millisecond)
	if after := mac.events(); after != before {
		t.Fatalf("client received input while the cursor was on the server:\n%s", after)
	}
}

func TestThreeScreensAndSkippingOfflineOnes(t *testing.T) {
	r := startServer(t, "desk", []string{"left", "desk", "mid", "far"}, nil)
	mid, stopMid := r.connect("mid", 800, 600)
	far, _ := r.connect("far", 1600, 1200)

	eventually(t, "right edge armed", func() bool {
		_, _, a := r.cap.state()
		return a == layout.Right.Bit() // "left" is not connected
	})

	r.cap.edge(layout.Right, 0)
	r.waitFocus("mid")
	waitPos(t, mid, 2, 0)

	// Push through the right edge of "mid" into "far".
	r.cap.move(400, 300)
	waitPos(t, mid, 402, 300)
	r.cap.move(500, 0)
	r.waitFocus("far")
	waitPos(t, far, 2, 601) // 300/599 of 1199 = 600.5, rounded
	eventually(t, "mid told that the cursor left", func() bool { return mid.has("releaseall") })

	// And back.
	r.cap.move(-10, 0)
	r.waitFocus("mid")
	waitPos(t, mid, 797, 300)

	// "mid" goes away while it has the cursor: control returns to the server.
	stopMid()
	r.waitFocus("")
	eventually(t, "capture released without warping", func() bool {
		_, rel, _ := r.cap.state()
		return len(rel) == 1 && rel[0] == "nowarp"
	})

	// With "mid" offline the right edge now leads straight to "far".
	eventually(t, "mid gone", func() bool { return len(r.srv.Connected()) == 1 })
	r.cap.edge(layout.Right, 1)
	r.waitFocus("far")
	waitPos(t, far, 2, 1199)

	// Leaving "far" to the left skips "mid" and lands on the server.
	r.cap.move(-10, 0)
	r.waitFocus("")
	_, rel, _ := r.cap.state()
	if got := rel[len(rel)-1]; got != "warp right 1.00" {
		t.Fatalf("last release = %q", got)
	}
}

func TestReleaseHotkey(t *testing.T) {
	r := startServer(t, "desk", []string{"desk", "mac"}, nil)
	mac, _ := r.connect("mac", 1000, 500)
	r.cap.edge(layout.Right, 0.5)
	r.waitFocus("mac")

	r.cap.key(keys.LeftCtrl, keys.Down)
	r.cap.key(keys.LeftAlt, keys.Down)
	r.cap.key(keys.LeftShift, keys.Down)
	r.cap.key(keys.Backspace, keys.Down)
	r.waitFocus("")
	eventually(t, "modifiers released on the client", func() bool {
		return mac.has("releaseall")
	})
	ev := mac.events()
	if strings.Contains(ev, "backspace") {
		t.Fatalf("the hotkey itself must not be typed on the client: %s", ev)
	}
	for _, k := range []string{"leftctrl 0", "leftalt 0", "leftshift 0"} {
		if !strings.Contains(ev, "key "+k) {
			t.Fatalf("%q missing from: %s", k, ev)
		}
	}
	if _, rel, _ := r.cap.state(); len(rel) != 1 || rel[0] != "nowarp" {
		t.Fatalf("releases = %v", rel)
	}
}

func TestScaleAndRemap(t *testing.T) {
	remap, err := keys.ParseRemap(map[string]string{"alt": "meta", "meta": "alt"})
	if err != nil {
		t.Fatal(err)
	}
	r := startServer(t, "desk", []string{"mac", "desk"}, map[string]server.ScreenOpts{
		"mac": {Scale: 2, Remap: remap},
	})
	mac, _ := r.connect("mac", 1000, 500)
	r.cap.edge(layout.Left, 0)
	r.waitFocus("mac")
	waitPos(t, mac, 997, 0) // enters through the client's right edge

	r.cap.move(-100, 50)
	waitPos(t, mac, 797, 100) // twice the mouse movement

	r.cap.key(keys.LeftAlt, keys.Down)
	r.cap.key(keys.C, keys.Down)
	r.cap.key(keys.C, keys.Up)
	r.cap.key(keys.LeftAlt, keys.Up)
	eventually(t, "remapped keys", func() bool {
		return mac.has("key leftsuper 1; key c 1; key c 0; key leftsuper 0")
	})
}

func TestWrongKeyAndUnknownNameAreRefused(t *testing.T) {
	r := startServer(t, "desk", []string{"desk", "mac"}, nil)

	run := func(name, key string) (*client.Client, context.CancelFunc) {
		c := client.New(client.Config{Name: name, Server: r.srv.Addr().String(), Key: key},
			&fakeInjector{w: 800, h: 600})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { c.Run(ctx); close(done) }()
		stop := func() { cancel(); <-done }
		t.Cleanup(stop)
		return c, stop
	}
	badKey, _ := run("mac", "not-the-key")
	stranger, _ := run("stranger", "test-key")
	sameName, _ := run("desk", "test-key")

	time.Sleep(300 * time.Millisecond)
	if badKey.Connected() || stranger.Connected() || sameName.Connected() {
		t.Fatal("a client that should have been refused is connected")
	}
	if got := r.srv.Connected(); len(got) != 0 {
		t.Fatalf("server lists %v", got)
	}
	// The legitimate client still gets in afterwards.
	r.connect("mac", 1000, 500)
}

func TestClientReconnectReplacesOldSession(t *testing.T) {
	r := startServer(t, "desk", []string{"desk", "mac"}, nil)
	_, stop1 := r.connect("mac", 1000, 500)
	stop1()
	eventually(t, "first session gone", func() bool { return len(r.srv.Connected()) == 0 })
	eventually(t, "edge disarmed", func() bool {
		_, _, a := r.cap.state()
		return a == 0
	})
	mac2, _ := r.connect("mac", 1280, 720)
	r.cap.edge(layout.Right, 1)
	r.waitFocus("mac")
	waitPos(t, mac2, 2, 719)
}

// TestMotionLatency measures how long a mouse movement takes from the moment
// it is captured on the server until the client moves its cursor, over the
// loopback interface (so: everything except the real network).
func TestMotionLatency(t *testing.T) {
	r := startServer(t, "desk", []string{"desk", "mac"}, nil)
	mac, _ := r.connect("mac", 100000, 1000)
	r.cap.edge(layout.Right, 0.5)
	r.waitFocus("mac")
	waitPos(t, mac, 2, 500)

	const n = 400
	sent := make([]time.Time, n+1)
	arrived := make([]time.Time, n+1)
	var mu sync.Mutex
	mac.mu.Lock()
	mac.onMove = func(x, y int) {
		if i := x - 2; i >= 1 && i <= n {
			mu.Lock()
			if arrived[i].IsZero() {
				arrived[i] = time.Now()
			}
			mu.Unlock()
		}
	}
	mac.mu.Unlock()
	for i := 1; i <= n; i++ {
		sent[i] = time.Now()
		r.cap.move(1, 0)
		time.Sleep(time.Millisecond) // a 1000 Hz mouse
	}
	waitPos(t, mac, 2+n, 500)

	var lat []time.Duration
	mu.Lock()
	for i := 1; i <= n; i++ {
		if !arrived[i].IsZero() {
			lat = append(lat, arrived[i].Sub(sent[i]))
		}
	}
	mu.Unlock()
	if len(lat) < n*9/10 {
		t.Fatalf("only %d of %d positions arrived individually", len(lat), n)
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	p50, p99 := lat[len(lat)/2], lat[len(lat)*99/100]
	// Note: on Windows the Go clock only resolves about half a millisecond,
	// so a reported 0s means "less than the clock can measure".
	t.Logf("capture -> inject over loopback: median %v, 99th percentile %v, worst %v (%d samples)",
		p50, p99, lat[len(lat)-1], len(lat))
	if p50 > 5*time.Millisecond {
		t.Errorf("median latency %v is too high", p50)
	}
}

// A computer with the right key that is missing from the layout is turned
// away, but both sides must learn why: the server lists it as waiting, and
// the client knows the reason.
func TestUnknownClientIsReportedOnBothSides(t *testing.T) {
	r := startServer(t, "desk", []string{"desk", "mac"}, nil)
	inj := &fakeInjector{w: 800, h: 600}
	c := client.New(client.Config{Name: "newpc", Server: r.srv.Addr().String(), Key: r.key}, inj)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the server to list the waiting computer", func() bool {
		u := r.srv.Unknown()
		return len(u) == 1 && u[0].Name == "newpc" && u[0].W == 800 && u[0].Addr != ""
	})
	eventually(t, "the client to know why it was refused", func() bool {
		return strings.Contains(c.LastError(), "not in the server's layout")
	})
	if c.Connected() {
		t.Fatal("a computer outside the layout must not be connected")
	}

	// A wrong key is a different reason, and such a computer is not listed.
	bad := client.New(client.Config{Name: "intruder", Server: r.srv.Addr().String(), Key: "wrong"}, &fakeInjector{w: 800, h: 600})
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() { bad.Run(ctx2); close(done2) }()
	t.Cleanup(func() { cancel2(); <-done2 })
	eventually(t, "the wrong-key reason", func() bool { return strings.Contains(bad.LastError(), "does not match") })
	for _, u := range r.srv.Unknown() {
		if u.Name == "intruder" {
			t.Fatal("a computer with the wrong key must not be offered for adding")
		}
	}

	// Once connected, the error is cleared.
	mac := client.New(client.Config{Name: "mac", Server: r.srv.Addr().String(), Key: r.key}, &fakeInjector{w: 800, h: 600})
	ctx3, cancel3 := context.WithCancel(context.Background())
	done3 := make(chan struct{})
	go func() { mac.Run(ctx3); close(done3) }()
	t.Cleanup(func() { cancel3(); <-done3 })
	eventually(t, "mac to connect", func() bool { return mac.Connected() && mac.LastError() == "" })
}

// With automatic speed the cursor covers a screen that has more pixels with
// the same hand movement; the per-screen option multiplies that.
func TestAutomaticCursorSpeed(t *testing.T) {
	// The fake server desktop is 1080 pixels tall.
	r := startServerWith(t, "desk", []string{"desk", "big", "tiny", "fast"}, map[string]server.ScreenOpts{
		"fast": {Scale: 2},
	}, true)
	big, _ := r.connect("big", 4000, 2160)   // twice as tall: x2
	tiny, _ := r.connect("tiny", 4000, 270)  // a quarter: limited to x0.5
	fast, _ := r.connect("fast", 4000, 1080) // same height, option x2

	r.cap.edge(layout.Right, 0)
	r.waitFocus("big")
	waitPos(t, big, 2, 0)
	r.cap.move(100, 50)
	waitPos(t, big, 202, 100)

	r.cap.move(2000, 0) // x2 = 4000: through the right edge into "tiny"
	r.waitFocus("tiny")
	waitPos(t, tiny, 2, 12) // 100/2159 of 269
	r.cap.move(100, 40)
	waitPos(t, tiny, 52, 32)

	r.cap.move(8000, 0) // x0.5 = 4000: on into "fast"
	r.waitFocus("fast")
	fx, fy := fast.pos()
	r.cap.move(100, 10)
	waitPos(t, fast, fx+200, fy+20)
}
