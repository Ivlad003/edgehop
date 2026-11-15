package server_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ivlad003/edgehop/internal/client"
	"github.com/Ivlad003/edgehop/internal/server"
)

// Files sent on purpose land in the receiver's folder for received files,
// in both directions, without any clipboard being involved.
func TestSentFilesArriveInTheDropFolder(t *testing.T) {
	serverDrops := t.TempDir()
	r := startServerWith(t, "desk", []string{"desk", "mac", "old"}, nil, false, func(c *server.Config) {
		c.DropDir = serverDrops
	})
	macDrops := t.TempDir()
	r.connectWith("mac", 1000, 500, func(c *client.Config) { c.DropDir = macDrops })
	mac := r.clients["mac"]
	eventually(t, "the data connection", func() bool {
		for _, c := range r.srv.Clients() {
			if c.Name == "mac" && c.Files {
				return mac.CanSendFiles() && mac.ServerName() == "desk"
			}
		}
		return false
	})

	src := t.TempDir()
	big := strings.Repeat("0123456789", 20000) // several chunks
	os.WriteFile(filepath.Join(src, "звіт.txt"), []byte("для мака"), 0o644)
	os.MkdirAll(filepath.Join(src, "album", "2026"), 0o755)
	os.WriteFile(filepath.Join(src, "album", "2026", "big.bin"), []byte(big), 0o644)

	// Server -> client.
	if err := r.srv.SendFiles("mac", []string{filepath.Join(src, "звіт.txt"), filepath.Join(src, "album")}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the files in the client's folder", func() bool {
		a, _ := os.ReadFile(filepath.Join(macDrops, "звіт.txt"))
		b, _ := os.ReadFile(filepath.Join(macDrops, "album", "2026", "big.bin"))
		return string(a) == "для мака" && string(b) == big
	})
	eventually(t, "the client to report it", func() bool {
		return strings.Contains(mac.ClipboardStatus(), "received") && strings.Contains(mac.ClipboardStatus(), macDrops)
	})
	if !strings.Contains(r.srv.ClipboardStatus(), "sent to mac") {
		t.Errorf("server status = %q", r.srv.ClipboardStatus())
	}

	// Sending the same file again must not overwrite the first copy.
	os.WriteFile(filepath.Join(src, "звіт.txt"), []byte("нова версія"), 0o644)
	if err := r.srv.SendFiles("mac", []string{filepath.Join(src, "звіт.txt")}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the second copy under a new name", func() bool {
		a, _ := os.ReadFile(filepath.Join(macDrops, "звіт (2).txt"))
		return string(a) == "нова версія"
	})
	if a, _ := os.ReadFile(filepath.Join(macDrops, "звіт.txt")); string(a) != "для мака" {
		t.Errorf("the first copy was overwritten: %q", a)
	}

	// Client -> server.
	other := filepath.Join(t.TempDir(), "from-mac.txt")
	os.WriteFile(other, []byte("для сервера"), 0o644)
	if err := mac.SendFiles([]string{other}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the file in the server's folder", func() bool {
		a, _ := os.ReadFile(filepath.Join(serverDrops, "from-mac.txt"))
		return string(a) == "для сервера"
	})
	eventually(t, "the server to report it", func() bool {
		return strings.Contains(r.srv.ClipboardStatus(), "from mac saved to")
	})

	// Clear errors instead of silence.
	if err := r.srv.SendFiles("ghost", []string{other}); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("sending to an unknown computer: %v", err)
	}
	if err := r.srv.SendFiles("mac", []string{filepath.Join(src, "missing.txt")}); err == nil {
		t.Error("sending a missing file must fail")
	}
	// A client that keeps no folder for received files cannot be sent any.
	r.connect("old", 800, 600)
	if err := r.srv.SendFiles("old", []string{other}); err == nil || !strings.Contains(err.Error(), "cannot receive files") {
		t.Errorf("sending to a client without a folder: %v", err)
	}
	// Nothing was left half-written.
	for _, dir := range []string{macDrops, serverDrops} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".edgehop-incoming") {
				t.Errorf("temporary folder left in %s: %s", dir, e.Name())
			}
		}
	}
}
