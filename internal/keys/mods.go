package keys

import (
	"fmt"
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
