// Package clip reads and writes the system clipboard: plain text and lists
// of copied files. Every operating system has its own implementation.
package clip

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"
)

// Kind says what a clipboard holds.
type Kind uint8

const (
	None  Kind = iota // empty, or something edgehop does not transfer (images ...)
	Text              // plain text
	Files             // files and folders copied in a file manager
)

// Content is what was read from, or should be put on, a clipboard.
type Content struct {
	Kind  Kind
	Text  string
	Files []string // absolute paths
}

// Clipboard is the system clipboard of one computer.
type Clipboard interface {
	// Mark returns a value that changes whenever the clipboard content
	// changes. Comparing two marks tells whether something new was copied.
	Mark() (string, error)
	Read() (Content, error)
	WriteText(text string) error
	WriteFiles(paths []string) error
	// Describe names the mechanism in use, for the log.
	Describe() string
}

// ErrUnavailable is returned by New when this computer offers no way to
// reach the clipboard (for example Linux without xclip or wl-clipboard).
var ErrUnavailable = errors.New("no clipboard access on this computer")

func hash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// Memory is an in-process clipboard, used in tests.
type Memory struct {
	mu      sync.Mutex
	content Content
	seq     int
}

func (m *Memory) Mark() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strconv.Itoa(m.seq), nil
}

func (m *Memory) Read() (Content, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.content
	c.Files = append([]string(nil), m.content.Files...)
	return c, nil
}

func (m *Memory) WriteText(text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.content = Content{Kind: Text, Text: text}
	m.seq++
	return nil
}

func (m *Memory) WriteFiles(paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.content = Content{Kind: Files, Files: append([]string(nil), paths...)}
	m.seq++
	return nil
}

func (m *Memory) Describe() string { return "in-memory clipboard" }
