// Package config loads and writes the edgehop.toml configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
)

// DefaultPort is used when no port is given.
const DefaultPort = 24920

// DefaultClipboardMaxMB is the largest clipboard content transferred.
const DefaultClipboardMaxMB = 200

// DefaultHotkey forces the cursor back to the server.
const DefaultHotkey = "ctrl+alt+shift+backspace"

// Config mirrors edgehop.toml.
type Config struct {
	Role string `toml:"role"` // "server" or "client"
	Name string `toml:"name"` // this computer's screen name
	Key  string `toml:"key"`  // shared secret, identical on every computer

	// Server only.
	Listen        string            `toml:"listen,omitempty"`
	Row           []string          `toml:"row,omitempty"`
	ReleaseHotkey *string           `toml:"release_hotkey"`
	HideCursor    *bool             `toml:"hide_cursor"`
	PointerSpeed  float64           `toml:"pointer_speed,omitzero"` // Linux server only
	PointerAccel  *float64          `toml:"pointer_accel"`          // Linux server only
	Links         []Link            `toml:"links,omitempty"`
	Screens       map[string]Screen `toml:"screens,omitempty"`

	// Client only.
	Server string `toml:"server,omitempty"`

	// Both: share the clipboard (text and copied files) with the other
	// computers. On by default; content above the limit is not transferred.
	Clipboard      *bool `toml:"clipboard"`
	ClipboardMaxMB int   `toml:"clipboard_max_mb,omitzero"`

	// Both: override the detected desktop size, "1920x1080".
	ScreenSize string `toml:"screen_size,omitempty"`

	path string
}

// Link places one screen next to another. It is applied in both directions
// unless OneWay is set.
type Link struct {
	From   string `toml:"from"`
	Side   string `toml:"side"`
	To     string `toml:"to"`
	OneWay bool   `toml:"one_way,omitempty"`
}

// Screen holds per-client options on the server.
type Screen struct {
	// Scale multiplies the cursor speed on that screen (default 1.0).
	Scale float64 `toml:"scale,omitzero"`
	// Remap replaces keys sent to that screen, e.g. {alt = "meta", meta = "alt"}.
	Remap map[string]string `toml:"remap,omitempty"`
}

// Path returns the file the config was loaded from.
func (c *Config) Path() string { return c.path }

// DefaultPath returns where the config is looked for when no path is given:
// $EDGEHOP_CONFIG, then ./edgehop.toml if it exists, then the per-user
// configuration directory.
func DefaultPath() string {
	if p := os.Getenv("EDGEHOP_CONFIG"); p != "" {
		return p
	}
	if _, err := os.Stat("edgehop.toml"); err == nil {
		return "edgehop.toml"
	}
	return UserPath()
}

// UserPath returns the per-user config file location.
func UserPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "edgehop.toml"
	}
	return filepath.Join(dir, "edgehop", "edgehop.toml")
}

// DefaultName derives a screen name from the host name.
func DefaultName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "computer"
	}
	h = strings.ToLower(h)
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	return h
}

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no config file at %s - start `edgehop` to set this computer up (or use `edgehop init`)", path)
		}
		return nil, err
	}
	c, err := Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.path = path
	return c, nil
}

// Parse parses and validates config text.
func Parse(text string) (*Config, error) {
	var c Config
	md, err := toml.Decode(text, &c)
	if err != nil {
		return nil, err
	}
	if un := md.Undecoded(); len(un) > 0 {
		return nil, fmt.Errorf("unknown setting %q", un[0].String())
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	c.Role = strings.ToLower(strings.TrimSpace(c.Role))
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		c.Name = DefaultName()
	}
	if strings.TrimSpace(c.Key) == "" {
		return errors.New("`key` is missing")
	}
	if _, _, err := c.Size(); err != nil {
		return err
	}
	switch c.Role {
	case "server":
		if c.Listen == "" {
			c.Listen = fmt.Sprintf("0.0.0.0:%d", DefaultPort)
		}
		if _, _, err := net.SplitHostPort(c.Listen); err != nil {
			return fmt.Errorf("`listen` must look like 0.0.0.0:%d: %w", DefaultPort, err)
		}
		if _, err := c.Layout(); err != nil {
			return err
		}
		if _, err := c.Hotkey(); err != nil {
			return err
		}
		for name, s := range c.Screens {
			if s.Scale < 0 {
				return fmt.Errorf("screens.%s.scale must be positive", name)
			}
			if _, err := keys.ParseRemap(s.Remap); err != nil {
				return fmt.Errorf("screens.%s: %w", name, err)
			}
		}
	case "client":
		if c.Server == "" {
			return errors.New("`server` is missing (the address of the computer the mouse is connected to)")
		}
		c.Server = WithPort(c.Server)
	default:
		return fmt.Errorf("`role` must be \"server\" or \"client\", not %q", c.Role)
	}
	return nil
}

// WithPort appends the default port when addr has none.
func WithPort(addr string) string {
	addr = strings.TrimSpace(addr)
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(strings.Trim(addr, "[]"), strconv.Itoa(DefaultPort))
}

// Layout builds the screen arrangement from `row` and `links`.
func (c *Config) Layout() (*layout.Layout, error) {
	l := layout.New()
	l.Add(c.Name)
	seen := map[string]bool{}
	for _, n := range c.Row {
		if strings.TrimSpace(n) == "" {
			return nil, errors.New("`row` contains an empty name")
		}
		if seen[n] {
			return nil, fmt.Errorf("`row` lists %q twice", n)
		}
		seen[n] = true
	}
	if len(c.Row) > 0 && !seen[c.Name] {
		return nil, fmt.Errorf("`row` must contain this computer's own name %q so edgehop knows where the others are relative to it", c.Name)
	}
	l.AddRow(c.Row)
	for i, ln := range c.Links {
		if ln.From == "" || ln.To == "" {
			return nil, fmt.Errorf("links[%d]: `from` and `to` are required", i)
		}
		if ln.From == ln.To {
			return nil, fmt.Errorf("links[%d]: a screen cannot be linked to itself", i)
		}
		side, err := layout.ParseSide(ln.Side)
		if err != nil {
			return nil, fmt.Errorf("links[%d]: %w", i, err)
		}
		if ln.OneWay {
			l.Link(ln.From, side, ln.To)
		} else {
			l.LinkBoth(ln.From, side, ln.To)
		}
	}
	for name := range c.Screens {
		if !l.Has(name) {
			return nil, fmt.Errorf("[screens.%s] is configured but %q is not in `row` or `links`", name, name)
		}
	}
	return l, nil
}

// Hotkey returns the parsed release hotkey.
func (c *Config) Hotkey() (keys.Hotkey, error) {
	s := DefaultHotkey
	if c.ReleaseHotkey != nil {
		s = *c.ReleaseHotkey
	}
	return keys.ParseHotkey(s)
}

// ClipboardEnabled reports whether the clipboard is shared (default true).
func (c *Config) ClipboardEnabled() bool { return c.Clipboard == nil || *c.Clipboard }

// ClipboardLimit returns the largest clipboard content to transfer, in bytes.
func (c *Config) ClipboardLimit() int64 {
	mb := c.ClipboardMaxMB
	if mb <= 0 {
		mb = DefaultClipboardMaxMB
	}
	return int64(mb) << 20
}

// HideCursorEnabled reports whether the server should hide its cursor while
// controlling another computer (default true).
func (c *Config) HideCursorEnabled() bool { return c.HideCursor == nil || *c.HideCursor }

var sizeRe = regexp.MustCompile(`^\s*(\d+)\s*[xX×, ]\s*(\d+)\s*$`)

// Size returns the screen_size override, or 0,0 when it is not set.
func (c *Config) Size() (int, int, error) {
	if strings.TrimSpace(c.ScreenSize) == "" {
		return 0, 0, nil
	}
	m := sizeRe.FindStringSubmatch(c.ScreenSize)
	if m == nil {
		return 0, 0, fmt.Errorf("`screen_size` must look like \"1920x1080\", not %q", c.ScreenSize)
	}
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	if w < 100 || h < 100 || w > 100000 || h > 100000 {
		return 0, 0, fmt.Errorf("`screen_size` %q is out of range", c.ScreenSize)
	}
	return w, h, nil
}

// ServerTemplate returns the text of a new server config.
func ServerTemplate(name, key string, port int, row []string) string {
	if len(row) == 0 {
		row = []string{name}
	}
	quoted := make([]string, len(row))
	for i, r := range row {
		quoted[i] = strconv.Quote(r)
	}
	var example string
	for _, r := range row {
		if r != name {
			example = r
			break
		}
	}
	if example == "" {
		example = "macbook"
	}
	return fmt.Sprintf(`# edgehop - this computer has the mouse and keyboard connected to it.
role = "server"

# The name of this screen. Every computer needs a different name.
name = %q

# Shared secret. Must be identical on every computer.
key = %q

listen = "0.0.0.0:%d"

# The screens from left to right, the way the monitors stand on the desk.
# Must contain this computer's own name. Example: ["laptop", %q, "macbook"]
row = [%s]

# Screens above / below each other are added with links:
# [[links]]
# from = %q
# side = "top"        # left, right, top or bottom
# to   = "tv"

# Pressing this returns the cursor to this computer, whatever happens.
release_hotkey = %q

# Hide this computer's cursor while it controls another one.
hide_cursor = true

# Per-screen options (optional):
# [screens.%s]
# scale = 1.0                             # cursor speed on that screen
# remap = { alt = "meta", meta = "alt" }  # e.g. PC keyboard driving a Mac
`, name, key, port, name, strings.Join(quoted, ", "), name, DefaultHotkey, example)
}

// ClientTemplate returns the text of a new client config.
func ClientTemplate(name, key, server string) string {
	return fmt.Sprintf(`# edgehop - this computer is controlled by the mouse and keyboard of the server.
role = "client"

# The name of this screen. It must appear in the server's "row" (or links).
name = %q

# Shared secret. Must be identical to the server's key.
key = %q

# Address of the server (the computer with the mouse).
server = %q

# Only needed if the desktop size is detected incorrectly (mostly on Linux):
# screen_size = "1920x1080"
`, name, key, WithPort(server))
}

// Write creates the file (and its directory). It refuses to overwrite an
// existing file unless force is set.
func Write(path, text string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite it)", path)
		}
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

// ---------------------------------------------------------------------------
// Editing the configuration from the web interface

// Form is the part of the configuration the web interface edits.
type Form struct {
	Role           string                `json:"role"`
	Name           string                `json:"name"`
	Key            string                `json:"key"`
	Port           int                   `json:"port"`
	Row            []string              `json:"row"`   // screens from left to right
	Above          string                `json:"above"` // the screen above this computer, if any
	Below          string                `json:"below"` // the screen below this computer, if any
	Server         string                `json:"server"`
	ReleaseHotkey  string                `json:"releaseHotkey"`
	HideCursor     bool                  `json:"hideCursor"`
	ScreenSize     string                `json:"screenSize"`
	Clipboard      bool                  `json:"clipboard"`
	ClipboardMaxMB int                   `json:"clipboardMaxMB"`
	Screens        map[string]FormScreen `json:"screens"`
}

// FormScreen holds the per-screen options shown in the web interface.
type FormScreen struct {
	Scale       float64 `json:"scale"`
	SwapAltMeta bool    `json:"swapAltMeta"` // PC keyboard driving a Mac, or the reverse
}

func hasSwap(remap map[string]string) bool {
	return strings.EqualFold(remap["alt"], "meta") && strings.EqualFold(remap["meta"], "alt")
}

// Port returns the port the server listens on.
func (c *Config) Port() int {
	if _, p, err := net.SplitHostPort(c.Listen); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
	}
	return DefaultPort
}

// Form returns the editable view of the configuration.
func (c *Config) Form() Form {
	f := Form{
		Role: c.Role, Name: c.Name, Key: c.Key,
		Port:           c.Port(),
		Row:            append([]string{}, c.Row...),
		Server:         c.Server,
		ReleaseHotkey:  DefaultHotkey,
		HideCursor:     c.HideCursorEnabled(),
		ScreenSize:     c.ScreenSize,
		Clipboard:      c.ClipboardEnabled(),
		ClipboardMaxMB: int(c.ClipboardLimit() >> 20),
		Screens:        map[string]FormScreen{},
	}
	if c.ReleaseHotkey != nil {
		f.ReleaseHotkey = *c.ReleaseHotkey
	}
	if lay, err := c.Layout(); err == nil {
		f.Above = lay.Neighbor(c.Name, layout.Top)
		f.Below = lay.Neighbor(c.Name, layout.Bottom)
	}
	for name, s := range c.Screens {
		scale := s.Scale
		if scale == 0 {
			scale = 1
		}
		f.Screens[name] = FormScreen{Scale: scale, SwapAltMeta: hasSwap(s.Remap)}
	}
	return f
}

// Apply copies the form into the configuration. Settings the form does not
// cover (links, other key remaps, pointer speed) are kept as they are.
func (c *Config) Apply(f Form) {
	c.Role = strings.ToLower(strings.TrimSpace(f.Role))
	c.Name = strings.TrimSpace(f.Name)
	c.Key = strings.TrimSpace(f.Key)
	c.ScreenSize = strings.TrimSpace(f.ScreenSize)
	share := f.Clipboard
	c.Clipboard = &share
	c.ClipboardMaxMB = f.ClipboardMaxMB
	if c.ClipboardMaxMB == DefaultClipboardMaxMB || c.ClipboardMaxMB < 0 {
		c.ClipboardMaxMB = 0 // the default; leave it out of the file
	}
	if c.Role == "client" {
		c.Server = strings.TrimSpace(f.Server)
		return
	}
	host := "0.0.0.0"
	if h, _, err := net.SplitHostPort(c.Listen); err == nil && h != "" {
		host = h
	}
	port := f.Port
	if port <= 0 || port > 65535 {
		port = DefaultPort
	}
	c.Listen = net.JoinHostPort(host, strconv.Itoa(port))

	c.Row = nil
	for _, n := range f.Row {
		if n = strings.TrimSpace(n); n != "" {
			c.Row = append(c.Row, n)
		}
	}
	// The screens above and below this computer are kept as links. Replace
	// whatever linked this computer vertically before; other links stay.
	vertical := func(side string) bool {
		s, err := layout.ParseSide(side)
		return err == nil && (s == layout.Top || s == layout.Bottom)
	}
	kept := c.Links[:0:0]
	for _, l := range c.Links {
		if vertical(l.Side) && (l.From == c.Name || l.To == c.Name) {
			continue
		}
		kept = append(kept, l)
	}
	c.Links = kept
	if above := strings.TrimSpace(f.Above); above != "" && above != c.Name {
		c.Links = append(c.Links, Link{From: c.Name, Side: "top", To: above})
	}
	if below := strings.TrimSpace(f.Below); below != "" && below != c.Name {
		c.Links = append(c.Links, Link{From: c.Name, Side: "bottom", To: below})
	}
	if len(c.Row) == 1 && c.Row[0] == c.Name {
		c.Row = nil // a server on its own needs no row
	}
	hotkey := strings.TrimSpace(f.ReleaseHotkey)
	c.ReleaseHotkey = &hotkey
	hide := f.HideCursor
	c.HideCursor = &hide

	// Per-screen options: only for screens that are still part of the layout.
	known := map[string]bool{}
	for _, n := range c.Row {
		known[n] = true
	}
	for _, l := range c.Links {
		known[l.From], known[l.To] = true, true
	}
	for name := range c.Screens {
		if !known[name] || name == c.Name {
			delete(c.Screens, name)
		}
	}
	for name, fs := range f.Screens {
		if !known[name] || name == c.Name {
			continue
		}
		s := c.Screens[name]
		s.Scale = fs.Scale
		if s.Scale == 1 || s.Scale < 0 {
			s.Scale = 0 // 1.0 is the default; leave it out of the file
		}
		if fs.SwapAltMeta != hasSwap(s.Remap) {
			if s.Remap == nil {
				s.Remap = map[string]string{}
			}
			if fs.SwapAltMeta {
				s.Remap["alt"], s.Remap["meta"] = "meta", "alt"
			} else {
				delete(s.Remap, "alt")
				delete(s.Remap, "meta")
			}
		}
		if s.Scale == 0 && len(s.Remap) == 0 {
			delete(c.Screens, name)
			continue
		}
		if c.Screens == nil {
			c.Screens = map[string]Screen{}
		}
		c.Screens[name] = s
	}
}

// Encode returns the configuration as TOML text.
func (c *Config) Encode() (string, error) {
	var buf bytes.Buffer
	buf.WriteString("# edgehop configuration. Edit it here or in the web interface (run `edgehop`).\n")
	buf.WriteString("# The key is a secret: anyone who has it can control these computers.\n\n")
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(c); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Save validates the configuration and writes it to path.
func Save(path string, c *Config) error {
	text, err := c.Encode()
	if err != nil {
		return err
	}
	if _, err := Parse(text); err != nil {
		return err
	}
	return Write(path, text, true)
}
