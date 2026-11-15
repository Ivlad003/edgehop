package rview

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Ivlad003/edgehop/internal/keys"
)

// viewRig is a viewer in front of a fake computer "win" with one window.
type viewRig struct {
	t      *testing.T
	v      *Viewer
	desk   *fakeDesk
	inj    *fakeInjector
	client *http.Client // has the access cookie
	base   string
}

func newViewRig(t *testing.T) *viewRig {
	t.Helper()
	r := &viewRig{t: t, desk: newDesk(200, 120), inj: &fakeInjector{}}
	src := &Source{Windows: r.desk, Injector: r.inj, Name: "win", OS: "windows", Fast: 5 * time.Millisecond, Slow: 20 * time.Millisecond}
	r.v = &Viewer{
		Peers: func() []Peer { return []Peer{{Name: "win", OS: "windows"}} },
		Open: func(peer string) (Conn, error) {
			if peer != "win" {
				return nil, fmt.Errorf("%q is not connected", peer)
			}
			a, b := newPipe()
			go src.Serve(a)
			return b, nil
		},
		Remap: func(string) map[uint16]uint16 { return map[uint16]uint16{keys.B: keys.C} },
	}
	if err := r.v.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.v.Serve(ctx)
	r.base = "http://" + r.v.ln.Addr().String()

	jar, _ := cookiejar.New(nil)
	r.client = &http.Client{Jar: jar, Timeout: 10 * time.Second}
	resp, err := r.client.Get(r.v.URL()) // follows the redirect and keeps the cookie
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "list.js") {
		t.Fatalf("opening the link: %d %.80s", resp.StatusCode, body)
	}
	return r
}

func (r *viewRig) get(path string) (int, string) {
	r.t.Helper()
	resp, err := r.client.Get(r.base + path)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func (r *viewRig) input(sid string, events string) int {
	r.t.Helper()
	resp, err := r.client.Post(r.base+"/api/input?sid="+sid, "application/json", strings.NewReader(`{"ev":`+events+`}`))
	if err != nil {
		r.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestViewerNeedsTheAccessCode(t *testing.T) {
	r := newViewRig(t)
	plain := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	status := func(req *http.Request) int {
		resp, err := plain.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	get := func(path string) *http.Request { req, _ := http.NewRequest("GET", r.base+path, nil); return req }

	for _, path := range []string{"/", "/view.html", "/view.js", "/api/peers", "/api/windows?peer=win", "/api/stream?peer=win&win=7"} {
		if got := status(get(path)); got != http.StatusUnauthorized {
			t.Errorf("%s without the code: status %d, want 401", path, got)
		}
	}
	if got := status(get("/?t=wrong")); got != http.StatusUnauthorized {
		t.Errorf("a wrong code: status %d, want 401", got)
	}
	// Requests another web site could cause are refused even with the cookie.
	cookie := r.client.Jar.Cookies(get("/").URL)[0]
	foreign := get("/api/peers")
	foreign.AddCookie(cookie)
	foreign.Host = "evil.example"
	if got := status(foreign); got != http.StatusForbidden {
		t.Errorf("a foreign host name: status %d, want 403", got)
	}
	cross, _ := http.NewRequest("POST", r.base+"/api/input?sid=x", strings.NewReader(`{"ev":[]}`))
	cross.AddCookie(cookie)
	cross.Header.Set("Origin", "https://evil.example")
	if got := status(cross); got != http.StatusForbidden {
		t.Errorf("a request from another site: status %d, want 403", got)
	}
}

func TestViewerListsWindows(t *testing.T) {
	r := newViewRig(t)
	if code, body := r.get("/api/peers"); code != 200 || !strings.Contains(body, `"name":"win"`) {
		t.Errorf("peers: %d %s", code, body)
	}
	code, body := r.get("/api/windows?peer=win")
	var list WindowList
	json.Unmarshal([]byte(body), &list)
	if code != 200 || list.Name != "win" || len(list.Windows) != 1 || list.Windows[0].ID != 7 || list.Windows[0].Title != "Notes" {
		t.Errorf("windows: %d %s", code, body)
	}
	if code, body := r.get("/api/windows?peer=ghost"); code != http.StatusBadGateway || !strings.Contains(body, "not connected") {
		t.Errorf("windows of an unknown computer: %d %s", code, body)
	}
}

// browserMsg reads one message of the stream the browser gets.
func browserMsg(br *bufio.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, be.Uint32(hdr[1:]))
	_, err := io.ReadFull(br, payload)
	return hdr[0], payload, err
}

func TestViewerShowsAWindowAndPassesInput(t *testing.T) {
	r := newViewRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", r.base+"/api/stream?peer=win&win=7", nil)
	streaming := &http.Client{Jar: r.client.Jar} // no timeout: the stream stays open
	resp, err := streaming.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)

	var sid string
	var meta metaInfo
	var pic *image.RGBA
	// update reads messages until one update is complete and confirms it.
	update := func() (format byte) {
		t.Helper()
		for {
			typ, payload, err := browserMsg(br)
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			switch typ {
			case bHello:
				var h struct{ Sid string }
				json.Unmarshal(payload, &h)
				sid = h.Sid
			case bMeta:
				json.Unmarshal(payload, &meta)
				pic = image.NewRGBA(image.Rect(0, 0, meta.W, meta.H))
			case bRect:
				seq := be.Uint32(payload)
				x, y := int(be.Uint16(payload[4:])), int(be.Uint16(payload[6:]))
				w, h := int(be.Uint16(payload[8:])), int(be.Uint16(payload[10:]))
				format = payload[12]
				var img image.Image
				if format == fmtJPEG {
					img, err = jpeg.Decode(bytes.NewReader(payload[14:]))
				} else {
					img, err = png.Decode(bytes.NewReader(payload[14:]))
				}
				if err != nil || img.Bounds().Dx() != w || img.Bounds().Dy() != h {
					t.Fatalf("rectangle %dx%d at %d,%d: %v", w, h, x, y, err)
				}
				for yy := 0; yy < h; yy++ {
					for xx := 0; xx < w; xx++ {
						pic.Set(x+xx, y+yy, img.At(img.Bounds().Min.X+xx, img.Bounds().Min.Y+yy))
					}
				}
				if payload[13] != 0 {
					if code := r.input(sid, fmt.Sprintf(`[[%d,%d]]`, evAck, seq)); code != http.StatusNoContent {
						t.Fatalf("confirming update %d: status %d", seq, code)
					}
					return format
				}
			case bEnd:
				t.Fatalf("the view ended: %s", payload)
			}
		}
	}

	if update() != fmtJPEG || sid == "" || meta.Title != "Notes" || meta.W != 200 || meta.H != 120 || meta.Name != "win" {
		t.Fatalf("first update: sid %q, meta %+v", sid, meta)
	}
	if update() != fmtPNG {
		t.Fatal("the sharp picture did not follow")
	}
	r.desk.mu.Lock()
	same := bytes.Equal(pic.Pix, r.desk.pix)
	r.desk.mu.Unlock()
	if !same {
		t.Fatal("the picture in the browser differs from the window")
	}

	// Input: position, click, key (remapped B -> C as configured), wheel.
	// Odd events are skipped without stopping the rest.
	events := fmt.Sprintf(`[[%d,0,0],[%d,65535,65535,1,1],[%d,65535,65535,1,0],[99],[%d,%d,1],[%d,%d,0],[%d,0,0,0,-120],[%d,70000,0]]`,
		evPointer, evButton, evButton, evKey, keys.B, evKey, keys.B, evWheel, evPointer)
	if code := r.input(sid, events); code != http.StatusNoContent {
		t.Fatalf("input: status %d", code)
	}
	want := fmt.Sprintf("[move 300,200 move 499,319 button 1 true move 499,319 button 1 false key %d 1 key %d 0 move 300,200 wheel 0,-120]", keys.C, keys.C)
	eventually(t, "the input", func() bool { r.inj.mu.Lock(); defer r.inj.mu.Unlock(); return len(r.inj.log) >= 9 })
	if got := fmt.Sprint(r.inj.take()); got != want {
		t.Errorf("input arrived as\n %s\nwant\n %s", got, want)
	}

	// Closing the browser window releases what is held and ends the view.
	r.input(sid, fmt.Sprintf(`[[%d,%d,1]]`, evKey, keys.A))
	eventually(t, "the key", func() bool { r.inj.mu.Lock(); defer r.inj.mu.Unlock(); return len(r.inj.log) >= 1 })
	r.inj.take()
	cancel()
	eventually(t, "the release", func() bool { r.inj.mu.Lock(); defer r.inj.mu.Unlock(); return len(r.inj.log) >= 1 })
	if got := fmt.Sprint(r.inj.take()); got != fmt.Sprintf("[key %d 0]", keys.A) {
		t.Errorf("after closing: %s", got)
	}
	eventually(t, "the view to be forgotten", func() bool { return r.input(sid, `[]`) == http.StatusGone })
}

func TestViewerReportsWhyAViewEnded(t *testing.T) {
	r := newViewRig(t)
	for _, tc := range []struct{ query, want string }{
		{"peer=ghost&win=7", "not connected"},
		{"peer=win&win=99", "closed"},
	} {
		resp, err := r.client.Get(r.base + "/api/stream?" + tc.query)
		if err != nil {
			t.Fatal(err)
		}
		br := bufio.NewReader(resp.Body)
		reason := ""
		for {
			typ, payload, err := browserMsg(br)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					t.Errorf("%s: %v", tc.query, err)
				}
				break
			}
			if typ == bEnd {
				reason = string(payload)
			}
		}
		resp.Body.Close()
		if !strings.Contains(reason, tc.want) {
			t.Errorf("%s: ended with %q, want something about %q", tc.query, reason, tc.want)
		}
	}
	if code, _ := r.get("/api/stream?peer=win&win=abc"); code != http.StatusBadRequest {
		t.Errorf("a window id that is not a number: status %d", code)
	}
}

// The link for a window that was dragged over: one address that lets the
// browser in and leads straight to that window - and nowhere else.
func TestWindowLinkOpensThatWindow(t *testing.T) {
	r := newViewRig(t)
	plain := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	location := func(link string) (int, string, bool) {
		resp, err := plain.Get(link)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("Location"), len(resp.Cookies()) > 0
	}
	code, to, cookie := location(r.v.WindowURL("win", 7))
	if code != http.StatusSeeOther || to != "/view.html?front=1&hide=1&peer=win&win=7" || !cookie {
		t.Errorf("the window link: %d to %q, cookie %v", code, to, cookie)
	}
	for _, bad := range []string{"//evil.example/view.html?x", "https://evil.example/", "/api/peers", "index.html"} {
		link := r.base + "/?" + url.Values{"t": {r.v.token}, "go": {bad}}.Encode()
		if code, to, _ := location(link); code != http.StatusSeeOther || to != "/" {
			t.Errorf("go=%q leads to %q (%d), want the start page", bad, to, code)
		}
	}

	// Opened that way, the window is brought to the front on its computer.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", r.base+"/api/stream?peer=win&win=7&front=1&hide=1", nil)
	resp, err := (&http.Client{Jar: r.client.Jar}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	eventually(t, "the window to be raised and hidden", func() bool {
		r.desk.mu.Lock()
		defer r.desk.mu.Unlock()
		return r.desk.raises == 1 && r.desk.hidden
	})
	// The size of the browser window is passed on to the window.
	br := bufio.NewReader(resp.Body)
	typ, payload, err := browserMsg(br)
	if err != nil || typ != bHello {
		t.Fatalf("first message: %d %v", typ, err)
	}
	var hello struct{ Sid string }
	json.Unmarshal(payload, &hello)
	if code := r.input(hello.Sid, fmt.Sprintf(`[[%d,260,140],[%d,0,140],[%d,70000,10]]`, evResize, evResize, evResize)); code != http.StatusNoContent {
		t.Fatalf("resize: status %d", code)
	}
	eventually(t, "the new size", func() bool {
		r.desk.mu.Lock()
		defer r.desk.mu.Unlock()
		return fmt.Sprint(r.desk.sizes) == "[[260 140]]"
	})
}

func TestAppWindowArguments(t *testing.T) {
	got := fmt.Sprint(appWindowArgs("http://127.0.0.1:5/?t=x&go=view.html", -1200, 40, 800, 600))
	want := "[--app=http://127.0.0.1:5/?t=x&go=view.html --window-size=800,600 --window-position=-1200,40]"
	if got != want {
		t.Errorf("arguments = %s\nwant        %s", got, want)
	}
}
