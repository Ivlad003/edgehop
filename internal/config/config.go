// Package config loads and writes the edgehop.toml configuration file.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"edgehop/internal/keys"
	"edgehop/internal/layout"
)

// DefaultPort is used when no port is given.
const DefaultPort = 24920

// DefaultHotkey forces the cursor back to the server.
const DefaultHotkey = "ctrl+alt+shift+backspace"

// Config mirrors edgehop.toml.
type Config struct {
	Role string `toml:"role"` // "server" or "client"
	Name string `toml:"name"` // this computer's screen name
	Key  string `toml:"key"`  // shared secret, identical on every computer

	// Server only.
	Listen        string            `toml:"listen"`
	Row           []string          `toml:"row"`
	Links         []Link            `toml:"links"`
	Screens       map[string]Screen `toml:"screens"`
	ReleaseHotkey *string           `toml:"release_hotkey"`
	HideCursor    *bool             `toml:"hide_cursor"`
	PointerSpeed  float64           `toml:"pointer_speed"` // Linux server only
	PointerAccel  *float64          `toml:"pointer_accel"` // Linux server only

	// Client only.
	Server string `toml:"server"`

	// Both: override the detected desktop size, "1920x1080".
	ScreenSize string `toml:"screen_size"`

	path string
}

// Link places one screen next to another. It is applied in both directions
// unless OneWay is set.
type Link struct {
	From   string `toml:"from"`
	Side   string `toml:"side"`
	To     string `toml:"to"`
	OneWay bool   `toml:"one_way"`
}

// Screen holds per-client options on the server.
type Screen struct {
	// Scale multiplies the cursor speed on that screen (default 1.0).
	Scale float64 `toml:"scale"`
	// Remap replaces keys sent to that screen, e.g. {alt = "meta", meta = "alt"}.
	Remap map[string]string `toml:"remap"`
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
			return nil, fmt.Errorf("no config file at %s - create one with `edgehop init server` or `edgehop init client`", path)
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
