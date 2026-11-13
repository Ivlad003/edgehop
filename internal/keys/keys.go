// Package keys defines the key codes that travel over the network and the
// tables that translate them to and from each operating system.
//
// The wire format is the Linux evdev key code. It identifies a physical key
// position, not a character, so the receiving computer applies its own
// keyboard layout - exactly as if the keyboard were plugged into it.
package keys

import (
	"fmt"
	"sort"
	"strings"
)

// MaxCode is one more than the largest key code handled.
const MaxCode = 256

// Mouse buttons on the wire.
const (
	ButtonLeft    = 1
	ButtonRight   = 2
	ButtonMiddle  = 3
	ButtonBack    = 4
	ButtonForward = 5
)

// Key states on the wire.
const (
	Up     = 0
	Down   = 1
	Repeat = 2
)

// evdev key codes.
const (
	Esc        = 1
	Num1       = 2
	Num0       = 11
	Minus      = 12
	Equal      = 13
	Backspace  = 14
	Tab        = 15
	Q          = 16
	LeftBrace  = 26
	RightBrace = 27
	Enter      = 28
	LeftCtrl   = 29
	A          = 30
	Semicolon  = 39
	Apostrophe = 40
	Grave      = 41
	LeftShift  = 42
	Backslash  = 43
	Z          = 44
	C          = 46
	Comma      = 51
	Dot        = 52
	Slash      = 53
	RightShift = 54
	KPAsterisk = 55
	LeftAlt    = 56
	Space      = 57
	CapsLock   = 58
	F1         = 59
	F10        = 68
	NumLock    = 69
	ScrollLock = 70
	KP7        = 71
	KP8        = 72
	KP9        = 73
	KPMinus    = 74
	KP4        = 75
	KP5        = 76
	KP6        = 77
	KPPlus     = 78
	KP1        = 79
	KP2        = 80
	KP3        = 81
	KP0        = 82
	KPDot      = 83
	Key102nd   = 86
	F11        = 87
	F12        = 88
	KPEnter    = 96
	RightCtrl  = 97
	KPSlash    = 98
	SysRq      = 99
	RightAlt   = 100
	Home       = 102
	Up_        = 103
	PageUp     = 104
	Left_      = 105
	Right_     = 106
	End        = 107
	Down_      = 108
	PageDown   = 109
	Insert     = 110
	Delete     = 111
	Mute       = 113
	VolumeDown = 114
	VolumeUp   = 115
	KPEqual    = 117
	Pause      = 119
	LeftMeta   = 125
	RightMeta  = 126
	Compose    = 127
	NextSong   = 163
	PlayPause  = 164
	PrevSong   = 165
	StopCD     = 166
	F13        = 183
	F24        = 194
)

// ---------------------------------------------------------------------------
// Names (used in the config file for hotkeys and remapping)

var nameToCode = map[string]uint16{
	"esc": Esc, "escape": Esc,
	"1": 2, "2": 3, "3": 4, "4": 5, "5": 6, "6": 7, "7": 8, "8": 9, "9": 10, "0": 11,
	"minus": Minus, "equal": Equal, "backspace": Backspace, "tab": Tab,
	"q": 16, "w": 17, "e": 18, "r": 19, "t": 20, "y": 21, "u": 22, "i": 23, "o": 24, "p": 25,
	"leftbrace": LeftBrace, "rightbrace": RightBrace, "enter": Enter, "return": Enter,
	"leftctrl": LeftCtrl, "lctrl": LeftCtrl,
	"a": 30, "s": 31, "d": 32, "f": 33, "g": 34, "h": 35, "j": 36, "k": 37, "l": 38,
	"semicolon": Semicolon, "apostrophe": Apostrophe, "grave": Grave,
	"leftshift": LeftShift, "lshift": LeftShift, "backslash": Backslash,
	"z": 44, "x": 45, "c": 46, "v": 47, "b": 48, "n": 49, "m": 50,
	"comma": Comma, "dot": Dot, "period": Dot, "slash": Slash,
	"rightshift": RightShift, "rshift": RightShift,
	"leftalt": LeftAlt, "lalt": LeftAlt, "space": Space, "capslock": CapsLock,
	"f1": 59, "f2": 60, "f3": 61, "f4": 62, "f5": 63, "f6": 64, "f7": 65, "f8": 66, "f9": 67, "f10": 68,
	"numlock": NumLock, "scrolllock": ScrollLock,
	"f11": F11, "f12": F12,
	"rightctrl": RightCtrl, "rctrl": RightCtrl,
	"printscreen": SysRq, "sysrq": SysRq,
	"rightalt": RightAlt, "ralt": RightAlt, "altgr": RightAlt,
	"home": Home, "up": Up_, "pageup": PageUp, "left": Left_, "right": Right_,
	"end": End, "down": Down_, "pagedown": PageDown, "insert": Insert, "delete": Delete, "del": Delete,
	"mute": Mute, "volumedown": VolumeDown, "volumeup": VolumeUp, "pause": Pause,
	"leftmeta": LeftMeta, "lmeta": LeftMeta, "leftwin": LeftMeta, "leftcmd": LeftMeta, "leftsuper": LeftMeta,
	"rightmeta": RightMeta, "rmeta": RightMeta, "rightwin": RightMeta, "rightcmd": RightMeta, "rightsuper": RightMeta,
	"menu": Compose, "compose": Compose,
	"f13": 183, "f14": 184, "f15": 185, "f16": 186, "f17": 187, "f18": 188,
	"f19": 189, "f20": 190, "f21": 191, "f22": 192, "f23": 193, "f24": 194,
}

// Generic modifier names cover both the left and the right key.
var groups = map[string][]uint16{
	"ctrl":    {LeftCtrl, RightCtrl},
	"control": {LeftCtrl, RightCtrl},
	"shift":   {LeftShift, RightShift},
	"alt":     {LeftAlt, RightAlt},
	"option":  {LeftAlt, RightAlt},
	"meta":    {LeftMeta, RightMeta},
	"win":     {LeftMeta, RightMeta},
	"cmd":     {LeftMeta, RightMeta},
	"command": {LeftMeta, RightMeta},
	"super":   {LeftMeta, RightMeta},
}

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Lookup returns the code(s) for a key name. Generic modifier names such as
// "ctrl" return both the left and the right key.
func Lookup(name string) ([]uint16, error) {
	n := norm(name)
	if g, ok := groups[n]; ok {
		return g, nil
	}
	if c, ok := nameToCode[n]; ok {
		return []uint16{c}, nil
	}
	return nil, fmt.Errorf("unknown key name %q", name)
}

// Name returns a readable name for a code.
func Name(code uint16) string {
	best := ""
	for n, c := range nameToCode {
		if c == code && (best == "" || len(n) > len(best) || (len(n) == len(best) && n < best)) {
			best = n
		}
	}
	if best == "" {
		return fmt.Sprintf("key%d", code)
	}
	return best
}

// Names returns every known key name, sorted.
func Names() []string {
	out := make([]string, 0, len(nameToCode)+len(groups))
	for n := range nameToCode {
		out = append(out, n)
	}
	for n := range groups {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ParseRemap turns {"alt": "meta", "capslock": "leftctrl"} into a code map.
// A generic name on both sides maps left to left and right to right.
func ParseRemap(m map[string]string) (map[uint16]uint16, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := map[uint16]uint16{}
	for from, to := range m {
		f, err := Lookup(from)
		if err != nil {
			return nil, fmt.Errorf("remap: %w", err)
		}
		t, err := Lookup(to)
		if err != nil {
			return nil, fmt.Errorf("remap: %w", err)
		}
		for i, c := range f {
			if len(t) == len(f) {
				out[c] = t[i]
			} else {
				out[c] = t[0]
			}
		}
	}
	return out, nil
}

// Hotkey is a key pressed while a set of modifiers is held.
type Hotkey struct {
	Mods [][]uint16 // every group needs at least one of its keys held
	Key  uint16
}

// ParseHotkey parses strings like "ctrl+alt+shift+backspace".
// An empty string returns a zero Hotkey, which never matches.
func ParseHotkey(s string) (Hotkey, error) {
	var h Hotkey
	if norm(s) == "" || norm(s) == "none" {
		return h, nil
	}
	parts := strings.Split(s, "+")
	for i, p := range parts {
		codes, err := Lookup(p)
		if err != nil {
			return Hotkey{}, fmt.Errorf("hotkey %q: %w", s, err)
		}
		if i == len(parts)-1 {
			if len(codes) != 1 {
				return Hotkey{}, fmt.Errorf("hotkey %q: the last key must be a single key, not a modifier group", s)
			}
			h.Key = codes[0]
		} else {
			h.Mods = append(h.Mods, codes)
		}
	}
	return h, nil
}

// Match reports whether pressing code completes the hotkey, given a function
// that tells which keys are currently held.
func (h Hotkey) Match(code uint16, held func(uint16) bool) bool {
	if h.Key == 0 || code != h.Key {
		return false
	}
	for _, group := range h.Mods {
		any := false
		for _, c := range group {
			if held(c) {
				any = true
				break
			}
		}
		if !any {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Windows: set-1 scan codes. For the main block the scan code equals the
// evdev code; "extended" keys (E0 prefix) need a table.

var winExtToCode = map[uint16]uint16{
	0x1C: KPEnter, 0x1D: RightCtrl, 0x35: KPSlash, 0x37: SysRq, 0x38: RightAlt,
	0x47: Home, 0x48: Up_, 0x49: PageUp, 0x4B: Left_, 0x4D: Right_,
	0x4F: End, 0x50: Down_, 0x51: PageDown, 0x52: Insert, 0x53: Delete,
	0x5B: LeftMeta, 0x5C: RightMeta, 0x5D: Compose,
	0x20: Mute, 0x2E: VolumeDown, 0x30: VolumeUp,
	0x19: NextSong, 0x22: PlayPause, 0x10: PrevSong, 0x24: StopCD,
}

var winCodeToExt = invert(winExtToCode)

// Windows virtual-key codes used where scan codes are unreliable.
const (
	vkPause      = 0x13
	vkSnapshot   = 0x2C
	vkNumLock    = 0x90
	vkVolumeMute = 0xAD
	vkVolumeDown = 0xAE
	vkVolumeUp   = 0xAF
	vkMediaNext  = 0xB0
	vkMediaPrev  = 0xB1
	vkMediaStop  = 0xB2
	vkMediaPlay  = 0xB3
)

var winCodeToVK = map[uint16]uint16{
	Pause: vkPause, NumLock: vkNumLock,
	Mute: vkVolumeMute, VolumeDown: vkVolumeDown, VolumeUp: vkVolumeUp,
	NextSong: vkMediaNext, PrevSong: vkMediaPrev, StopCD: vkMediaStop, PlayPause: vkMediaPlay,
}

var winVKToCode = func() map[uint32]uint16 {
	m := map[uint32]uint16{vkSnapshot: SysRq}
	for c, vk := range winCodeToVK {
		m[uint32(vk)] = c
	}
	return m
}()

// FromWindows converts what a low-level keyboard hook reports into a key code.
// It returns 0 for keys that are not handled.
func FromWindows(vk, scan uint32, extended bool) uint16 {
	if c, ok := winVKToCode[vk]; ok {
		return c
	}
	scan &= 0xFF
	switch {
	case scan == 0x36: // right shift is sometimes flagged as extended
		return RightShift
	case extended:
		return winExtToCode[uint16(scan)]
	case scan >= 1 && scan <= 83, scan >= 86 && scan <= 88:
		return uint16(scan)
	case scan >= 0x64 && scan <= 0x6E: // F13..F23
		return uint16(F13 + (scan - 0x64))
	case scan == 0x76:
		return F24
	}
	return 0
}

// ToWindows returns how to inject a key on Windows: either a virtual-key code
// (vk != 0) or a scan code with its extended flag.
func ToWindows(code uint16) (scan uint16, extended bool, vk uint16) {
	if v, ok := winCodeToVK[code]; ok {
		return 0, false, v
	}
	if s, ok := winCodeToExt[code]; ok {
		return s, true, 0
	}
	switch {
	case code >= 1 && code <= 83, code >= 86 && code <= 88:
		return code, false, 0
	case code >= F13 && code < F24:
		return 0x64 + (code - F13), false, 0
	case code == F24:
		return 0x76, false, 0
	}
	return 0, false, 0
}

// ---------------------------------------------------------------------------
// macOS virtual key codes (kVK_*).

var codeToMac = map[uint16]uint16{
	30: 0x00, 31: 0x01, 32: 0x02, 33: 0x03, 35: 0x04, 34: 0x05, 44: 0x06, 45: 0x07,
	46: 0x08, 47: 0x09, Key102nd: 0x0A, 48: 0x0B, 16: 0x0C, 17: 0x0D, 18: 0x0E, 19: 0x0F,
	21: 0x10, 20: 0x11, 2: 0x12, 3: 0x13, 4: 0x14, 5: 0x15, 7: 0x16, 6: 0x17,
	Equal: 0x18, 10: 0x19, 8: 0x1A, Minus: 0x1B, 9: 0x1C, 11: 0x1D, RightBrace: 0x1E, 24: 0x1F,
	22: 0x20, LeftBrace: 0x21, 23: 0x22, 25: 0x23, Enter: 0x24, 38: 0x25, 36: 0x26, Apostrophe: 0x27,
	37: 0x28, Semicolon: 0x29, Backslash: 0x2A, Comma: 0x2B, Slash: 0x2C, 49: 0x2D, 50: 0x2E, Dot: 0x2F,
	Tab: 0x30, Space: 0x31, Grave: 0x32, Backspace: 0x33, Esc: 0x35,
	RightMeta: 0x36, LeftMeta: 0x37, LeftShift: 0x38, CapsLock: 0x39, LeftAlt: 0x3A, LeftCtrl: 0x3B,
	RightShift: 0x3C, RightAlt: 0x3D, RightCtrl: 0x3E,
	KPDot: 0x41, KPAsterisk: 0x43, KPPlus: 0x45, NumLock: 0x47,
	VolumeUp: 0x48, VolumeDown: 0x49, Mute: 0x4A,
	KPSlash: 0x4B, KPEnter: 0x4C, KPMinus: 0x4E, KPEqual: 0x51,
	KP0: 0x52, KP1: 0x53, KP2: 0x54, KP3: 0x55, KP4: 0x56, KP5: 0x57, KP6: 0x58, KP7: 0x59,
	KP8: 0x5B, KP9: 0x5C,
	63: 0x60, 64: 0x61, 65: 0x62, 61: 0x63, 66: 0x64, 67: 0x65, F11: 0x67,
	183: 0x69, 186: 0x6A, 184: 0x6B, F10: 0x6D, Compose: 0x6E, F12: 0x6F, 185: 0x71,
	Insert: 0x72, Home: 0x73, PageUp: 0x74, Delete: 0x75, 62: 0x76, End: 0x77, 60: 0x78,
	PageDown: 0x79, F1: 0x7A, Left_: 0x7B, Right_: 0x7C, Down_: 0x7D, Up_: 0x7E,
}

var macToCode = invert(codeToMac)

// ToMac returns the macOS virtual key code for a key.
func ToMac(code uint16) (uint16, bool) {
	k, ok := codeToMac[code]
	return k, ok
}

// FromMac returns the key code for a macOS virtual key code, or 0.
func FromMac(keycode uint16) uint16 { return macToCode[keycode] }

func invert(m map[uint16]uint16) map[uint16]uint16 {
	out := make(map[uint16]uint16, len(m))
	for k, v := range m {
		if _, dup := out[v]; dup {
			panic(fmt.Sprintf("keys: duplicate value %#x in table", v))
		}
		out[v] = k
	}
	return out
}
