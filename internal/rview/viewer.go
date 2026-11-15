package rview

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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/wincap"
)

//go:embed web
var webFiles embed.FS

const viewCookie = "edgehop_view"

// Peer is a computer whose windows can be shown.
type Peer struct {
	Name string `json:"name"`
	OS   string `json:"os"`
}

// WindowList is what a computer answers when asked for its windows.
type WindowList = listReply

// ListWindows asks the source at the other end of conn for its windows.
func ListWindows(conn Conn) (WindowList, error) {
	var list WindowList
	if err := conn.Send([]byte{mList}); err != nil {
		return list, err
	}
	for {
		msg, err := conn.Recv()
		if err != nil {
			return list, err
		}
		if len(msg) > 0 && msg[0] == mWindows {
			err := json.Unmarshal(msg[1:], &list)
			return list, err
		}
	}
}

// Viewer is the local web server behind the windows that show another
// computer's windows. It listens on this computer only and wants the access
// code from its link, like the settings page.
type Viewer struct {
	// Peers lists the computers whose windows can be shown right now.
	Peers func() []Peer
	// Open opens a view connection to the named computer.
	Open func(peer string) (Conn, error)
	// Remap returns the key remapping for the named computer, or nil.
	Remap func(peer string) map[uint16]uint16

	token string
	ln    net.Listener

	mu      sync.Mutex
	streams map[string]*stream
}

// stream is one window being shown in one browser window.
type stream struct {
	conn   Conn
	sendMu sync.Mutex
	remap  map[uint16]uint16
}

func (s *stream) send(b []byte) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	return s.conn.Send(b)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Listen starts listening on a free port of this computer.
func (v *Viewer) Listen() error {
	token, err := randomHex(24)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	v.token, v.ln, v.streams = token, ln, map[string]*stream{}
	return nil
}

// URL is the link that opens the window list, including the access code.
func (v *Viewer) URL() string {
	return fmt.Sprintf("http://%s/?t=%s", v.ln.Addr(), v.token)
}

// WindowURL is a link that shows one window of a computer straight away:
// it carries the access code and leads on to the page of that window. The
// window is brought to the front over there, as it would be after a click.
func (v *Viewer) WindowURL(peer string, window uint64) string {
	page := "view.html?" + url.Values{"peer": {peer}, "win": {strconv.FormatUint(window, 10)}, "front": {"1"}, "hide": {"1"}}.Encode()
	return fmt.Sprintf("http://%s/?%s", v.ln.Addr(), url.Values{"t": {v.token}, "go": {page}}.Encode())
}

// Serve answers requests until ctx ends.
func (v *Viewer) Serve(ctx context.Context) {
	srv := &http.Server{Handler: v.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
		v.mu.Lock()
		for _, s := range v.streams {
			s.conn.Close()
		}
		v.mu.Unlock()
	}()
	srv.Serve(v.ln)
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

func (v *Viewer) tokenOK(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(v.token)) == 1
}

// Handler returns the web server: pages, window lists, pictures and input.
func (v *Viewer) Handler() http.Handler {
	if v.streams == nil {
		v.streams = map[string]*stream{}
	}
	static, _ := fs.Sub(webFiles, "web")
	files := http.FileServer(http.FS(static))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/peers", v.handlePeers)
	mux.HandleFunc("/api/windows", v.handleWindows)
	mux.HandleFunc("/api/stream", v.handleStream)
	mux.HandleFunc("/api/input", v.handleInput)
	mux.Handle("/", files)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The same defences as the settings page: no requests that another
		// web site could have caused, and nothing without the access code.
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
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")

		if t := r.URL.Query().Get("t"); t != "" && r.URL.Path == "/" && r.Method == http.MethodGet {
			if !v.tokenOK(t) {
				http.Error(w, "the access code in this link is not valid any more - open the window list from the edgehop settings page again", http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: viewCookie, Value: v.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			next := "/"
			// Only on to the page of a window, never to another site.
			if page := r.URL.Query().Get("go"); strings.HasPrefix(page, "view.html?") {
				next = "/" + page
			}
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		c, err := r.Cookie(viewCookie)
		if err != nil || !v.tokenOK(c.Value) {
			http.Error(w, "open the window list from the edgehop settings page", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func (v *Viewer) handlePeers(w http.ResponseWriter, r *http.Request) {
	peers := v.Peers()
	if peers == nil {
		peers = []Peer{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"peers": peers})
}

func (v *Viewer) handleWindows(w http.ResponseWriter, r *http.Request) {
	conn, err := v.Open(r.URL.Query().Get("peer"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer conn.Close()
	type result struct {
		list WindowList
		err  error
	}
	done := make(chan result, 1)
	go func() { list, err := ListWindows(conn); done <- result{list, err} }()
	select {
	case res := <-done:
		if res.err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": res.err.Error()})
			return
		}
		if res.list.Windows == nil {
			res.list.Windows = []wincap.Window{}
		}
		writeJSON(w, http.StatusOK, res.list)
	case <-time.After(10 * time.Second):
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "the other computer did not answer"})
	case <-r.Context().Done():
	}
}

// Messages to the browser on /api/stream: type, length (4 bytes), payload.
const (
	bHello = 1 // JSON: the id to send input under
	bMeta  = 2 // JSON: title and size
	bRect  = 3 // seq u32, x u16, y u16, w u16, h u16, format u8, last u8, then the picture
	bEnd   = 4 // text: why it ended
)

func writeMsg(w io.Writer, typ byte, parts ...[]byte) error {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	hdr := [5]byte{typ}
	be.PutUint32(hdr[1:], uint32(n))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	for _, p := range parts {
		if _, err := w.Write(p); err != nil {
			return err
		}
	}
	return nil
}

// handleStream shows one window: it forwards the pictures to the browser
// for as long as the browser keeps the request open.
func (v *Viewer) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not possible here", http.StatusInternalServerError)
		return
	}
	peer := r.URL.Query().Get("peer")
	win, err := strconv.ParseUint(r.URL.Query().Get("win"), 10, 64)
	if err != nil {
		http.Error(w, "which window?", http.StatusBadRequest)
		return
	}
	sid, err := randomHex(8)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	end := func(reason string) {
		writeMsg(w, bEnd, []byte(reason))
		flusher.Flush()
	}
	conn, err := v.Open(peer)
	if err != nil {
		end(err.Error())
		return
	}
	st := &stream{conn: conn}
	if v.Remap != nil {
		st.remap = v.Remap(peer)
	}
	v.mu.Lock()
	v.streams[sid] = st
	v.mu.Unlock()
	defer func() {
		v.mu.Lock()
		delete(v.streams, sid)
		v.mu.Unlock()
		conn.Close()
	}()
	go func() { // the browser window was closed: stop the other side too
		<-r.Context().Done()
		conn.Close()
	}()

	hello, _ := json.Marshal(map[string]string{"sid": sid})
	if writeMsg(w, bHello, hello) != nil {
		return
	}
	flusher.Flush()
	if err := st.send(be.AppendUint64([]byte{mStart}, win)); err != nil {
		end(err.Error())
		return
	}
	if r.URL.Query().Get("front") == "1" {
		st.send([]byte{mFront})
	}
	if r.URL.Query().Get("hide") == "1" {
		st.send([]byte{mHide, 1})
	}
	for {
		msg, err := conn.Recv()
		if err != nil {
			if r.Context().Err() == nil {
				end("the connection to the other computer was lost")
			}
			return
		}
		if len(msg) == 0 {
			continue
		}
		switch msg[0] {
		case mMeta:
			err = writeMsg(w, bMeta, msg[1:])
		case mRect:
			var data []byte
			if _, data, err = recvRect(conn, msg); err != nil {
				end(err.Error())
				return
			}
			// The browser gets the header without the length, which the
			// message length already tells.
			err = writeMsg(w, bRect, msg[1:rectHeaderLen-4], data)
		case mEnd:
			end(string(msg[1:]))
			return
		}
		if err != nil {
			return
		}
		flusher.Flush()
	}
}

// inputBatch is what the browser posts: a list of events, each a short list
// that starts with its kind.
type inputBatch struct {
	Events [][]json.Number `json:"ev"`
}

func num(e []json.Number, i int) (int, bool) {
	if i >= len(e) {
		return 0, false
	}
	n, err := e[i].Int64()
	return int(n), err == nil
}

func (v *Viewer) handleInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	v.mu.Lock()
	st := v.streams[r.URL.Query().Get("sid")]
	v.mu.Unlock()
	if st == nil {
		http.Error(w, "this window is not being shown any more", http.StatusGone)
		return
	}
	var batch inputBatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&batch); err != nil {
		http.Error(w, "bad input", http.StatusBadRequest)
		return
	}
	for _, e := range batch.Events {
		frame, err := encodeInput(e, st.remap)
		if err != nil {
			continue // one odd event must not hold up the others
		}
		if err := st.send(frame); err != nil {
			http.Error(w, "the connection to the other computer was lost", http.StatusGone)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

var errBadEvent = errors.New("bad event")

// Event kinds in the browser's input list. The numbers that follow are
// positions (0..65535 across the picture), buttons, wheel steps or key codes.
const (
	evPointer = 1 // x, y
	evButton  = 2 // x, y, button, down
	evWheel   = 3 // x, y, dx, dy
	evKey     = 4 // code, state
	evRelease = 5 //
	evAck     = 6 // seq
	evResize  = 7 // width, height: the size the window should take
)

func encodeInput(e []json.Number, remap map[uint16]uint16) ([]byte, error) {
	kind, ok := num(e, 0)
	if !ok {
		return nil, errBadEvent
	}
	arg := func(i, lo, hi int) (int, error) {
		n, ok := num(e, i)
		if !ok || n < lo || n > hi {
			return 0, errBadEvent
		}
		return n, nil
	}
	pos := func(typ byte) ([]byte, error) {
		x, err := arg(1, 0, 0xFFFF)
		if err != nil {
			return nil, err
		}
		y, err := arg(2, 0, 0xFFFF)
		if err != nil {
			return nil, err
		}
		return appendPos(nil, typ, uint16(x), uint16(y)), nil
	}
	switch kind {
	case evPointer:
		return pos(mPointer)
	case evButton:
		b, err := pos(mButton)
		if err != nil {
			return nil, err
		}
		button, err := arg(3, keys.ButtonLeft, keys.ButtonForward)
		if err != nil {
			return nil, err
		}
		down, err := arg(4, 0, 1)
		if err != nil {
			return nil, err
		}
		return append(b, byte(button), byte(down)), nil
	case evWheel:
		b, err := pos(mWheel)
		if err != nil {
			return nil, err
		}
		dx, err := arg(3, -32000, 32000)
		if err != nil {
			return nil, err
		}
		dy, err := arg(4, -32000, 32000)
		if err != nil {
			return nil, err
		}
		return be.AppendUint16(be.AppendUint16(b, uint16(int16(dx))), uint16(int16(dy))), nil
	case evKey:
		code, err := arg(1, 1, keys.MaxCode-1)
		if err != nil {
			return nil, err
		}
		state, err := arg(2, 0, 1)
		if err != nil {
			return nil, err
		}
		c := uint16(code)
		if to, ok := remap[c]; ok {
			c = to
		}
		return append(be.AppendUint16([]byte{mKey}, c), byte(state)), nil
	case evRelease:
		return []byte{mRelease}, nil
	case evResize:
		w, err := arg(1, 1, 0xFFFF)
		if err != nil {
			return nil, err
		}
		h, err := arg(2, 1, 0xFFFF)
		if err != nil {
			return nil, err
		}
		return be.AppendUint16(be.AppendUint16([]byte{mResize}, uint16(w)), uint16(h)), nil
	case evAck:
		seq, err := arg(1, 0, 0xFFFFFFFF)
		if err != nil {
			return nil, err
		}
		return be.AppendUint32([]byte{mAck}, uint32(seq)), nil
	}
	return nil, errBadEvent
}
