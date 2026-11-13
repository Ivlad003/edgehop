package server_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ivlad003/edgehop/internal/client"
	"github.com/Ivlad003/edgehop/internal/clip"
	"github.com/Ivlad003/edgehop/internal/layout"
	"github.com/Ivlad003/edgehop/internal/server"
)

// clipRig is a server with a clipboard plus clients with their own.
type clipRig struct {
	*rig
	clip *clip.Memory
	dir  string
}

func startClipServer(t *testing.T, row []string, limit int64) *clipRig {
	t.Helper()
	mem := &clip.Memory{}
	dir := t.TempDir()
	r := startServerWith(t, "desk", row, nil, false, func(c *server.Config) {
		c.Clipboard, c.ClipDir, c.ClipLimit = mem, dir, limit
	})
	return &clipRig{rig: r, clip: mem, dir: dir}
}

// join connects a client that shares its clipboard.
func (r *clipRig) join(name string) (*fakeInjector, *clip.Memory, string) {
	r.t.Helper()
	mem := &clip.Memory{}
	dir := r.t.TempDir()
	inj, _ := r.connectWith(name, 1000, 500, func(c *client.Config) {
		c.Clipboard, c.ClipDir = mem, dir
	})
	return inj, mem, dir
}

// arrivesOn runs act (which moves the cursor) and waits until a clipboard
// transfer has been completely applied on the named client and ok holds.
// "Completely" matters: only then may the test copy something new there.
func (r *clipRig) arrivesOn(name, what string, act func(), ok func() bool) {
	r.t.Helper()
	c := r.clients[name]
	before := c.ClipboardTransfers()
	act()
	eventually(r.t, what, func() bool { return c.ClipboardTransfers() > before && ok() })
}

// arrivesOnServer is arrivesOn for the server's own clipboard.
func (r *clipRig) arrivesOnServer(what string, act func(), ok func() bool) {
	r.t.Helper()
	before := r.srv.ClipboardTransfers()
	act()
	eventually(r.t, what, func() bool { return r.srv.ClipboardTransfers() > before && ok() })
}

func text(m *clip.Memory) string {
	c, _ := m.Read()
	if c.Kind != clip.Text {
		return ""
	}
	return c.Text
}

func mark(m *clip.Memory) string {
	s, _ := m.Mark()
	return s
}

func TestClipboardTextFollowsTheCursor(t *testing.T) {
	r := startClipServer(t, []string{"desk", "mac"}, 0)
	mac, macClip, _ := r.join("mac")
	toMac := func() {
		r.cap.edge(layout.Right, 0.5)
		r.waitFocus("mac")
	}
	toDesk := func() {
		r.cap.move(-10, 0)
		r.waitFocus("")
	}

	// Copy on the server, move to the client: it is there to paste.
	r.clip.WriteText("скопійовано на сервері")
	r.arrivesOn("mac", "the server's text on the client", toMac,
		func() bool { return text(macClip) == "скопійовано на сервері" })
	waitPos(t, mac, 2, 250)

	// Copy on the client, move back: it is on the server.
	macClip.WriteText("copied on the mac")
	r.arrivesOnServer("the client's text on the server", toDesk,
		func() bool { return text(r.clip) == "copied on the mac" })
	if !strings.Contains(r.srv.ClipboardStatus(), "mac -> desk") {
		t.Errorf("status = %q", r.srv.ClipboardStatus())
	}

	// Nothing new was copied: moving back and forth must not rewrite anything.
	before := mark(macClip) + "/" + mark(r.clip)
	transfers := r.srv.ClipboardTransfers() + r.clients["mac"].ClipboardTransfers()
	toMac()
	toDesk()
	time.Sleep(150 * time.Millisecond)
	if after := mark(macClip) + "/" + mark(r.clip); after != before {
		t.Errorf("clipboards were rewritten without a new copy: %s -> %s", before, after)
	}
	if now := r.srv.ClipboardTransfers() + r.clients["mac"].ClipboardTransfers(); now != transfers {
		t.Errorf("%d transfer(s) happened without a new copy", now-transfers)
	}
}

func TestClipboardFilesAreTransferred(t *testing.T) {
	r := startClipServer(t, []string{"desk", "mac"}, 0)
	_, macClip, macDir := r.join("mac")

	src := t.TempDir()
	big := strings.Repeat("0123456789abcdef", 8000) // several chunks
	os.WriteFile(filepath.Join(src, "звіт.txt"), []byte("вміст звіту"), 0o644)
	os.MkdirAll(filepath.Join(src, "photos", "2026"), 0o755)
	os.WriteFile(filepath.Join(src, "photos", "2026", "big.bin"), []byte(big), 0o644)

	// Files copied on the server arrive on the client as files.
	r.clip.WriteFiles([]string{filepath.Join(src, "звіт.txt"), filepath.Join(src, "photos")})
	var got clip.Content
	r.arrivesOn("mac", "the files on the client", func() {
		r.cap.edge(layout.Right, 0.5)
		r.waitFocus("mac")
	}, func() bool {
		got, _ = macClip.Read()
		return got.Kind == clip.Files && len(got.Files) == 2
	})
	for _, p := range got.Files {
		if !strings.HasPrefix(p, macDir) {
			t.Fatalf("received file %s is outside the client's folder %s", p, macDir)
		}
	}
	if data, _ := os.ReadFile(got.Files[0]); string(data) != "вміст звіту" || filepath.Base(got.Files[0]) != "звіт.txt" {
		t.Errorf("first file: %s = %q", got.Files[0], data)
	}
	if data, _ := os.ReadFile(filepath.Join(got.Files[1], "2026", "big.bin")); string(data) != big {
		t.Errorf("the nested file differs (%d bytes)", len(data))
	}

	// And the other way: files copied on the client arrive on the server.
	other := filepath.Join(t.TempDir(), "from-mac.txt")
	os.WriteFile(other, []byte("made on the mac"), 0o644)
	macClip.WriteFiles([]string{other})
	r.arrivesOnServer("the client's file on the server", func() {
		r.cap.move(-10, 0)
		r.waitFocus("")
	}, func() bool {
		c, _ := r.clip.Read()
		if c.Kind != clip.Files || len(c.Files) != 1 || !strings.HasPrefix(c.Files[0], r.dir) {
			return false
		}
		data, _ := os.ReadFile(c.Files[0])
		return string(data) == "made on the mac"
	})
}

func TestClipboardReachesEveryScreen(t *testing.T) {
	r := startClipServer(t, []string{"desk", "a", "b"}, 0)
	a, aClip, _ := r.join("a")
	b, bClip, _ := r.join("b")

	// Copy on client a, move straight to client b: relayed by the server.
	r.cap.edge(layout.Right, 0)
	r.waitFocus("a")
	waitPos(t, a, 2, 0)
	aClip.WriteText("from a")
	r.arrivesOn("b", "a's text on b", func() {
		r.cap.move(2000, 0)
		r.waitFocus("b")
	}, func() bool { return text(bClip) == "from a" })
	waitPos(t, b, 2, 0)

	// Walk back to the server without copying anything new: the same text
	// must arrive there too, although it was copied two screens away.
	r.cap.move(-10, 0)
	r.waitFocus("a")
	r.arrivesOnServer("a's text on the server", func() {
		r.cap.move(-2000, 0)
		r.waitFocus("")
	}, func() bool { return text(r.clip) == "from a" })

	// A later copy on the server replaces it everywhere the cursor goes.
	r.clip.WriteText("newer")
	r.arrivesOn("a", "the newer text on a", func() {
		r.cap.edge(layout.Right, 0)
		r.waitFocus("a")
	}, func() bool { return text(aClip) == "newer" })
	r.arrivesOn("b", "the newer text on b", func() {
		r.cap.move(2000, 0)
		r.waitFocus("b")
	}, func() bool { return text(bClip) == "newer" })
}

func TestClipboardSizeLimit(t *testing.T) {
	r := startClipServer(t, []string{"desk", "mac"}, 100)
	_, macClip, _ := r.join("mac")

	r.clip.WriteText(strings.Repeat("x", 101))
	r.cap.edge(layout.Right, 0.5)
	r.waitFocus("mac")
	eventually(t, "the size limit to be reported", func() bool {
		return strings.Contains(r.srv.ClipboardStatus(), "over the size limit")
	})
	if text(macClip) != "" {
		t.Fatal("content over the limit was transferred")
	}
	// Something small still goes through afterwards.
	r.cap.move(-10, 0)
	r.waitFocus("")
	r.clip.WriteText("small")
	r.arrivesOn("mac", "the small text on the client", func() {
		r.cap.edge(layout.Right, 0.5)
		r.waitFocus("mac")
	}, func() bool { return text(macClip) == "small" })
}

// A client without a clipboard (or an older version) simply does not take
// part; input sharing is unaffected.
func TestClipboardIsOptional(t *testing.T) {
	r := startClipServer(t, []string{"desk", "plain"}, 0)
	plain, _ := r.connect("plain", 1000, 500)
	r.clip.WriteText("only here")
	r.cap.edge(layout.Right, 0.5)
	r.waitFocus("plain")
	waitPos(t, plain, 2, 250)
	r.cap.move(50, 0)
	waitPos(t, plain, 52, 250)
	r.cap.move(-100, 0)
	r.waitFocus("")
	if text(r.clip) != "only here" {
		t.Errorf("the server's clipboard changed: %q", text(r.clip))
	}
}
