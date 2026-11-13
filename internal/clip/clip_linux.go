//go:build linux

package clip

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Linux has no single clipboard interface: X11 and Wayland each have their
// own protocol. edgehop uses the standard command line tools for them:
// wl-copy / wl-paste (package wl-clipboard) under Wayland, xclip under X11.

const (
	uriList     = "text/uri-list"
	gnomeCopied = "x-special/gnome-copied-files" // what GNOME Files reads and writes
)

type toolClipboard struct {
	wayland bool
}

// New returns the clipboard of the current desktop session, or
// ErrUnavailable when the needed tool is not installed.
func New() (Clipboard, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if _, err := exec.LookPath("wl-copy"); err == nil {
			if _, err := exec.LookPath("wl-paste"); err == nil {
				return &toolClipboard{wayland: true}, nil
			}
		}
	}
	if os.Getenv("DISPLAY") != "" {
		if _, err := exec.LookPath("xclip"); err == nil {
			return &toolClipboard{}, nil
		}
	}
	return nil, ErrUnavailable
}

func (c *toolClipboard) Describe() string {
	if c.wayland {
		return "Wayland clipboard (wl-clipboard)"
	}
	return "X11 clipboard (xclip)"
}

func run(stdin []byte, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	if stdin == nil {
		cmd.Stdout = &out
	}
	err := cmd.Run()
	return out.Bytes(), err
}

// get reads the clipboard in the given format ("" = plain text). A format
// the clipboard does not offer yields an error.
func (c *toolClipboard) get(format string) ([]byte, error) {
	if c.wayland {
		args := []string{"--no-newline"}
		if format != "" {
			args = append(args, "--type", format)
		}
		return run(nil, "wl-paste", args...)
	}
	args := []string{"-selection", "clipboard", "-o"}
	if format != "" {
		args = append(args, "-t", format)
	}
	return run(nil, "xclip", args...)
}

// put offers data on the clipboard in the given format. Both tools keep
// running in the background to serve the data, and return once it is set.
func (c *toolClipboard) put(format string, data []byte) error {
	if c.wayland {
		args := []string{}
		if format != "" {
			args = append(args, "--type", format)
		}
		_, err := run(data, "wl-copy", args...)
		return err
	}
	args := []string{"-selection", "clipboard", "-i"}
	if format != "" {
		args = append(args, "-t", format)
	}
	_, err := run(data, "xclip", args...)
	return err
}

func parseURIs(data []byte) []string {
	var files []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "file://") {
			continue // also skips the "copy" / "cut" first line of the GNOME format
		}
		u, err := url.Parse(line)
		if err != nil || u.Path == "" {
			continue
		}
		files = append(files, filepath.FromSlash(u.Path))
	}
	return files
}

func (c *toolClipboard) Read() (Content, error) {
	for _, format := range []string{gnomeCopied, uriList} {
		if data, err := c.get(format); err == nil {
			if files := parseURIs(data); len(files) > 0 {
				return Content{Kind: Files, Files: files}, nil
			}
		}
	}
	if data, err := c.get(""); err == nil && len(data) > 0 {
		return Content{Kind: Text, Text: string(data)}, nil
	}
	return Content{}, nil
}

// Mark has no change counter to rely on here, so it summarises the content.
func (c *toolClipboard) Mark() (string, error) {
	content, err := c.Read()
	if err != nil {
		return "", err
	}
	return hash(append([]string{string(rune('0' + content.Kind)), content.Text}, content.Files...)...), nil
}

func (c *toolClipboard) WriteText(text string) error {
	return c.put("", []byte(text))
}

func (c *toolClipboard) WriteFiles(paths []string) error {
	if len(paths) == 0 {
		return errors.New("no files")
	}
	var uris []string
	for _, p := range paths {
		u := url.URL{Scheme: "file", Path: filepath.ToSlash(p)}
		uris = append(uris, u.String())
	}
	// GNOME Files only pastes its own format; other file managers read the
	// standard list. One program can offer only one of them at a time.
	if strings.Contains(strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP")), "GNOME") {
		return c.put(gnomeCopied, []byte("copy\n"+strings.Join(uris, "\n")))
	}
	return c.put(uriList, []byte(strings.Join(uris, "\r\n")+"\r\n"))
}
