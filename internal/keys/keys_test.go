package keys

import "testing"

func TestWindowsRoundTrip(t *testing.T) {
	// Every code we can inject on Windows must map back to itself when the
	// hook reports it.
	for code := uint16(1); code < MaxCode; code++ {
		scan, ext, vk := ToWindows(code)
		if scan == 0 && vk == 0 {
			continue
		}
		got := FromWindows(uint32(vk), uint32(scan), ext)
		if got != code {
			t.Errorf("code %d (%s): ToWindows -> scan=%#x ext=%v vk=%#x -> FromWindows = %d",
				code, Name(code), scan, ext, vk, got)
		}
	}
}

func TestWindowsKnownKeys(t *testing.T) {
	cases := []struct {
		vk, scan uint32
		ext      bool
		want     uint16
	}{
		{0x41, 0x1E, false, A},
		{0x0D, 0x1C, false, Enter},
		{0x0D, 0x1C, true, KPEnter},
		{0xA2, 0x1D, false, LeftCtrl},
		{0xA3, 0x1D, true, RightCtrl},
		{0xA5, 0x38, true, RightAlt},
		{0xA1, 0x36, true, RightShift}, // reported with the extended flag
		{0x25, 0x4B, true, Left_},
		{0x64, 0x4B, false, KP4},
		{0x5B, 0x5B, true, LeftMeta},
		{0x13, 0x45, false, Pause},
		{0x90, 0x45, true, NumLock},
		{0x2C, 0x37, true, SysRq},
		{0x7A, 0x57, false, F11},
		{0xFF, 0x00, false, 0},
	}
	for _, c := range cases {
		if got := FromWindows(c.vk, c.scan, c.ext); got != c.want {
			t.Errorf("FromWindows(vk=%#x scan=%#x ext=%v) = %d, want %d", c.vk, c.scan, c.ext, got, c.want)
		}
	}
}

func TestMacRoundTrip(t *testing.T) {
	for code, mac := range codeToMac {
		if got := FromMac(mac); got != code {
			t.Errorf("mac %#x -> %d, want %d", mac, got, code)
		}
	}
	if k, ok := ToMac(A); !ok || k != 0 {
		t.Errorf("ToMac(A) = %#x, %v", k, ok)
	}
	if k, _ := ToMac(LeftMeta); k != 0x37 {
		t.Errorf("ToMac(LeftMeta) = %#x, want 0x37 (command)", k)
	}
	if _, ok := ToMac(ScrollLock); ok {
		t.Errorf("ScrollLock has no macOS key")
	}
	// The whole alphanumeric block must be covered.
	for code := uint16(Num1); code <= Slash; code++ {
		if code == LeftCtrl || code == LeftShift {
			continue
		}
		if _, ok := ToMac(code); !ok {
			t.Errorf("no macOS mapping for %s (%d)", Name(code), code)
		}
	}
}

func TestLookupAndRemap(t *testing.T) {
	if c, err := Lookup("Backspace"); err != nil || len(c) != 1 || c[0] != Backspace {
		t.Errorf("Lookup(Backspace) = %v, %v", c, err)
	}
	if c, _ := Lookup("ctrl"); len(c) != 2 {
		t.Errorf("Lookup(ctrl) = %v", c)
	}
	if _, err := Lookup("nope"); err == nil {
		t.Errorf("expected an error")
	}
	m, err := ParseRemap(map[string]string{"alt": "meta", "meta": "alt", "capslock": "ctrl"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint16]uint16{
		LeftAlt: LeftMeta, RightAlt: RightMeta,
		LeftMeta: LeftAlt, RightMeta: RightAlt,
		CapsLock: LeftCtrl,
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("remap[%s] = %s, want %s", Name(k), Name(m[k]), Name(v))
		}
	}
	if _, err := ParseRemap(map[string]string{"alt": "bogus"}); err == nil {
		t.Errorf("expected an error for a bad target")
	}
}

func TestHotkey(t *testing.T) {
	h, err := ParseHotkey("ctrl+alt+shift+backspace")
	if err != nil {
		t.Fatal(err)
	}
	held := map[uint16]bool{RightCtrl: true, LeftAlt: true, LeftShift: true}
	is := func(c uint16) bool { return held[c] }
	if !h.Match(Backspace, is) {
		t.Errorf("hotkey should match")
	}
	if h.Match(Enter, is) {
		t.Errorf("wrong key must not match")
	}
	delete(held, LeftAlt)
	if h.Match(Backspace, is) {
		t.Errorf("missing modifier must not match")
	}
	none, err := ParseHotkey("")
	if err != nil || none.Match(Backspace, is) {
		t.Errorf("empty hotkey must never match")
	}
	if _, err := ParseHotkey("ctrl+shift"); err == nil {
		t.Errorf("a hotkey ending in a modifier group must be rejected")
	}
	if _, err := ParseHotkey("ctrl+wat"); err == nil {
		t.Errorf("unknown key must be rejected")
	}
}

func TestParseMods(t *testing.T) {
	cases := map[string]uint8{
		"":             0,
		"none":         0,
		" None ":       0,
		"ctrl":         ModCtrl,
		"Control":      ModCtrl,
		"ctrl+alt":     ModCtrl | ModAlt,
		"option+shift": ModAlt | ModShift,
		"cmd":          ModMeta,
		"win + ctrl":   ModMeta | ModCtrl,
		"super+shift":  ModMeta | ModShift,
	}
	for in, want := range cases {
		got, err := ParseMods(in)
		if err != nil || got != want {
			t.Errorf("ParseMods(%q) = %04b, %v; want %04b", in, got, err, want)
		}
	}
	for _, bad := range []string{"ctrl+a", "banana", "ctrl+", "leftctrl"} {
		if _, err := ParseMods(bad); err == nil {
			t.Errorf("ParseMods(%q): expected an error", bad)
		}
	}
	if got := ModsText(ModCtrl|ModAlt|ModMeta, "darwin"); got != "Ctrl+Option+Cmd" {
		t.Errorf("ModsText on a Mac = %q", got)
	}
	if got := ModsText(ModAlt|ModShift|ModMeta, "windows"); got != "Alt+Shift+Win" {
		t.Errorf("ModsText on Windows = %q", got)
	}
	if ModsText(0, "linux") != "" {
		t.Errorf("no modifiers must give an empty text")
	}
}

func TestModOf(t *testing.T) {
	cases := map[uint16]uint8{
		LeftCtrl: ModCtrl, RightCtrl: ModCtrl, LeftAlt: ModAlt, RightAlt: ModAlt,
		LeftShift: ModShift, RightShift: ModShift, LeftMeta: ModMeta, RightMeta: ModMeta,
		A: 0, Space: 0, CapsLock: 0,
	}
	for code, want := range cases {
		if got := ModOf(code); got != want {
			t.Errorf("ModOf(%s) = %04b, want %04b", Name(code), got, want)
		}
	}
}

// Two hotkeys are the same combination however they were written.
func TestHotkeyID(t *testing.T) {
	id := func(s string) string {
		h, err := ParseHotkey(s)
		if err != nil {
			t.Fatalf("ParseHotkey(%q): %v", s, err)
		}
		return h.ID()
	}
	same := [][2]string{
		{"ctrl+alt+2", "Alt+Control+2"},
		{"cmd+f1", "win+f1"},
		{"ctrl+ctrl+a", "ctrl+a"},
		{"f13", " F13 "},
	}
	for _, p := range same {
		if id(p[0]) != id(p[1]) {
			t.Errorf("%q and %q should be the same hotkey", p[0], p[1])
		}
	}
	different := [][2]string{
		{"ctrl+alt+2", "ctrl+alt+3"},
		{"ctrl+2", "ctrl+alt+2"},
		{"ctrl+2", "leftctrl+2"}, // either Ctrl key against one particular key
		{"ctrl+alt+2", "ctrl+shift+2"},
	}
	for _, p := range different {
		if id(p[0]) == id(p[1]) {
			t.Errorf("%q and %q should be different hotkeys", p[0], p[1])
		}
	}
	if id("") != "" || id("none") != "" {
		t.Errorf("an empty hotkey must have an empty ID")
	}
}
