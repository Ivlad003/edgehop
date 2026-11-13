package clip

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This test uses the real system clipboard of the computer it runs on (and
// overwrites it), so it is opt-in:
//
//	EDGEHOP_LIVE=1 go test ./internal/clip/ -run Live -v

func same(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ra, rb)
	}
	return ra == rb
}

func TestLiveClipboard(t *testing.T) {
	if os.Getenv("EDGEHOP_LIVE") != "1" {
		t.Skip("set EDGEHOP_LIVE=1 to test against the real system clipboard (it gets overwritten)")
	}
	cb, err := New()
	if err != nil {
		t.Fatalf("no clipboard: %v", err)
	}
	t.Logf("clipboard: %s", cb.Describe())

	marks := map[string]bool{}
	mark := func(what string) {
		t.Helper()
		m, err := cb.Mark()
		if err != nil {
			t.Fatalf("Mark after %s: %v", what, err)
		}
		if marks[m] {
			t.Errorf("the mark did not change after %s", what)
		}
		marks[m] = true
	}
	mark("start")

	// Text, including letters outside ASCII and several lines.
	for _, text := range []string{"edgehop: привіт, світ 👋", "line one\nline two\ttabbed", strings.Repeat("довгий текст ", 5000)} {
		if err := cb.WriteText(text); err != nil {
			t.Fatalf("WriteText: %v", err)
		}
		got, err := cb.Read()
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.Kind != Text || got.Text != text {
			t.Fatalf("text did not survive the clipboard:\n got kind %d, %d bytes: %.60q\nwant %d bytes: %.60q", got.Kind, len(got.Text), got.Text, len(text), text)
		}
		mark("writing text")
	}
	// Reading does not change anything.
	before, _ := cb.Mark()
	cb.Read()
	if after, _ := cb.Mark(); after != before {
		t.Errorf("reading changed the mark: %s -> %s", before, after)
	}

	// Files: a file with a non-ASCII name and a folder.
	dir := t.TempDir()
	file := filepath.Join(dir, "звіт 2026.txt")
	folder := filepath.Join(dir, "photos")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cb.WriteFiles([]string{file, folder}); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}
	got, err := cb.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Kind != Files || len(got.Files) != 2 || !same(t, got.Files[0], file) || !same(t, got.Files[1], folder) {
		t.Fatalf("files did not survive the clipboard:\n got kind %d %q\nwant %q", got.Kind, got.Files, []string{file, folder})
	}
	mark("writing files")

	// Text replaces files again.
	if err := cb.WriteText("after the files"); err != nil {
		t.Fatal(err)
	}
	if got, _ := cb.Read(); got.Kind != Text || got.Text != "after the files" {
		t.Fatalf("after files, text reads back as kind %d %q %q", got.Kind, got.Text, got.Files)
	}
	mark("replacing files with text")
}
