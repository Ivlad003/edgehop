// Package xfer carries clipboard content - text or files - from one computer
// to another as a sequence of frames:
//
//	Bundle   what is coming: kind, number of items, total size
//	File     one item: a file (followed by its Chunks) or a folder
//	Chunk    a piece of the current file
//	End      the bundle is complete, or was aborted
//
// Every frame starts with a type byte and the bundle id, so a relay can
// route frames without understanding them.
package xfer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Ivlad003/edgehop/internal/clip"
)

// Frame types.
const (
	FBundle = 0x20
	FFile   = 0x21
	FChunk  = 0x22
	FEnd    = 0x23
)

const (
	// ChunkSize is the payload of one Chunk frame.
	ChunkSize = 32 * 1024
	maxItems  = 20000
	maxDepth  = 32
	flagDir   = 1
	statusOK  = 0
)

var be = binary.BigEndian

// ErrTooLarge means the content exceeds the configured limit.
var ErrTooLarge = errors.New("larger than the clipboard size limit")

// Stats describes a transferred bundle.
type Stats struct {
	Kind  clip.Kind
	Files int   // files and folders, 0 for text
	Bytes int64 // payload size
}

func (s Stats) String() string {
	if s.Kind == clip.Text {
		return fmt.Sprintf("text, %s", Size(s.Bytes))
	}
	return fmt.Sprintf("%d file(s), %s", s.Files, Size(s.Bytes))
}

// Size formats a byte count for people.
func Size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// ID returns the bundle id of a frame.
func ID(frame []byte) (uint32, bool) {
	if len(frame) < 5 || frame[0] < FBundle || frame[0] > FEnd {
		return 0, false
	}
	return be.Uint32(frame[1:]), true
}

// IsEnd reports whether the frame closes its bundle.
func IsEnd(frame []byte) bool { return len(frame) >= 5 && frame[0] == FEnd }

func header(typ byte, id uint32) []byte {
	b := make([]byte, 5, 64)
	b[0] = typ
	be.PutUint32(b[1:], id)
	return b
}

// Abort returns the frame that cancels a bundle.
func Abort(id uint32) []byte { return append(header(FEnd, id), 1) }

// ---------------------------------------------------------------------------
// Sending

type item struct {
	rel  string // path inside the bundle, with forward slashes
	src  string // file on this computer
	dir  bool
	size int64
}

// manifest lists everything below the given paths.
func manifest(paths []string) ([]item, int64, error) {
	var items []item
	var total int64
	used := map[string]bool{}
	for _, top := range paths {
		info, err := os.Lstat(top)
		if err != nil {
			return nil, 0, err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			continue // links, devices ...
		}
		// Two copied items may share a name when they come from different folders.
		name, base := filepath.Base(top), filepath.Base(top)
		for n := 2; used[strings.ToLower(name)]; n++ {
			ext := filepath.Ext(base)
			name = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(base, ext), n, ext)
		}
		used[strings.ToLower(name)] = true

		err = filepath.Walk(top, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !fi.IsDir() && !fi.Mode().IsRegular() {
				return nil
			}
			sub, err := filepath.Rel(top, p)
			if err != nil {
				return err
			}
			rel := name
			if sub != "." {
				rel = name + "/" + filepath.ToSlash(sub)
			}
			it := item{rel: rel, src: p, dir: fi.IsDir()}
			if !it.dir {
				it.size = fi.Size()
				total += it.size
			}
			items = append(items, it)
			if len(items) > maxItems {
				return fmt.Errorf("more than %d files", maxItems)
			}
			return nil
		})
		if err != nil {
			return nil, 0, err
		}
	}
	return items, total, nil
}

// Send streams the content as frames through send. Nothing is sent when the
// content is larger than limit (ErrTooLarge).
func Send(send func([]byte) error, id uint32, c clip.Content, limit int64) (Stats, error) {
	st := Stats{Kind: c.Kind}
	var items []item
	switch c.Kind {
	case clip.Text:
		st.Bytes = int64(len(c.Text))
		items = []item{{size: st.Bytes}}
	case clip.Files:
		var err error
		if items, st.Bytes, err = manifest(c.Files); err != nil {
			return st, err
		}
		st.Files = len(items)
		if len(items) == 0 {
			return st, errors.New("nothing to send")
		}
	default:
		return st, errors.New("nothing to send")
	}
	if limit > 0 && st.Bytes > limit {
		return st, ErrTooLarge
	}

	b := append(header(FBundle, id), byte(c.Kind))
	b = be.AppendUint32(b, uint32(len(items)))
	b = be.AppendUint64(b, uint64(st.Bytes))
	if err := send(b); err != nil {
		return st, err
	}
	fail := func(err error) (Stats, error) {
		send(Abort(id))
		return st, err
	}
	buf := make([]byte, 5+ChunkSize)
	buf[0] = FChunk
	be.PutUint32(buf[1:], id)
	for _, it := range items {
		f := header(FFile, id)
		flags := byte(0)
		if it.dir {
			flags = flagDir
		}
		f = append(f, flags)
		f = be.AppendUint64(f, uint64(it.size))
		f = be.AppendUint16(f, uint16(len(it.rel)))
		f = append(f, it.rel...)
		if len(it.rel) > 4096 {
			return fail(fmt.Errorf("path too long: %s", it.rel))
		}
		if err := send(f); err != nil {
			return st, err
		}
		if it.dir {
			continue
		}
		var src io.Reader
		var file *os.File
		if c.Kind == clip.Text {
			src = strings.NewReader(c.Text)
		} else {
			var err error
			if file, err = os.Open(it.src); err != nil {
				return fail(err)
			}
			src = file
		}
		// Send exactly the announced size, even if the file changes meanwhile.
		err := sendBody(send, src, buf, it.size)
		if file != nil {
			file.Close()
		}
		if err != nil {
			var se sendError
			if errors.As(err, &se) {
				return st, se.error // the connection is gone; nothing more to say
			}
			return fail(fmt.Errorf("%s: %w", it.rel, err))
		}
	}
	return st, send(append(header(FEnd, id), statusOK))
}

// sendError marks a failure of the connection rather than of the source.
type sendError struct{ error }

func sendBody(send func([]byte) error, src io.Reader, buf []byte, size int64) error {
	for left := size; left > 0; {
		n, err := io.ReadFull(src, buf[5:5+min(int64(ChunkSize), left)])
		if n > 0 {
			if err := send(buf[:5+n]); err != nil {
				return sendError{err}
			}
			left -= int64(n)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Receiving

// Result is a completely received bundle.
type Result struct {
	ID      uint32
	Content clip.Content
	Stats   Stats
}

// Receiver assembles the frames of incoming bundles. Files are written below
// Dir, each bundle into its own folder; the folders of earlier bundles are
// removed when a new one starts.
type Receiver struct {
	Dir   string
	Limit int64 // 0 = no limit

	cur *incoming
}

type incoming struct {
	id      uint32
	kind    clip.Kind
	total   int64
	got     int64
	items   int
	seen    int
	root    string
	text    []byte
	file    *os.File
	left    int64
	tops    []string
	topSeen map[string]bool
}

// cleanRel turns a path from the wire into a safe relative path for this
// system. The sender is another computer: never let it pick a location
// outside the bundle folder.
func cleanRel(rel string) (string, error) {
	if rel == "" || strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("invalid path %q", rel)
	}
	parts := strings.Split(rel, "/")
	if len(parts) > maxDepth {
		return "", fmt.Errorf("path too deep: %q", rel)
	}
	for i, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("invalid path %q", rel)
		}
		// Characters some file systems (Windows) do not allow.
		p = strings.Map(func(r rune) rune {
			if r < 32 || strings.ContainsRune(`<>:"|?*\`, r) {
				return '_'
			}
			return r
		}, p)
		p = strings.TrimRight(p, " .")
		if p == "" {
			p = "_"
		}
		if len(p) > 240 {
			p = p[:240]
		}
		parts[i] = p
	}
	return path.Join(parts...), nil
}

func isBundleDir(name string) bool {
	if len(name) != 8 {
		return false
	}
	for _, c := range name {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func (r *Receiver) abort() {
	in := r.cur
	r.cur = nil
	if in == nil {
		return
	}
	if in.file != nil {
		in.file.Close()
	}
	if in.root != "" {
		os.RemoveAll(in.root)
	}
}

// Abort drops a half-received bundle, for example when the connection is lost.
func (r *Receiver) Abort() { r.abort() }

// Frame feeds one frame. It returns the bundle's content once its End frame
// has arrived, and nil while the bundle is still incomplete.
func (r *Receiver) Frame(b []byte) (*Result, error) {
	id, ok := ID(b)
	if !ok {
		return nil, errors.New("xfer: not a transfer frame")
	}
	body := b[5:]
	fail := func(format string, args ...any) (*Result, error) {
		r.abort()
		return nil, fmt.Errorf("xfer: "+format, args...)
	}
	if b[0] == FBundle {
		r.abort() // a new bundle replaces an unfinished one
		if len(body) < 13 {
			return fail("short bundle header")
		}
		in := &incoming{id: id, kind: clip.Kind(body[0]), items: int(be.Uint32(body[1:])), total: int64(be.Uint64(body[5:])), topSeen: map[string]bool{}}
		if in.kind != clip.Text && in.kind != clip.Files {
			return fail("unknown content kind %d", in.kind)
		}
		if in.total < 0 || (r.Limit > 0 && in.total > r.Limit) {
			return nil, ErrTooLarge
		}
		if in.items <= 0 || in.items > maxItems || (in.kind == clip.Text && in.items != 1) {
			return fail("invalid item count %d", in.items)
		}
		if in.kind == clip.Files {
			// Make room: earlier bundles are no longer on any clipboard. Only
			// folders this package created (eight hex digits) are removed.
			if entries, err := os.ReadDir(r.Dir); err == nil {
				for _, e := range entries {
					if isBundleDir(e.Name()) {
						os.RemoveAll(filepath.Join(r.Dir, e.Name()))
					}
				}
			}
			in.root = filepath.Join(r.Dir, fmt.Sprintf("%08x", id))
			if err := os.MkdirAll(in.root, 0o755); err != nil {
				return fail("%v", err)
			}
		}
		r.cur = in
		return nil, nil
	}

	in := r.cur
	if in == nil || in.id != id {
		return nil, nil // frames of a bundle that was refused or aborted
	}
	switch b[0] {
	case FFile:
		if in.file != nil || in.left != 0 {
			return fail("file started before the previous one ended")
		}
		if len(body) < 11 {
			return fail("short file header")
		}
		flags, size := body[0], int64(be.Uint64(body[1:]))
		n := int(be.Uint16(body[9:]))
		if len(body) != 11+n {
			return fail("malformed file header")
		}
		in.seen++
		if in.seen > in.items || size < 0 || in.got+size > in.total {
			return fail("more data than announced")
		}
		if in.kind == clip.Text {
			in.text = make([]byte, 0, size)
			in.left = size
			return nil, nil
		}
		rel, err := cleanRel(string(body[11:]))
		if err != nil {
			return fail("%v", err)
		}
		target := filepath.Join(in.root, filepath.FromSlash(rel))
		top := filepath.Join(in.root, strings.SplitN(rel, "/", 2)[0])
		if !in.topSeen[top] {
			in.topSeen[top] = true
			in.tops = append(in.tops, top)
		}
		if flags&flagDir != 0 {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fail("%v", err)
			}
			return nil, nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fail("%v", err)
		}
		// O_EXCL: a bundle must not overwrite one of its own files.
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fail("%v", err)
		}
		if size == 0 {
			f.Close()
			return nil, nil
		}
		in.file, in.left = f, size
	case FChunk:
		if int64(len(body)) > in.left {
			return fail("more data than announced")
		}
		if in.kind == clip.Text {
			in.text = append(in.text, body...)
		} else {
			if in.file == nil {
				return fail("data without a file")
			}
			if _, err := in.file.Write(body); err != nil {
				return fail("%v", err)
			}
		}
		in.left -= int64(len(body))
		in.got += int64(len(body))
		if in.left == 0 && in.file != nil {
			err := in.file.Close()
			in.file = nil
			if err != nil {
				return fail("%v", err)
			}
		}
	case FEnd:
		if len(body) < 1 || body[0] != statusOK {
			r.abort()
			return nil, errors.New("xfer: the sender cancelled the transfer")
		}
		if in.left != 0 || in.seen != in.items || in.got != in.total {
			return fail("the transfer ended early")
		}
		r.cur = nil
		res := &Result{ID: id, Stats: Stats{Kind: in.kind, Bytes: in.got}}
		if in.kind == clip.Text {
			res.Content = clip.Content{Kind: clip.Text, Text: string(in.text)}
		} else {
			res.Stats.Files = in.items
			res.Content = clip.Content{Kind: clip.Files, Files: in.tops}
		}
		return res, nil
	}
	return nil, nil
}
