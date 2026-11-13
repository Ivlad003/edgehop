package config

import (
	"path/filepath"
	"strings"
	"testing"

	"edgehop/internal/keys"
	"edgehop/internal/layout"
)

func TestTemplatesParse(t *testing.T) {
	s, err := Parse(ServerTemplate("desktop", "secret", DefaultPort, []string{"laptop", "desktop", "macbook"}))
	if err != nil {
		t.Fatalf("server template: %v", err)
	}
	if s.Role != "server" || s.Name != "desktop" || s.Listen != "0.0.0.0:24920" {
		t.Errorf("server config = %+v", s)
	}
	l, err := s.Layout()
	if err != nil {
		t.Fatal(err)
	}
	if l.Neighbor("desktop", layout.Left) != "laptop" || l.Neighbor("desktop", layout.Right) != "macbook" {
		t.Errorf("layout:\n%s", l.Describe())
	}
	hk, err := s.Hotkey()
	if err != nil || hk.Key != keys.Backspace || len(hk.Mods) != 3 {
		t.Errorf("hotkey = %+v, %v", hk, err)
	}
	if !s.HideCursorEnabled() {
		t.Errorf("hide_cursor should default to true")
	}

	c, err := Parse(ClientTemplate("macbook", "secret", "192.168.1.10"))
	if err != nil {
		t.Fatalf("client template: %v", err)
	}
	if c.Role != "client" || c.Server != "192.168.1.10:24920" {
		t.Errorf("client config = %+v", c)
	}

	// A server alone (no row given) must still be a valid config.
	if _, err := Parse(ServerTemplate("solo", "k", 5000, nil)); err != nil {
		t.Fatalf("solo template: %v", err)
	}
}

func TestLinksAndScreens(t *testing.T) {
	c, err := Parse(`
role = "server"
name = "desk"
key = "k"
row = ["desk", "mac"]
release_hotkey = "none"
screen_size = "2560x1440"

[[links]]
from = "desk"
side = "top"
to = "tv"

[[links]]
from = "mac"
side = "bottom"
to = "pad"
one_way = true

[screens.mac]
scale = 1.5
remap = { alt = "meta", meta = "alt" }
`)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := c.Layout()
	if l.Neighbor("desk", layout.Top) != "tv" || l.Neighbor("tv", layout.Bottom) != "desk" {
		t.Errorf("two-way link missing:\n%s", l.Describe())
	}
	if l.Neighbor("mac", layout.Bottom) != "pad" || l.Neighbor("pad", layout.Top) != "" {
		t.Errorf("one-way link wrong:\n%s", l.Describe())
	}
	if hk, _ := c.Hotkey(); hk.Key != 0 {
		t.Errorf("hotkey \"none\" should disable it")
	}
	if w, h, _ := c.Size(); w != 2560 || h != 1440 {
		t.Errorf("size = %dx%d", w, h)
	}
	if c.Screens["mac"].Scale != 1.5 {
		t.Errorf("scale = %v", c.Screens["mac"].Scale)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"missing key":      `role = "server"` + "\n" + `name = "a"`,
		"bad role":         `role = "boss"` + "\n" + `key = "k"`,
		"client no server": `role = "client"` + "\n" + `key = "k"`,
		"row without self": `role = "server"` + "\n" + `name = "a"` + "\n" + `key = "k"` + "\n" + `row = ["b", "c"]`,
		"row duplicate":    `role = "server"` + "\n" + `name = "a"` + "\n" + `key = "k"` + "\n" + `row = ["a", "b", "b"]`,
		"unknown setting":  `role = "server"` + "\n" + `name = "a"` + "\n" + `key = "k"` + "\n" + `colour = "red"`,
		"bad hotkey":       `role = "server"` + "\n" + `name = "a"` + "\n" + `key = "k"` + "\n" + `release_hotkey = "ctrl+banana"`,
		"bad size":         `role = "server"` + "\n" + `name = "a"` + "\n" + `key = "k"` + "\n" + `screen_size = "big"`,
		"bad link side":    `role = "server"` + "\n" + `name = "a"` + "\n" + `key = "k"` + "\n" + "[[links]]\nfrom = \"a\"\nside = \"sideways\"\nto = \"b\"",
		"orphan screen":    `role = "server"` + "\n" + `name = "a"` + "\n" + `key = "k"` + "\n" + "[screens.ghost]\nscale = 2.0",
		"bad remap":        `role = "server"` + "\n" + `name = "a"` + "\n" + `key = "k"` + "\n" + `row = ["a", "b"]` + "\n" + "[screens.b]\nremap = { alt = \"nothing\" }",
	}
	for name, text := range cases {
		if _, err := Parse(text); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestWithPort(t *testing.T) {
	cases := map[string]string{
		"192.168.1.5":      "192.168.1.5:24920",
		"192.168.1.5:9000": "192.168.1.5:9000",
		"desktop.local":    "desktop.local:24920",
		"[fe80::1]:9000":   "[fe80::1]:9000",
		"fe80::1":          "[fe80::1]:24920",
	}
	for in, want := range cases {
		if got := WithPort(in); got != want {
			t.Errorf("WithPort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "edgehop.toml")
	if err := Write(path, ClientTemplate("c", "k", "10.0.0.1"), false); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, "x", false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("overwrite without force: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Path() != path || c.Name != "c" {
		t.Errorf("loaded %+v", c)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); err == nil || !strings.Contains(err.Error(), "edgehop init") {
		t.Errorf("missing file error should point at `edgehop init`: %v", err)
	}
}
