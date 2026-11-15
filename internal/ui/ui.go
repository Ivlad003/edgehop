// Package ui serves the local settings page: a small web interface, reachable
// only from this computer, to configure edgehop and to start and stop it.
package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ivlad003/edgehop/internal/config"
	"github.com/Ivlad003/edgehop/internal/input"
	"github.com/Ivlad003/edgehop/internal/layout"
	"github.com/Ivlad003/edgehop/internal/logx"
	"github.com/Ivlad003/edgehop/internal/secure"
	"github.com/Ivlad003/edgehop/internal/xfer"
)

//go:embed web
var webFiles embed.FS

// DefaultPort is the port of the settings page.
const DefaultPort = 24921

const cookieName = "edgehop_ui"

// Options configure the settings page.
type Options struct {
	Addr       string   // listen address; must be a loopback address
	ConfigPath string   // the config file to edit and run
	TokenPath  string   // where the access token is kept ("" = next to the config)
	Version    string   //
	RepoURL    string   // project page, for the "download for other computers" link
	Exe        string   // the edgehop program, used to run the engine
	EngineArgs []string // extra arguments for the engine
	AutoStart  bool     // start the engine right away if the config is valid
	SpoolDir   string   // where files dropped on the page wait to be sent ("" = system temp folder)
}

// Server is the settings page.
type Server struct {
	opts   Options
	token  string
	engine *engine
	ln     net.Listener

	deskMu sync.Mutex
	desk   layout.Desktop
	deskAt time.Time
}

// New prepares the settings page; call Run to serve it.
func New(opts Options) (*Server, error) {
	if opts.Addr == "" {
		opts.Addr = fmt.Sprintf("127.0.0.1:%d", DefaultPort)
	}
	host, _, err := net.SplitHostPort(opts.Addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		// The page hands out the shared key; it must never be reachable
		// from other computers.
		return nil, fmt.Errorf("the settings page may only listen on this computer (127.0.0.1), not on %s", host)
	}
	if opts.SpoolDir == "" {
		opts.SpoolDir = filepath.Join(os.TempDir(), "edgehop-send")
	}
	cleanSpool(opts.SpoolDir)
	if opts.TokenPath == "" {
		opts.TokenPath = filepath.Join(filepath.Dir(config.UserPath()), "ui-token")
	}
	token, err := loadToken(opts.TokenPath)
	if err != nil {
		return nil, err
	}
	return &Server{
		opts:   opts,
		token:  token,
		engine: &engine{exe: opts.Exe, args: opts.EngineArgs},
	}, nil
}

// loadToken returns the access token, creating it on first use. It stays the
// same between runs so that a bookmark of the page keeps working.
func loadToken(path string) (string, error) {
	if data, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(data)); len(t) >= 32 {
			return t, nil
		}
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

// Listen opens the port.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return fmt.Errorf("cannot open the settings page on %s: %w (is edgehop already running?)", s.opts.Addr, err)
	}
	s.ln = ln
	return nil
}

// URL returns the address of the page, including the access token.
func (s *Server) URL() string {
	addr := s.opts.Addr
	if s.ln != nil {
		addr = s.ln.Addr().String()
	}
	return "http://" + addr + "/?t=" + s.token
}

// Run serves the page until ctx is cancelled, then stops the engine.
func (s *Server) Run(ctx context.Context) error {
	if s.ln == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}
	if s.opts.AutoStart {
		if _, err := config.Load(s.opts.ConfigPath); err == nil {
			if err := s.engine.start(s.opts.ConfigPath); err != nil {
				logx.Warnf("could not start: %v", err)
			}
		}
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(s.ln) }()
	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	s.engine.stop()
	shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv.Shutdown(shutCtx)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// OpenBrowser opens url in the default browser.
func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// ---------------------------------------------------------------------------
// HTTP

// Handler returns the HTTP handler of the page.
func (s *Server) Handler() http.Handler {
	static, _ := fs.Sub(webFiles, "web")
	files := http.FileServer(http.FS(static))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", s.api(http.MethodGet, s.handleState))
	mux.HandleFunc("/api/config", s.api(http.MethodPost, s.handleConfig))
	mux.HandleFunc("/api/start", s.api(http.MethodPost, s.handleStart))
	mux.HandleFunc("/api/stop", s.api(http.MethodPost, s.handleStop))
	mux.HandleFunc("/api/newkey", s.api(http.MethodPost, s.handleNewKey))
	mux.HandleFunc("/api/send/begin", s.api(http.MethodPost, s.handleSendBegin))
	mux.HandleFunc("/api/send/file", s.upload(s.handleSendFile))
	mux.HandleFunc("/api/send/commit", s.api(http.MethodPost, s.handleSendCommit))
	mux.HandleFunc("/api/send/cancel", s.api(http.MethodPost, s.handleSendCancel))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("t"); t != "" && r.URL.Path == "/" {
			// The link printed at start-up carries the token; trade it for
			// a cookie and drop it from the address bar.
			if s.tokenOK(t) {
				http.SetCookie(w, &http.Cookie{
					Name: cookieName, Value: s.token, Path: "/",
					HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 365 * 24 * 3600,
				})
			}
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
	return s.guard(mux)
}

func (s *Server) tokenOK(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

func localHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// guard rejects requests that a web page on another site could have caused.
// The Host check defeats DNS rebinding; the Origin check defeats cross-site
// requests.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !strings.HasPrefix(origin, "http://") || !localHost(strings.TrimPrefix(origin, "http://")) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// api wraps a handler with the method and authentication checks.
func (s *Server) api(method string, fn func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			writeJSON(w, http.StatusMethodNotAllowed, apiError{"method not allowed"})
			return
		}
		c, err := r.Cookie(cookieName)
		if err != nil || !s.tokenOK(c.Value) {
			writeJSON(w, http.StatusUnauthorized, apiError{"unauthorized"})
			return
		}
		if method == http.MethodPost && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, apiError{"JSON expected"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		fn(w, r)
	}
}

type monitor struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type state struct {
	Version      string      `json:"version"`
	OS           string      `json:"os"`
	Arch         string      `json:"arch"`
	RepoURL      string      `json:"repoUrl"`
	Hostname     string      `json:"hostname"`
	Addresses    []string    `json:"addresses"`
	DesktopW     int         `json:"desktopW"`
	DesktopH     int         `json:"desktopH"`
	Monitors     []monitor   `json:"monitors"`
	ConfigPath   string      `json:"configPath"`
	ConfigExists bool        `json:"configExists"`
	ConfigError  string      `json:"configError,omitempty"`
	Config       config.Form `json:"config"`
	DefaultPort  int         `json:"defaultPort"`
	Engine       engineState `json:"engine"`
	Logs         []string    `json:"logs"`
}

// desktop returns the monitor layout, refreshed at most every ten seconds
// (detecting it can be slow on Linux).
func (s *Server) desktop() layout.Desktop {
	s.deskMu.Lock()
	defer s.deskMu.Unlock()
	if s.deskAt.IsZero() || time.Since(s.deskAt) > 10*time.Second {
		s.desk = layout.NewDesktop(input.Monitors())
		s.deskAt = time.Now()
	}
	return s.desk
}

func (s *Server) state() state {
	st := state{
		Version: s.opts.Version, OS: runtime.GOOS, Arch: runtime.GOARCH, RepoURL: s.opts.RepoURL,
		Hostname:    config.DefaultName(),
		Addresses:   LocalIPs(),
		ConfigPath:  s.opts.ConfigPath,
		DefaultPort: config.DefaultPort,
		Monitors:    []monitor{},
	}
	d := s.desktop()
	st.DesktopW, st.DesktopH = d.Bounds.W, d.Bounds.H
	for _, m := range d.Monitors {
		st.Monitors = append(st.Monitors, monitor{m.X, m.Y, m.W, m.H})
	}
	if _, err := os.Stat(s.opts.ConfigPath); err == nil {
		st.ConfigExists = true
	}
	if cfg, err := config.Load(s.opts.ConfigPath); err == nil {
		st.Config = cfg.Form()
		if cfg.Role == "server" && len(st.Config.Row) == 0 {
			st.Config.Row = []string{cfg.Name}
		}
	} else {
		if st.ConfigExists {
			st.ConfigError = err.Error()
		}
		name := config.DefaultName()
		st.Config = config.Form{
			Name: name, Port: config.DefaultPort, Row: []string{name},
			ReleaseHotkey: config.DefaultHotkey, HideCursor: true,
			Clipboard: true, ClipboardMaxMB: config.DefaultClipboardMaxMB,
			Screens: map[string]config.FormScreen{},
		}
	}
	st.Engine, st.Logs = s.engine.snapshot()
	if st.Logs == nil {
		st.Logs = []string{}
	}
	return st
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.state())
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config config.Form `json:"config"`
		Start  bool        `json:"start"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{"invalid request: " + err.Error()})
		return
	}
	cfg, err := config.Load(s.opts.ConfigPath)
	if err != nil {
		cfg = &config.Config{} // no usable file yet: start from scratch
	}
	cfg.Apply(req.Config)
	if err := config.Save(s.opts.ConfigPath, cfg); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{err.Error()})
		return
	}
	if req.Start {
		s.engine.stop()
		if err := s.engine.start(s.opts.ConfigPath); err != nil {
			writeJSON(w, http.StatusInternalServerError, apiError{err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, s.state())
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if _, err := config.Load(s.opts.ConfigPath); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{err.Error()})
		return
	}
	if err := s.engine.start(s.opts.ConfigPath); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.state())
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	s.engine.stop()
	writeJSON(w, http.StatusOK, s.state())
}

func (s *Server) handleNewKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"key": secure.NewKey()})
}

// ---------------------------------------------------------------------------
// Sending files dropped on the page
//
// A browser never tells a page where a dropped file lives on disk, so the
// page uploads the file to this server (same computer, so this is a local
// copy), which keeps it in a spool folder and then asks the engine to send
// that folder's content to another computer.

// cleanSpool removes batches that were left behind more than a day ago.
func cleanSpool(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// batchDir returns the spool folder of a batch id made by handleSendBegin.
func (s *Server) batchDir(batch string) (string, bool) {
	if len(batch) != 16 {
		return "", false
	}
	for _, c := range batch {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return "", false
		}
	}
	dir := filepath.Join(s.opts.SpoolDir, batch)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

// upload wraps a handler that receives a raw file body: same access checks
// as api, but no JSON and no size limit.
func (s *Server) upload(fn func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, apiError{"method not allowed"})
			return
		}
		c, err := r.Cookie(cookieName)
		if err != nil || !s.tokenOK(c.Value) {
			writeJSON(w, http.StatusUnauthorized, apiError{"unauthorized"})
			return
		}
		fn(w, r)
	}
}

func (s *Server) handleSendBegin(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}
	batch := hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Join(s.opts.SpoolDir, batch), 0o700); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"batch": batch})
}

func (s *Server) handleSendFile(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.batchDir(r.URL.Query().Get("batch"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiError{"unknown batch"})
		return
	}
	rel, err := xfer.CleanRel(r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{err.Error()})
		return
	}
	target := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		writeJSON(w, http.StatusConflict, apiError{"this file was already added"})
		return
	}
	n, err := io.Copy(f, r.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(target)
		writeJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"bytes": n})
}

func (s *Server) handleSendCommit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Batch string `json:"batch"`
		To    string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{"invalid request"})
		return
	}
	dir, ok := s.batchDir(req.Batch)
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiError{"unknown batch"})
		return
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) == 0 {
		os.RemoveAll(dir)
		writeJSON(w, http.StatusBadRequest, apiError{"no files to send"})
		return
	}
	// The engine sends the folder's content and removes the folder.
	cmd := map[string]string{"cmd": "send", "to": req.To, "dir": dir}
	if err := s.engine.command(cmd); err != nil {
		os.RemoveAll(dir)
		writeJSON(w, http.StatusConflict, apiError{err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSendCancel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Batch string `json:"batch"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if dir, ok := s.batchDir(req.Batch); ok {
		os.RemoveAll(dir)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// LocalIPs lists this computer's LAN addresses, private IPv4 ranges first.
func LocalIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return []string{}
	}
	private, other := []string{}, []string{}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if ip.IsPrivate() {
			private = append(private, ip.String())
		} else {
			other = append(other, ip.String())
		}
	}
	sort.Strings(private)
	sort.Strings(other)
	return append(private, other...)
}
