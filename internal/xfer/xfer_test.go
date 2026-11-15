package xfer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ivlad003/edgehop/internal/clip"
)

// pipe sends content and feeds every frame to a receiver.
func pipe(t *testing.T, r *Receiver, id uint32, c clip.Content, limit int64) (*Result, Stats, error) {
	t.Helper()
	var res *Result
	var recvErr error
	st, err := Send(func(b []byte) error {
		got, e := r.Frame(append([]byte(nil), b...))
		if e != nil && recvErr == nil {
			recvErr = e
		}
		if got != nil {
			res = got
		}
		return nil
	}, id, c, limit)
	if err == nil {
		err = recvErr
	}
	return res, st, err
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestText(t *testing.T) {
	r := &Receiver{Dir: t.TempDir()}
	for _, text := range []string{"привіт, світ 👋", strings.Repeat("0123456789", 20000), "x"} {
		res, st, err := pipe(t, r, 7, clip.Content{Kind: clip.Text, Text: text}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if res == nil || res.ID != 7 || res.Content.Kind != clip.Text || res.Content.Text != text {
			t.Fatalf("text of %d bytes did not arrive intact", len(text))
		}
		if st.Bytes != int64(len(text)) || res.Stats.Bytes != st.Bytes {
			t.Errorf("stats: sent %+v received %+v", st, res.Stats)
		}
	}
}

func TestFilesAndFolders(t *testing.T) {
	src := t.TempDir()
	big := strings.Repeat("ABCDEFGH", 9000) // more than two chunks
	write(t, filepath.Join(src, "звіт.txt"), "вміст")
	write(t, filepath.Join(src, "empty.bin"), "")
	write(t, filepath.Join(src, "проєкт", "a.txt"), "aaa")
	write(t, filepath.Join(src, "проєкт", "sub", "deep", "big.dat"), big)
	if err := os.MkdirAll(filepath.Join(src, "проєкт", "empty dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A second item with the same name from another folder.
	write(t, filepath.Join(src, "other", "звіт.txt"), "інший")

	dst := t.TempDir()
	r := &Receiver{Dir: dst}
	res, st, err := pipe(t, r, 0xAB, clip.Content{Kind: clip.Files, Files: []string{
		filepath.Join(src, "звіт.txt"),
		filepath.Join(src, "empty.bin"),
		filepath.Join(src, "проєкт"),
		filepath.Join(src, "other", "звіт.txt"),
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dst, "000000ab")
	want := []string{
		filepath.Join(root, "звіт.txt"),
		filepath.Join(root, "empty.bin"),
		filepath.Join(root, "проєкт"),
		filepath.Join(root, "звіт (2).txt"),
	}
	if res == nil || res.Content.Kind != clip.Files || strings.Join(res.Content.Files, "|") != strings.Join(want, "|") {
		t.Fatalf("top-level items:\n got %v\nwant %v", res.Content.Files, want)
	}
	if read(t, want[0]) != "вміст" || read(t, want[1]) != "" || read(t, want[3]) != "інший" {
		t.Error("file contents differ")
	}
	if read(t, filepath.Join(root, "проєкт", "a.txt")) != "aaa" || read(t, filepath.Join(root, "проєкт", "sub", "deep", "big.dat")) != big {
		t.Error("nested file contents differ")
	}
	if fi, err := os.Stat(filepath.Join(root, "проєкт", "empty dir")); err != nil || !fi.IsDir() {
		t.Error("the empty folder was not created")
	}
	wantBytes := int64(len("вміст") + len("aaa") + len(big) + len("інший"))
	if st.Bytes != wantBytes || res.Stats.Bytes != wantBytes {
		t.Errorf("bytes: sent %d received %d want %d", st.Bytes, res.Stats.Bytes, wantBytes)
	}

	// The next bundle replaces the previous one on disk, but leaves anything
	// else in the folder alone.
	write(t, filepath.Join(dst, "keep.txt"), "not ours")
	if _, _, err := pipe(t, r, 0xAC, clip.Content{Kind: clip.Files, Files: []string{filepath.Join(src, "empty.bin")}}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Error("the previous bundle was not removed")
	}
	if read(t, filepath.Join(dst, "keep.txt")) != "not ours" {
		t.Error("a foreign file was removed")
	}
}

func TestLimit(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "a.bin"), strings.Repeat("x", 5000))
	sent := 0
	_, err := Send(func([]byte) error { sent++; return nil }, 1,
		clip.Content{Kind: clip.Files, Files: []string{filepath.Join(src, "a.bin")}}, 4999)
	if !errors.Is(err, ErrTooLarge) || sent != 0 {
		t.Fatalf("over the limit: err=%v, frames sent=%d (must be none)", err, sent)
	}
	if _, err := Send(func([]byte) error { return nil }, 1,
		clip.Content{Kind: clip.Files, Files: []string{filepath.Join(src, "a.bin")}}, 5000); err != nil {
		t.Fatalf("exactly at the limit: %v", err)
	}
	// The receiver enforces its own limit, whatever the sender announces.
	r := &Receiver{Dir: t.TempDir(), Limit: 100}
	res, _, err := pipe(t, r, 2, clip.Content{Kind: clip.Text, Text: strings.Repeat("y", 101)}, 0)
	if !errors.Is(err, ErrTooLarge) || res != nil {
		t.Fatalf("receiver limit: res=%v err=%v", res, err)
	}
}

// frames builds raw frames the way a hostile or broken sender might.
func bundle(id uint32, kind clip.Kind, items uint32, total uint64) []byte {
	b := append(header(FBundle, id), byte(kind))
	b = be.AppendUint32(b, items)
	return be.AppendUint64(b, total)
}

func file(id uint32, flags byte, size uint64, rel string) []byte {
	b := append(header(FFile, id), flags)
	b = be.AppendUint64(b, size)
	b = be.AppendUint16(b, uint16(len(rel)))
	return append(b, rel...)
}

func chunk(id uint32, data string) []byte { return append(header(FChunk, id), data...) }

func TestHostilePathsAreRefused(t *testing.T) {
	for _, rel := range []string{"../evil.txt", "/etc/passwd", "a/../../evil", "a//b", ".", "..", "a/./b", "", "a\x00b"} {
		dst := t.TempDir()
		outside := filepath.Join(filepath.Dir(dst), "evil.txt")
		r := &Receiver{Dir: dst}
		if _, err := r.Frame(bundle(1, clip.Files, 1, 4)); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Frame(file(1, 0, 4, rel)); err == nil {
			t.Errorf("path %q was accepted", rel)
		}
		if _, err := os.Stat(outside); err == nil {
			t.Fatalf("path %q wrote outside the bundle folder", rel)
		}
		if entries, _ := os.ReadDir(dst); len(entries) != 0 {
			t.Errorf("path %q: a refused bundle left files behind", rel)
		}
	}
	// Characters that are legal on one system but not on another are replaced.
	got, err := CleanRel(`dir/a:b?c*.txt`)
	if err != nil || got != "dir/a_b_c_.txt" {
		t.Errorf("cleanRel = %q, %v", got, err)
	}
	if got, _ := CleanRel(`C:\Windows\x.dll`); strings.ContainsAny(got, `:\`) {
		t.Errorf("a Windows path kept its separators: %q", got)
	}
}

func TestMalformedBundles(t *testing.T) {
	dst := t.TempDir()
	r := &Receiver{Dir: dst}
	steps := map[string][][]byte{
		"more bytes than announced": {bundle(1, clip.Text, 1, 3), file(1, 0, 3, ""), chunk(1, "abcd")},
		"file bigger than bundle":   {bundle(1, clip.Files, 1, 3), file(1, 0, 10, "a")},
		"more items than announced": {bundle(1, clip.Files, 1, 0), file(1, 0, 0, "a"), file(1, 0, 0, "b")},
		"ends early":                {bundle(1, clip.Text, 1, 3), file(1, 0, 3, ""), chunk(1, "ab"), append(header(FEnd, 1), 0)},
		"same file twice":           {bundle(1, clip.Files, 2, 0), file(1, 0, 0, "a"), file(1, 0, 0, "a")},
		"sender cancelled":          {bundle(1, clip.Files, 1, 3), file(1, 0, 3, "a"), chunk(1, "ab"), Abort(1)},
		"unknown kind":              {bundle(1, 9, 1, 0)},
		"text with two items":       {bundle(1, clip.Text, 2, 0)},
	}
	for name, frames := range steps {
		var err error
		var res *Result
		for _, f := range frames {
			if res, err = r.Frame(f); err != nil {
				break
			}
		}
		if err == nil || res != nil {
			t.Errorf("%s: accepted (res=%v)", name, res)
		}
		if entries, _ := os.ReadDir(dst); len(entries) != 0 {
			t.Errorf("%s: left %d entries behind", name, len(entries))
		}
	}
	// Frames of a bundle nobody announced are ignored, not fatal.
	if res, err := r.Frame(chunk(99, "stray")); res != nil || err != nil {
		t.Errorf("stray chunk: %v %v", res, err)
	}
	if _, err := r.Frame([]byte{1, 2, 3}); err == nil {
		t.Error("a non-transfer frame must be refused")
	}
}

func TestSendAbortsWhenAFileDisappears(t *testing.T) {
	src := t.TempDir()
	p := filepath.Join(src, "gone.txt")
	write(t, p, "data")
	var frames [][]byte
	_, err := Send(func(b []byte) error {
		frames = append(frames, append([]byte(nil), b...))
		if b[0] == FBundle {
			os.Remove(p) // the file vanishes after it was counted
		}
		return nil
	}, 5, clip.Content{Kind: clip.Files, Files: []string{p}}, 0)
	if err == nil {
		t.Fatal("expected an error")
	}
	last := frames[len(frames)-1]
	if !IsEnd(last) || !bytes.Equal(last, Abort(5)) {
		t.Fatalf("the transfer was not cancelled properly; last frame %v", last)
	}
	if id, ok := ID(last); !ok || id != 5 {
		t.Errorf("ID = %d, %v", id, ok)
	}
}

func TestSizeText(t *testing.T) {
	for n, want := range map[int64]string{12: "12 bytes", 2048: "2.0 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB"} {
		if got := Size(n); got != want {
			t.Errorf("Size(%d) = %q, want %q", n, got, want)
		}
	}
}

// capture collects the frames a sender produces.
func capture(t *testing.T, send func(func([]byte) error) error) [][]byte {
	t.Helper()
	var frames [][]byte
	if err := send(func(b []byte) error {
		frames = append(frames, append([]byte(nil), b...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return frames
}

func TestDropsLandInTheDropFolder(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "звіт.txt"), "перший")
	write(t, filepath.Join(src, "album", "1.jpg"), "jpeg")
	drops := t.TempDir()
	write(t, filepath.Join(drops, "звіт.txt"), "already here") // must not be overwritten
	r := &Receiver{Dir: t.TempDir(), DropDir: drops, Limit: 1} // the clipboard limit does not apply to drops

	frames := capture(t, func(send func([]byte) error) error {
		_, err := SendDrop(send, 0x10, []string{filepath.Join(src, "звіт.txt"), filepath.Join(src, "album")}, "mac")
		return err
	})
	if dest, isDrop := DropDest(frames[0]); !isDrop || dest != "mac" {
		t.Fatalf("DropDest = %q, %v", dest, isDrop)
	}
	var res *Result
	for _, f := range frames {
		got, err := r.Frame(f)
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			res = got
		}
	}
	if res == nil || !res.Drop || len(res.Content.Files) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if res.Content.Files[0] != filepath.Join(drops, "звіт (2).txt") || res.Content.Files[1] != filepath.Join(drops, "album") {
		t.Fatalf("placed at %v", res.Content.Files)
	}
	if read(t, filepath.Join(drops, "звіт.txt")) != "already here" || read(t, res.Content.Files[0]) != "перший" {
		t.Error("an existing file was overwritten or the new one is wrong")
	}
	if read(t, filepath.Join(drops, "album", "1.jpg")) != "jpeg" {
		t.Error("the folder did not arrive")
	}
	entries, _ := os.ReadDir(drops)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".edgehop-incoming") {
			t.Errorf("temporary folder %s was left behind", e.Name())
		}
	}

	// A second drop of the same items gets new names instead of replacing them.
	for _, f := range capture(t, func(send func([]byte) error) error {
		_, err := SendDrop(send, 0x11, []string{filepath.Join(src, "album")}, "")
		return err
	}) {
		if got, err := r.Frame(f); err != nil {
			t.Fatal(err)
		} else if got != nil && got.Content.Files[0] != filepath.Join(drops, "album (2)") {
			t.Errorf("second drop placed at %v", got.Content.Files)
		}
	}

	// A text bundle is not a drop.
	if _, isDrop := DropDest(bundle(1, clip.Text, 1, 3)); isDrop {
		t.Error("a text bundle was taken for a drop")
	}
	// A computer without a drop folder refuses drops.
	closed := &Receiver{Dir: t.TempDir()}
	if _, err := closed.Frame(frames[0]); err == nil {
		t.Error("a drop was accepted without a drop folder")
	}
}

// Two bundles whose frames arrive interleaved (a long file transfer and a
// clipboard text, say) must both complete.
func TestInterleavedBundles(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "big.bin"), strings.Repeat("Z", 3*ChunkSize+17))
	r := &Receiver{Dir: t.TempDir(), DropDir: t.TempDir()}
	files := capture(t, func(send func([]byte) error) error {
		_, err := Send(send, 1, clip.Content{Kind: clip.Files, Files: []string{filepath.Join(src, "big.bin")}}, 0)
		return err
	})
	text := capture(t, func(send func([]byte) error) error {
		_, err := Send(send, 2, clip.Content{Kind: clip.Text, Text: "meanwhile"}, 0)
		return err
	})
	drop := capture(t, func(send func([]byte) error) error {
		_, err := SendDrop(send, 3, []string{filepath.Join(src, "big.bin")}, "")
		return err
	})
	// A second clipboard-files bundle starts while the first is unfinished:
	// its clean-up must not remove the folder of the first.
	other := capture(t, func(send func([]byte) error) error {
		_, err := Send(send, 4, clip.Content{Kind: clip.Files, Files: []string{filepath.Join(src, "big.bin")}}, 0)
		return err
	})
	done := map[uint32]*Result{}
	queues := [][][]byte{files, text, drop, other}
	for remaining := true; remaining; {
		remaining = false
		for i := range queues {
			if len(queues[i]) == 0 {
				continue
			}
			remaining = true
			res, err := r.Frame(queues[i][0])
			queues[i] = queues[i][1:]
			if err != nil {
				t.Fatalf("stream %d: %v", i+1, err)
			}
			if res != nil {
				done[res.ID] = res
			}
		}
	}
	if len(done) != 4 {
		t.Fatalf("%d of 4 bundles completed", len(done))
	}
	if done[2].Content.Text != "meanwhile" || !done[3].Drop {
		t.Errorf("text %q, drop %v", done[2].Content.Text, done[3].Drop)
	}
	for _, id := range []uint32{1, 3, 4} {
		if got := read(t, done[id].Content.Files[0]); len(got) != 3*ChunkSize+17 {
			t.Errorf("bundle %d: file has %d bytes", id, len(got))
		}
	}
	// Abort removes whatever is still unfinished.
	r.Frame(bundle(9, clip.Files, 1, 5))
	r.Frame(file(9, 0, 5, "half.txt"))
	r.Frame(bundle(10, 3, 1, 5))
	r.Abort()
	if _, err := os.Stat(filepath.Join(r.Dir, "00000009")); !os.IsNotExist(err) {
		t.Error("Abort left a clipboard bundle folder behind")
	}
	if _, err := os.Stat(filepath.Join(r.DropDir, ".edgehop-incoming-0000000a")); !os.IsNotExist(err) {
		t.Error("Abort left a drop folder behind")
	}
}
