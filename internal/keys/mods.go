package keys

import (
	"fmt"
	"sort"
	"strings"
)

// Modifier keys as a mask. Left and right keys count the same.
const (
	ModCtrl  uint8 = 1 << iota
	ModAlt         // Option on a Mac
	ModShift       //
	ModMeta        // Command on a Mac, the Windows key elsewhere
)

var modNames = map[string]uint8{
	"ctrl": ModCtrl, "control": ModCtrl,
	"alt": ModAlt, "option": ModAlt, "opt": ModAlt,
	"shift": ModShift,
	"meta":  ModMeta, "win": ModMeta, "cmd": ModMeta, "command": ModMeta, "super": ModMeta,
}

// ModKeys lists, for every modifier of the mask, its left and right key.
var ModKeys = [4][2]uint16{
	{LeftCtrl, RightCtrl},
	{LeftAlt, RightAlt},
	{LeftShift, RightShift},
	{LeftMeta, RightMeta},
}

// ModOf returns the mask bit of a modifier key, 0 for any other key.
func ModOf(code uint16) uint8 {
	for i, pair := range ModKeys {
		if code == pair[0] || code == pair[1] {
			return 1 << i
		}
	}
	return 0
}

// ParseMods reads a combination of modifier keys such as "ctrl+alt". An
// empty string or "none" means no combination (0).
func ParseMods(s string) (uint8, error) {
	s = norm(s)
	if s == "" || s == "none" {
		return 0, nil
	}
	var mask uint8
	for _, part := range strings.Split(s, "+") {
		m, ok := modNames[norm(part)]
		if !ok {
			return 0, fmt.Errorf("%q is not a modifier key (use ctrl, alt, shift, cmd joined with +)", strings.TrimSpace(part))
		}
		mask |= m
	}
	return mask, nil
}

// ModsText names a combination the way the keys are labelled on the given
// system ("darwin": Option and Cmd; otherwise Alt and Win).
func ModsText(mask uint8, goos string) string {
	alt, meta := "Alt", "Win"
	if goos == "darwin" {
		alt, meta = "Option", "Cmd"
	}
	var parts []string
	if mask&ModCtrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if mask&ModAlt != 0 {
		parts = append(parts, alt)
	}
	if mask&ModShift != 0 {
		parts = append(parts, "Shift")
	}
	if mask&ModMeta != 0 {
		parts = append(parts, meta)
	}
	return strings.Join(parts, "+")
}

// ID returns a text that is the same for two hotkeys exactly when they are
// the same combination, however they were written ("ctrl+alt+2" and
// "Alt+Control+2"). An empty hotkey has the ID "".
func (h Hotkey) ID() string {
	if h.Key == 0 {
		return ""
	}
	groups := make([]string, 0, len(h.Mods))
	for _, g := range h.Mods {
		codes := append([]uint16(nil), g...)
		sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
		groups = append(groups, fmt.Sprint(codes))
	}
	sort.Strings(groups)
	// The same modifier written twice ("ctrl+ctrl+2") is still one modifier.
	uniq := groups[:0]
	for i, g := range groups {
		if i == 0 || g != groups[i-1] {
			uniq = append(uniq, g)
		}
	}
	return strings.Join(uniq, "+") + fmt.Sprintf("+%d", h.Key)
}
