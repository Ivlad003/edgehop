package ui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ivlad003/edgehop/internal/config"
)

// The test binary doubles as a fake engine: with EDGEHOP_FAKE_ENGINE set it
// behaves like `edgehop run --child` instead of running the tests.
func TestMain(m *testing.M) {
	if os.Getenv("EDGEHOP_FAKE_ENGINE") == "1" {
		if os.Getenv("EDGEHOP_FAKE_FAIL") == "1" {
			fmt.Fprintln(os.Stderr, "edgehop: cannot listen on :1: boom")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "12:00:00.000 fake engine started: "+strings.Join(os.Args[1:], " "))
		fmt.Println(`{"role":"client","name":"t","server":"srv:1","connected":true,"active":false}`)
		// Commands arrive as lines on stdin; the page closes stdin to stop us.
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			fmt.Fprintln(os.Stderr, "12:00:00.500 command: "+sc.Text())
		}
		fmt.Fprintln(os.Stderr, "12:00:01.000 fake engine stopping")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type rig struct {
	t      *testing.T
	srv    *Server
	http   *httptest.Server
	client *http.Client
	cookie *http.Cookie
	cfg    string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("EDGEHOP_FAKE_ENGINE", "1")
	srv, err := New(Options{
		Addr:       "127.0.0.1:0",
		ConfigPath: filepath.Join(dir, "edgehop.toml"),
		TokenPath:  filepath.Join(dir, "ui-token"),
		SpoolDir:   filepath.Join(dir, "spool"),
		Version:    "1.2.3",
		RepoURL:    "https://example.org/edgehop",
		Exe:        os.Args[0],
	})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		srv.engine.stop()
		hs.Close()
	})
	r := &rig{t: t, srv: srv, http: hs, cfg: srv.opts.ConfigPath}
	r.client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r.cookie = &http.Cookie{Name: cookieName, Value: srv.token}
	return r
}

// do sends a request. body == nil means GET; auth adds the session cookie.
func (r *rig) do(path string, body any, auth bool, edit func(*http.Request)) (*http.Response, []byte) {
	r.t.Helper()
	var req *http.Request
	if body == nil {
		req, _ = http.NewRequest(http.MethodGet, r.http.URL+path, nil)
	} else {
		data, _ := json.Marshal(body)
		req, _ = http.NewRequest(http.MethodPost, r.http.URL+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.AddCookie(r.cookie)
	}
	if edit != nil {
		edit(req)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func (r *rig) state() state {
	r.t.Helper()
	resp, data := r.do("/api/state", nil, true, nil)
	if resp.StatusCode != 200 {
		r.t.Fatalf("state: %d %s", resp.StatusCode, data)
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		r.t.Fatalf("state: %v in %s", err, data)
	}
	return st
}

func (r *rig) waitFor(what string, cond func(state) bool) state {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := r.state()
		if cond(st) {
			return st
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s; engine=%+v logs=%v", what, st.Engine, st.Logs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAccessControl(t *testing.T) {
	r := newRig(t)

	// The API is closed without the cookie.
	if resp, _ := r.do("/api/state", nil, false, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no cookie: status %d, want 401", resp.StatusCode)
	}
	bad := func(req *http.Request) { req.AddCookie(&http.Cookie{Name: cookieName, Value: "nope"}) }
	if resp, _ := r.do("/api/state", nil, false, bad); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong cookie: status %d, want 401", resp.StatusCode)
	}

	// The start-up link trades the token for a cookie; a wrong token gets none.
	resp, _ := r.do("/?t="+r.srv.token, nil, false, nil)
	if resp.StatusCode != http.StatusSeeOther || len(resp.Cookies()) != 1 || resp.Cookies()[0].Value != r.srv.token {
		t.Errorf("token link: status %d cookies %v", resp.StatusCode, resp.Cookies())
	} else if c := resp.Cookies()[0]; !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie must be HttpOnly and SameSite=Strict: %+v", c)
	}
	if resp, _ := r.do("/?t=wrong", nil, false, nil); len(resp.Cookies()) != 0 {
		t.Errorf("a wrong token must not set a cookie")
	}
	if !strings.Contains(r.srv.URL(), "/?t="+r.srv.token) {
		t.Errorf("URL() = %s", r.srv.URL())
	}

	// A page on another site must not be able to talk to us.
	rebinding := func(req *http.Request) { req.Host = "evil.example:24921" }
	if resp, _ := r.do("/api/state", nil, true, rebinding); resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host: status %d, want 403", resp.StatusCode)
	}
	crossSite := func(req *http.Request) { req.Header.Set("Origin", "https://evil.example") }
	if resp, _ := r.do("/api/stop", map[string]any{}, true, crossSite); resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Origin: status %d, want 403", resp.StatusCode)
	}
	sameSite := func(req *http.Request) { req.Header.Set("Origin", r.http.URL) }
	if resp, _ := r.do("/api/stop", map[string]any{}, true, sameSite); resp.StatusCode != 200 {
		t.Errorf("own Origin: status %d, want 200", resp.StatusCode)
	}
	form := func(req *http.Request) { req.Header.Set("Content-Type", "text/plain") }
	if resp, _ := r.do("/api/stop", map[string]any{}, true, form); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("non-JSON post: status %d, want 415", resp.StatusCode)
	}
	if resp, _ := r.do("/api/stop", nil, true, nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET on a POST endpoint: status %d, want 405", resp.StatusCode)
	}
}

func TestOnlyLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:24921", "192.168.1.5:24921", "example.org:80"} {
		if _, err := New(Options{Addr: addr, TokenPath: filepath.Join(t.TempDir(), "tok")}); err == nil {
			t.Errorf("New(%s) must be refused: the page hands out the key", addr)
		}
	}
}

func TestTokenIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "ui-token")
	a, err := loadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := loadToken(path)
	if a != b || len(a) < 32 {
		t.Errorf("tokens %q and %q", a, b)
	}
}

func TestStaticPage(t *testing.T) {
	r := newRig(t)
	resp, body := r.do("/", nil, false, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "<title>edgehop</title>") {
		t.Fatalf("index: %d", resp.StatusCode)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}
	for _, f := range []string{"/app.js", "/style.css", "/favicon.svg"} {
		if resp, _ := r.do(f, nil, false, nil); resp.StatusCode != 200 {
			t.Errorf("%s: status %d", f, resp.StatusCode)
		}
	}
	// The page must not need inline scripts or styles (the CSP forbids them).
	if strings.Contains(string(body), "<script>") || strings.Contains(string(body), " style=") || strings.Contains(string(body), "onclick=") {
		t.Errorf("index.html contains inline script or style")
	}
}

func TestConfigThroughThePage(t *testing.T) {
	r := newRig(t)

	st := r.state()
	if st.ConfigExists || st.Config.Name == "" || len(st.Config.Row) != 1 || st.Config.Port != config.DefaultPort {
		t.Fatalf("fresh state = %+v", st)
	}
	if st.Version != "1.2.3" || st.RepoURL == "" || st.Engine.Running {
		t.Fatalf("fresh state = %+v", st)
	}

	// Invalid settings are refused and nothing is written.
	resp, body := r.do("/api/config", map[string]any{"config": config.Form{Role: "server", Name: "desk"}}, true, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "key") {
		t.Fatalf("invalid config: %d %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(r.cfg); err == nil {
		t.Fatal("an invalid config was written")
	}

	form := config.Form{
		Role: "server", Name: "desk", Key: "secret", Port: 5001,
		Row:           []string{"lap", "desk", "mac"},
		ReleaseHotkey: "ctrl+alt+f12", HideCursor: true,
		Screens: map[string]config.FormScreen{"mac": {Scale: 1.5, SwapAltMeta: true}},
	}
	resp, body = r.do("/api/config", map[string]any{"config": form, "start": false}, true, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("save: %d %s", resp.StatusCode, body)
	}
	cfg, err := config.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Role != "server" || cfg.Listen != "0.0.0.0:5001" || strings.Join(cfg.Row, ",") != "lap,desk,mac" ||
		cfg.Screens["mac"].Scale != 1.5 || cfg.Screens["mac"].Remap["alt"] != "meta" {
		t.Fatalf("written config = %+v", cfg)
	}
	st = r.state()
	if !st.ConfigExists || st.Config.Port != 5001 || !st.Config.Screens["mac"].SwapAltMeta || st.Engine.Running {
		t.Fatalf("state after save = %+v", st)
	}

	// A broken file is reported, and the form falls back to defaults.
	os.WriteFile(r.cfg, []byte("role = \n"), 0o600)
	st = r.state()
	if !st.ConfigExists || st.ConfigError == "" {
		t.Fatalf("broken file not reported: %+v", st)
	}
	if resp, _ := r.do("/api/start", map[string]any{}, true, nil); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("start with a broken config: status %d, want 422", resp.StatusCode)
	}

	resp, body = r.do("/api/newkey", map[string]any{}, true, nil)
	var k struct{ Key string }
	json.Unmarshal(body, &k)
	if resp.StatusCode != 200 || len(k.Key) != 32 {
		t.Errorf("newkey: %d %s", resp.StatusCode, body)
	}
}

func TestEngineLifecycle(t *testing.T) {
	r := newRig(t)
	form := config.Form{Role: "client", Name: "lap", Key: "secret", Server: "10.0.0.1"}
	resp, body := r.do("/api/config", map[string]any{"config": form, "start": true}, true, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("save and start: %d %s", resp.StatusCode, body)
	}
	st := r.waitFor("status report", func(s state) bool { return s.Engine.Running && len(s.Engine.Status) > 0 })
	var status struct {
		Role      string
		Connected bool
	}
	json.Unmarshal(st.Engine.Status, &status)
	if status.Role != "client" || !status.Connected {
		t.Errorf("status = %s", st.Engine.Status)
	}
	if st.Engine.Since == "" {
		t.Errorf("no start time")
	}
	st = r.waitFor("log output", func(s state) bool { return len(s.Logs) > 0 })
	if !strings.Contains(st.Logs[0], "run --child -c "+r.cfg) {
		t.Errorf("engine arguments: %q", st.Logs[0])
	}

	// Starting again while it runs is a no-op.
	if resp, _ := r.do("/api/start", map[string]any{}, true, nil); resp.StatusCode != 200 {
		t.Errorf("second start: %d", resp.StatusCode)
	}

	// Stop: the engine gets the chance to shut down by itself.
	resp, body = r.do("/api/stop", map[string]any{}, true, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("stop: %d %s", resp.StatusCode, body)
	}
	st = r.state()
	if st.Engine.Running || st.Engine.Exit != "" || len(st.Engine.Status) != 0 {
		t.Errorf("after stop: %+v", st.Engine)
	}
	if last := st.Logs[len(st.Logs)-1]; !strings.Contains(last, "fake engine stopping") {
		t.Errorf("the engine was killed instead of stopping cleanly; last log line: %q", last)
	}
}

func TestEngineFailureIsReported(t *testing.T) {
	r := newRig(t)
	t.Setenv("EDGEHOP_FAKE_FAIL", "1")
	form := config.Form{Role: "client", Name: "lap", Key: "secret", Server: "10.0.0.1"}
	if resp, body := r.do("/api/config", map[string]any{"config": form, "start": true}, true, nil); resp.StatusCode != 200 {
		t.Fatalf("save and start: %d %s", resp.StatusCode, body)
	}
	st := r.waitFor("the engine to fail", func(s state) bool { return !s.Engine.Running })
	if st.Engine.Exit != "cannot listen on :1: boom" {
		t.Errorf("exit reason = %q", st.Engine.Exit)
	}
}

// put uploads one file body the way the page does.
func (r *rig) put(batch, path string, body []byte, auth bool) (*http.Response, []byte) {
	r.t.Helper()
	q := url.Values{"batch": {batch}, "path": {path}}
	req, _ := http.NewRequest(http.MethodPost, r.http.URL+"/api/send/file?"+q.Encode(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	if auth {
		req.AddCookie(r.cookie)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func (r *rig) begin() string {
	r.t.Helper()
	resp, body := r.do("/api/send/begin", map[string]any{}, true, nil)
	var out struct{ Batch string }
	json.Unmarshal(body, &out)
	if resp.StatusCode != 200 || len(out.Batch) != 16 {
		r.t.Fatalf("begin: %d %s", resp.StatusCode, body)
	}
	return out.Batch
}

func TestSendingFilesThroughThePage(t *testing.T) {
	r := newRig(t)
	spool := r.srv.opts.SpoolDir

	// Nothing can be sent while edgehop is not running.
	early := r.begin()
	r.put(early, "a.txt", []byte("x"), true)
	if resp, body := r.do("/api/send/commit", map[string]any{"batch": early, "to": "srv"}, true, nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("commit without a running engine: %d %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(spool, early)); !os.IsNotExist(err) {
		t.Error("a refused batch was left in the spool folder")
	}

	form := config.Form{Role: "client", Name: "lap", Key: "secret", Server: "10.0.0.1"}
	if resp, body := r.do("/api/config", map[string]any{"config": form, "start": true}, true, nil); resp.StatusCode != 200 {
		t.Fatalf("start: %d %s", resp.StatusCode, body)
	}
	r.waitFor("the engine", func(s state) bool { return s.Engine.Running })

	batch := r.begin()
	big := bytes.Repeat([]byte("0123456789abcdef"), 70000) // over a megabyte: no size limit on uploads
	for path, body := range map[string][]byte{"notes.txt": []byte("привіт"), "album/2026/pic.bin": big} {
		if resp, data := r.put(batch, path, body, true); resp.StatusCode != 200 {
			t.Fatalf("upload %s: %d %s", path, resp.StatusCode, data)
		}
	}
	dir := filepath.Join(spool, batch)
	if data, _ := os.ReadFile(filepath.Join(dir, "notes.txt")); string(data) != "привіт" {
		t.Errorf("notes.txt = %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "album", "2026", "pic.bin")); !bytes.Equal(data, big) {
		t.Errorf("pic.bin has %d bytes, want %d", len(data), len(big))
	}

	// What must be refused.
	if resp, _ := r.put(batch, "notes.txt", []byte("again"), true); resp.StatusCode != http.StatusConflict {
		t.Errorf("the same file twice: status %d, want 409", resp.StatusCode)
	}
	for _, bad := range []string{"../evil.txt", "/etc/passwd", "a/../../b", ""} {
		if resp, _ := r.put(batch, bad, []byte("x"), true); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("path %q: status %d, want 400", bad, resp.StatusCode)
		}
	}
	if _, err := os.Stat(filepath.Join(spool, "evil.txt")); err == nil {
		t.Fatal("an upload escaped its batch folder")
	}
	if resp, _ := r.put(batch, "x.txt", []byte("x"), false); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("upload without the cookie: status %d, want 401", resp.StatusCode)
	}
	for _, id := range []string{"0000000000000000", "../../../../etc", "short"} {
		if resp, _ := r.put(id, "x.txt", []byte("x"), true); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("batch %q: status %d, want 400", id, resp.StatusCode)
		}
	}

	// Commit hands the folder to the engine.
	if resp, body := r.do("/api/send/commit", map[string]any{"batch": batch, "to": "srv"}, true, nil); resp.StatusCode != 200 {
		t.Fatalf("commit: %d %s", resp.StatusCode, body)
	}
	st := r.waitFor("the command to reach the engine", func(s state) bool {
		return strings.Contains(strings.Join(s.Logs, "\n"), `"cmd":"send"`)
	})
	var got map[string]string
	for _, line := range st.Logs {
		if i := strings.Index(line, "command: "); i >= 0 {
			json.Unmarshal([]byte(line[i+len("command: "):]), &got)
		}
	}
	if got["to"] != "srv" || got["dir"] != dir {
		t.Errorf("command = %v, want to=srv dir=%s", got, dir)
	}

	// An empty batch is not sent; cancel removes a batch.
	empty := r.begin()
	if resp, _ := r.do("/api/send/commit", map[string]any{"batch": empty, "to": "srv"}, true, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty batch: status %d, want 400", resp.StatusCode)
	}
	other := r.begin()
	r.put(other, "a.txt", []byte("x"), true)
	r.do("/api/send/cancel", map[string]any{"batch": other}, true, nil)
	if _, err := os.Stat(filepath.Join(spool, other)); !os.IsNotExist(err) {
		t.Error("cancel did not remove the batch")
	}
}
