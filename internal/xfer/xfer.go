// Package xfer carries content from one computer to another as a sequence of
// frames: clipboard text, clipboard files, or files sent on purpose ("drops").
//
//	Bundle   what is coming: kind, number of items, total size
//	File     one item: a file (followed by its Chunks) or a folder
//	Chunk    a piece of the current file
//	End      the bundle is complete, or was aborted
//
// Every frame starts with a type byte and the bundle id, so a relay can
// route frames without understanding them, and several bundles can be on
// their way over one connection at the same time.
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

// Bundle kinds on the wire. The first two equal clip.Text and clip.Files.
const (
	kindText  = 1
	kindFiles = 2
	kindDrop  = 3 // files sent on purpose: saved to a folder, not put on a clipboard
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

// EndOK reports whether an End frame closes its bundle successfully.
func EndOK(frame []byte) bool { return IsEnd(frame) && len(frame) > 5 && frame[5] == statusOK }

// DropDest reports whether the frame opens a drop bundle and, if so, which
// screen it is addressed to ("" means the computer at the other end).
func DropDest(frame []byte) (dest string, isDrop bool) {
	if len(frame) < 18 || frame[0] != FBundle || frame[5] != kindDrop {
		return "", false
	}
	rest := frame[18:]
	if len(rest) >= 1 && len(rest) >= 1+int(rest[0]) {
		dest = string(rest[1 : 1+int(rest[0])])
	}
	return dest, true
}

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
		// Two items may share a name when they come from different folders.
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

// Send streams clipboard content as frames through send. Nothing is sent
// when the content is larger than limit (ErrTooLarge).
func Send(send func([]byte) error, id uint32, c clip.Content, limit int64) (Stats, error) {
	switch c.Kind {
	case clip.Text:
		return stream(send, id, kindText, "", c.Text, nil, limit)
	case clip.Files:
		return stream(send, id, kindFiles, "", "", c.Files, limit)
	}
	return Stats{Kind: c.Kind}, errors.New("nothing to send")
}

// SendDrop streams files that the user sends on purpose. dest names the
// screen they are meant for; "" means the computer at the other end of the
// connection. There is no size limit: the user chose these files.
func SendDrop(send func([]byte) error, id uint32, paths []string, dest string) (Stats, error) {
	return stream(send, id, kindDrop, dest, "", paths, 0)
}

func stream(send func([]byte) error, id uint32, kind byte, dest, text string, paths []string, limit int64) (Stats, error) {
	st := Stats{Kind: clip.Files}
	var items []item
	if kind == kindText {
		st.Kind = clip.Text
		st.Bytes = int64(len(text))
		items = []item{{size: st.Bytes}}
	} else {
		var err error
		if items, st.Bytes, err = manifest(paths); err != nil {
			return st, err
		}
		st.Files = len(items)
		if len(items) == 0 {
			return st, errors.New("nothing to send")
		}
	}
	if limit > 0 && st.Bytes > limit {
		return st, ErrTooLarge
	}
	if len(dest) > 255 {
		return st, errors.New("destination name too long")
	}

	b := append(header(FBundle, id), kind)
	b = be.AppendUint32(b, uint32(len(items)))
	b = be.AppendUint64(b, uint64(st.Bytes))
	if kind == kindDrop {
		b = append(b, byte(len(dest)))
		b = append(b, dest...)
	}
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
		if len(it.rel) > 4096 {
			return fail(fmt.Errorf("path too long: %s", it.rel))
		}
		f := header(FFile, id)
		flags := byte(0)
		if it.dir {
			flags = flagDir
		}
		f = append(f, flags)
		f = be.AppendUint64(f, uint64(it.size))
		f = be.AppendUint16(f, uint16(len(it.rel)))
		f = append(f, it.rel...)
		if err := send(f); err != nil {
			return st, err
		}
		if it.dir {
			continue
		}
		var src io.Reader
		var file *os.File
		if kind == kindText {
			src = strings.NewReader(text)
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
	// Drop is set for files that were sent on purpose: they are already in
	// their final place (Content.Files) and do not belong on the clipboard.
	Drop bool
}

// Receiver assembles the frames of incoming bundles.
//
// Clipboard files are written below Dir, each bundle into its own folder;
// the folders of earlier bundles are removed when a new one starts. Dropped
// files end up directly in DropDir and are never removed.
type Receiver struct {
	Dir     string
	DropDir string // "" = drops are refused
	Limit   int64  // largest clipboard content; 0 = no limit

	active map[uint32]*incoming
}

type incoming struct {
	id      uint32
	kind    byte
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

// CleanRel turns a path from another computer into a safe relative path
// (with forward slashes) for this system. Never let the other side pick a
// location outside the folder the content is meant for.
func CleanRel(rel string) (string, error) {
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

func (r *Receiver) drop(id uint32) {
	in := r.active[id]
	if in == nil {
		return
	}
	delete(r.active, id)
	if in.file != nil {
		in.file.Close()
	}
	if in.root != "" {
		os.RemoveAll(in.root)
	}
}

// Cancel drops one half-received bundle.
func (r *Receiver) Cancel(id uint32) { r.drop(id) }

// Abort drops every half-received bundle, for example when the connection
// is lost.
func (r *Receiver) Abort() {
	for id := range r.active {
		r.drop(id)
	}
}

// place moves a received item into dir under a name that is still free.
func place(src, dir string) (string, error) {
	base := filepath.Base(src)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for n := 1; n < 10000; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		dst := filepath.Join(dir, name)
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			return "", err
		}
		return dst, nil
	}
	return "", fmt.Errorf("no free name for %s", base)
}

// Frame feeds one frame. It returns the bundle's content once its End frame
// has arrived, and nil while the bundle is still incomplete.
func (r *Receiver) Frame(b []byte) (*Result, error) {
	id, ok := ID(b)
	if !ok {
		return nil, errors.New("xfer: not a transfer frame")
	}
	body := b[5:]
	fail := func(format string, args ...any) (*Result, error) {
		r.drop(id)
		return nil, fmt.Errorf("xfer: "+format, args...)
	}
	if b[0] == FBundle {
		r.drop(id) // a bundle that starts again replaces its unfinished self
		if len(body) < 13 {
			return fail("short bundle header")
		}
		in := &incoming{id: id, kind: body[0], items: int(be.Uint32(body[1:])), total: int64(be.Uint64(body[5:])), topSeen: map[string]bool{}}
		if in.kind != kindText && in.kind != kindFiles && in.kind != kindDrop {
			return fail("unknown content kind %d", in.kind)
		}
		if in.total < 0 || (in.kind != kindDrop && r.Limit > 0 && in.total > r.Limit) {
			return nil, ErrTooLarge
		}
		if in.items <= 0 || in.items > maxItems || (in.kind == kindText && in.items != 1) {
			return fail("invalid item count %d", in.items)
		}
		switch in.kind {
		case kindFiles:
			// Make room: earlier bundles are no longer on any clipboard. Only
			// folders this package created (eight hex digits) are removed,
			// and never those of bundles still on their way.
			busy := map[string]bool{}
			for _, other := range r.active {
				busy[filepath.Base(other.root)] = true
			}
			if entries, err := os.ReadDir(r.Dir); err == nil {
				for _, e := range entries {
					if isBundleDir(e.Name()) && !busy[e.Name()] {
						os.RemoveAll(filepath.Join(r.Dir, e.Name()))
					}
				}
			}
			in.root = filepath.Join(r.Dir, fmt.Sprintf("%08x", id))
		case kindDrop:
			if r.DropDir == "" {
				return fail("this computer does not accept sent files")
			}
			in.root = filepath.Join(r.DropDir, fmt.Sprintf(".edgehop-incoming-%08x", id))
			os.RemoveAll(in.root)
		}
		if in.root != "" {
			if err := os.MkdirAll(in.root, 0o755); err != nil {
				return fail("%v", err)
			}
		}
		if r.active == nil {
			r.active = map[uint32]*incoming{}
		}
		r.active[id] = in
		return nil, nil
	}

	in := r.active[id]
	if in == nil {
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
		if in.kind == kindText {
			in.text = make([]byte, 0, size)
			in.left = size
			return nil, nil
		}
		rel, err := CleanRel(string(body[11:]))
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
		if in.kind == kindText {
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
			r.drop(id)
			return nil, errors.New("xfer: the sender cancelled the transfer")
		}
		if in.left != 0 || in.seen != in.items || in.got != in.total {
			return fail("the transfer ended early")
		}
		res := &Result{ID: id, Stats: Stats{Kind: clip.Files, Bytes: in.got, Files: in.items}}
		switch in.kind {
		case kindText:
			res.Stats = Stats{Kind: clip.Text, Bytes: in.got}
			res.Content = clip.Content{Kind: clip.Text, Text: string(in.text)}
		case kindFiles:
			res.Content = clip.Content{Kind: clip.Files, Files: in.tops}
		case kindDrop:
			// Move the finished items next to earlier ones, under free names.
			var placed []string
			for _, top := range in.tops {
				dst, err := place(top, r.DropDir)
				if err != nil {
					return fail("%v", err)
				}
				placed = append(placed, dst)
			}
			os.RemoveAll(in.root)
			res.Drop = true
			res.Content = clip.Content{Kind: clip.Files, Files: placed}
		}
		delete(r.active, id)
		return res, nil
	}
	return nil, nil
}
