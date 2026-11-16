package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
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

// A config file written for a version that still had remote windows keeps
// loading: the settings of that feature are ignored, not reported as unknown.
func TestObsoleteSettingsAreIgnored(t *testing.T) {
	text := `role = "server"` + "\n" + `name = "a"` + "\n" + `key = "k"` + "\n" +
		`remote_windows = true` + "\n" + `window_keys = "ctrl+shift"` + "\n"
	c, err := Parse(text)
	if err != nil {
		t.Fatalf("a config of an older version is refused: %v", err)
	}
	if c.Name != "a" || c.Role != "server" {
		t.Fatalf("parsed as %+v", c)
	}
	out, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "remote_windows") || strings.Contains(out, "window_keys") {
		t.Fatalf("an obsolete setting is written back:\n%s", out)
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
	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); err == nil || !strings.Contains(err.Error(), "edgehop") {
		t.Errorf("missing file error should say how to create one: %v", err)
	}
}

func TestFormApplyEncodeRoundTrip(t *testing.T) {
	c, err := Parse(`
role = "server"
name = "desk"
key = "k"
listen = "127.0.0.1:5000"
row = ["desk", "mac", "old"]
pointer_speed = 1.5

[[links]]
from = "desk"
side = "top"
to = "tv"

[screens.mac]
remap = { capslock = "ctrl" }

[screens.old]
scale = 2.0
`)
	if err != nil {
		t.Fatal(err)
	}
	f := c.Form()
	if f.Port != 5000 || f.ReleaseHotkey != DefaultHotkey || !f.HideCursor || len(f.Row) != 3 {
		t.Fatalf("form = %+v", f)
	}
	if f.Screens["mac"].Scale != 1 || f.Screens["mac"].SwapAltMeta || f.Screens["old"].Scale != 2 {
		t.Fatalf("form screens = %+v", f.Screens)
	}

	// The user renames nothing, drops "old", adds "lap", swaps alt/meta for the Mac.
	f.Row = []string{"lap", " desk ", "mac", ""}
	f.Port = 6000
	f.HideCursor = false
	f.ReleaseHotkey = "ctrl+alt+f12"
	f.Screens = map[string]FormScreen{
		"mac":  {Scale: 1.25, SwapAltMeta: true},
		"lap":  {Scale: 1},
		"old":  {Scale: 3},
		"desk": {Scale: 9},
	}
	c.Apply(f)
	path := filepath.Join(t.TempDir(), "edgehop.toml")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Listen != "127.0.0.1:6000" {
		t.Errorf("listen = %q (the host must be kept)", got.Listen)
	}
	if strings.Join(got.Row, ",") != "lap,desk,mac" {
		t.Errorf("row = %v", got.Row)
	}
	if got.HideCursorEnabled() || *got.ReleaseHotkey != "ctrl+alt+f12" {
		t.Errorf("hide=%v hotkey=%v", got.HideCursorEnabled(), *got.ReleaseHotkey)
	}
	mac := got.Screens["mac"]
	if mac.Scale != 1.25 || mac.Remap["alt"] != "meta" || mac.Remap["meta"] != "alt" || mac.Remap["capslock"] != "ctrl" {
		t.Errorf("mac = %+v (the existing capslock remap must survive)", mac)
	}
	if _, ok := got.Screens["old"]; ok {
		t.Errorf("options of a removed screen must be dropped")
	}
	if _, ok := got.Screens["lap"]; ok {
		t.Errorf("a screen with default options needs no section")
	}
	if _, ok := got.Screens["desk"]; ok {
		t.Errorf("the server itself has no per-screen options")
	}
	// Settings the form does not show must survive.
	if got.PointerSpeed != 1.5 || len(got.Links) != 1 || got.Links[0].To != "tv" {
		t.Errorf("pointer_speed=%v links=%+v", got.PointerSpeed, got.Links)
	}

	// Un-ticking the swap removes only those two entries.
	f = got.Form()
	if !f.Screens["mac"].SwapAltMeta {
		t.Fatalf("swap not detected: %+v", f.Screens)
	}
	f.Screens["mac"] = FormScreen{Scale: 1, SwapAltMeta: false}
	got.Apply(f)
	if r := got.Screens["mac"].Remap; len(r) != 1 || r["capslock"] != "ctrl" {
		t.Errorf("remap after un-swapping = %v", r)
	}
}

func TestApplyClientAndInvalidSave(t *testing.T) {
	c := &Config{}
	c.Apply(Form{Role: "Client", Name: " lap ", Key: " secret ", Server: " 10.0.0.5 "})
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != "client" || got.Name != "lap" || got.Key != "secret" || got.Server != "10.0.0.5:24920" {
		t.Errorf("client = %+v", got)
	}
	// An invalid configuration must not be written.
	bad := &Config{}
	bad.Apply(Form{Role: "server", Name: "a", Key: "", Row: []string{"a", "b"}})
	badPath := filepath.Join(t.TempDir(), "bad.toml")
	if err := Save(badPath, bad); err == nil {
		t.Fatal("saving a config without a key must fail")
	}
	if _, err := Load(badPath); err == nil {
		t.Fatal("an invalid config was written to disk")
	}
	// A lone server needs no row at all.
	solo := &Config{}
	solo.Apply(Form{Role: "server", Name: "solo", Key: "k", Row: []string{"solo"}})
	if solo.Row != nil || solo.Listen != "0.0.0.0:24920" {
		t.Errorf("solo = %+v", solo)
	}
}

func TestFormAboveBelow(t *testing.T) {
	c, err := Parse(`
role = "server"
name = "desk"
key = "k"
row = ["desk", "mac"]

[[links]]
from = "tv"
side = "bottom"
to = "desk"

[[links]]
from = "mac"
side = "right"
to = "far"
`)
	if err != nil {
		t.Fatal(err)
	}
	f := c.Form()
	if f.Above != "tv" || f.Below != "" {
		t.Fatalf("above=%q below=%q, want tv and nothing", f.Above, f.Below)
	}
	// Replace the screen above, add one below, give the lower one options.
	f.Above = "projector"
	f.Below = " pad "
	f.Screens["pad"] = FormScreen{Scale: 2}
	c.Apply(f)
	path := filepath.Join(t.TempDir(), "edgehop.toml")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := got.Layout()
	if err != nil {
		t.Fatal(err)
	}
	if l.Neighbor("desk", layout.Top) != "projector" || l.Neighbor("projector", layout.Bottom) != "desk" {
		t.Errorf("screen above:\n%s", l.Describe())
	}
	if l.Neighbor("desk", layout.Bottom) != "pad" || l.Has("tv") {
		t.Errorf("screen below / old link:\n%s", l.Describe())
	}
	if l.Neighbor("mac", layout.Right) != "far" {
		t.Errorf("an unrelated link was lost:\n%s", l.Describe())
	}
	if got.Screens["pad"].Scale != 2 {
		t.Errorf("options of the screen below were dropped: %+v", got.Screens)
	}
	// Clearing both removes the links again.
	f = got.Form()
	f.Above, f.Below = "", ""
	got.Apply(f)
	if len(got.Links) != 1 || got.Links[0].To != "far" {
		t.Errorf("links after clearing = %+v", got.Links)
	}
}

func TestHotkeysAndEdgeProtection(t *testing.T) {
	c, err := Parse(`
role = "server"
name = "desk"
key = "k"
row = ["lap", "desk", "mac"]
switch_delay_ms = 250
switch_keys = "Ctrl + Shift"
switch_corner = 40

[screens.mac]
hotkey = "ctrl+alt+2"

[screens.lap]
scale = 1.5
hotkey = "none"
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.SwitchDelayMS != 250 || c.SwitchCorner != 40 {
		t.Errorf("delay=%d corner=%d", c.SwitchDelayMS, c.SwitchCorner)
	}
	if mods, err := c.SwitchMods(); err != nil || mods != keys.ModCtrl|keys.ModShift {
		t.Errorf("switch mods = %04b, %v", mods, err)
	}
	hk, err := c.ScreenHotkeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(hk) != 1 || hk["mac"].Key != 3 || len(hk["mac"].Mods) != 2 {
		t.Errorf("screen hotkeys = %+v (only mac has one; \"none\" means none)", hk)
	}

	// Without the settings everything is off, and a saved file does not
	// mention them.
	plain, err := Parse(`role = "server"` + "\n" + `name = "desk"` + "\n" + `key = "k"` + "\n" + `row = ["desk", "mac"]`)
	if err != nil {
		t.Fatal(err)
	}
	if mods, _ := plain.SwitchMods(); mods != 0 || plain.SwitchDelayMS != 0 || plain.SwitchCorner != 0 {
		t.Errorf("the protection must be off by default: %+v", plain)
	}
	if hk, _ := plain.ScreenHotkeys(); len(hk) != 0 {
		t.Errorf("hotkeys by default: %+v", hk)
	}
	out, err := plain.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"switch_", "hotkey", "screens"} {
		if strings.Contains(out, word) {
			t.Errorf("an unset setting (%s) is written to the file:\n%s", word, out)
		}
	}
	if none, err := Parse(`role = "server"` + "\n" + `name = "a"` + "\n" + `key = "k"` + "\n" + `switch_keys = "none"`); err != nil {
		t.Errorf("switch_keys = \"none\": %v", err)
	} else if mods, _ := none.SwitchMods(); mods != 0 {
		t.Errorf("switch_keys = \"none\" gave %04b", mods)
	}
}

func TestHotkeyAndEdgeProtectionErrors(t *testing.T) {
	base := `role = "server"` + "\n" + `name = "a"` + "\n" + `key = "k"` + "\n" + `row = ["a", "b", "c"]` + "\n"
	cases := map[string]string{
		"switch_keys not a modifier": base + `switch_keys = "ctrl+a"`,
		"negative delay":             base + `switch_delay_ms = -1`,
		"huge delay":                 base + `switch_delay_ms = 60000`,
		"negative corner":            base + `switch_corner = -5`,
		"huge corner":                base + `switch_corner = 5000`,
		"bad screen hotkey":          base + "[screens.b]\nhotkey = \"ctrl+banana\"",
		"hotkey of modifiers only":   base + "[screens.b]\nhotkey = \"ctrl+alt\"",
		"two screens, one hotkey":    base + "[screens.b]\nhotkey = \"ctrl+alt+2\"\n[screens.c]\nhotkey = \"alt+control+2\"",
		"same as the release hotkey": base + `release_hotkey = "ctrl+alt+f1"` + "\n[screens.b]\nhotkey = \"ctrl+alt+f1\"",
		"same as the default one":    base + "[screens.b]\nhotkey = \"ctrl+alt+shift+backspace\"",
		"hotkey for the server":      base + "[screens.a]\nhotkey = \"ctrl+alt+1\"",
	}
	for name, text := range cases {
		if _, err := Parse(text); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	// With the release hotkey switched off, its combination is free.
	if _, err := Parse(base + `release_hotkey = "none"` + "\n[screens.b]\nhotkey = \"ctrl+alt+shift+backspace\""); err != nil {
		t.Errorf("a screen may use the combination of a disabled release hotkey: %v", err)
	}
}

func TestFormHotkeysAndEdgeProtection(t *testing.T) {
	c, err := Parse(`
role = "server"
name = "desk"
key = "k"
row = ["desk", "mac", "lap"]
switch_keys = "none"

[screens.mac]
hotkey = "ctrl+alt+2"
remap = { capslock = "ctrl" }
`)
	if err != nil {
		t.Fatal(err)
	}
	f := c.Form()
	if f.SwitchDelayMS != 0 || f.SwitchKeys != "" || f.SwitchCorner != 0 {
		t.Fatalf("form = %+v", f)
	}
	if f.Screens["mac"].Hotkey != "ctrl+alt+2" {
		t.Fatalf("form screens = %+v", f.Screens)
	}

	f.SwitchDelayMS = 300
	f.SwitchKeys = " ctrl "
	f.SwitchCorner = 25
	f.Screens["mac"] = FormScreen{Scale: 1, Hotkey: " ctrl+alt+m "}
	f.Screens["lap"] = FormScreen{Scale: 1, Hotkey: "ctrl+alt+l"}
	c.Apply(f)
	path := filepath.Join(t.TempDir(), "edgehop.toml")
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SwitchDelayMS != 300 || got.SwitchKeys != "ctrl" || got.SwitchCorner != 25 {
		t.Errorf("saved protection: delay=%d keys=%q corner=%d", got.SwitchDelayMS, got.SwitchKeys, got.SwitchCorner)
	}
	if got.Screens["mac"].Hotkey != "ctrl+alt+m" || got.Screens["mac"].Remap["capslock"] != "ctrl" {
		t.Errorf("mac = %+v (the hotkey changes, the remap stays)", got.Screens["mac"])
	}
	if got.Screens["lap"].Hotkey != "ctrl+alt+l" {
		t.Errorf("a screen with only a hotkey needs its section: %+v", got.Screens)
	}

	// Switching everything off again leaves nothing behind in the file.
	f = got.Form()
	f.SwitchDelayMS, f.SwitchKeys, f.SwitchCorner = 0, "none", 0
	f.Screens["mac"] = FormScreen{Scale: 1}
	f.Screens["lap"] = FormScreen{Scale: 1, Hotkey: "none"}
	got.Apply(f)
	text, err := got.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "switch_") || strings.Contains(text, "\nhotkey") || strings.Contains(text, "screens.lap") {
		t.Errorf("switched-off settings are still written:\n%s", text)
	}
	if got.Screens["mac"].Remap["capslock"] != "ctrl" {
		t.Errorf("the remap was lost with the hotkey: %+v", got.Screens)
	}

	// What the page cannot fix by itself is refused, not silently changed.
	f.SwitchKeys = "ctrl+q"
	got.Apply(f)
	if err := Save(path, got); err == nil || !strings.Contains(err.Error(), "switch_keys") {
		t.Errorf("a bad switch_keys must be refused with a clear message, got: %v", err)
	}
	f.SwitchKeys = ""
	f.SwitchDelayMS = -20
	got.Apply(f)
	if err := Save(path, got); err == nil || !strings.Contains(err.Error(), "switch_delay_ms") {
		t.Errorf("a negative delay must be refused, got: %v", err)
	}
	f.SwitchDelayMS = 0
	f.Screens["lap"] = FormScreen{Scale: 1, Hotkey: "ctrl+alt+shift+backspace"}
	got.Apply(f)
	if err := Save(path, got); err == nil || !strings.Contains(err.Error(), "release_hotkey") {
		t.Errorf("a screen hotkey equal to the release hotkey must be refused, got: %v", err)
	}
}
