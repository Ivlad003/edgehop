// Package wincap lists the windows on this computer's desktop and takes
// pictures of single windows. It serves the experimental "remote windows"
// feature: a window of one computer shown, and controlled, on another.
//
// Every operating system has its own file; systems without one report that
// the feature is not available there.
package wincap

import "errors"

// Window describes one window that can be shown remotely.
type Window struct {
	ID    uint64 `json:"id"`
	Title string `json:"title"`
	App   string `json:"app"` // the program it belongs to
	W     int    `json:"w"`   // 0 while the window is minimised
	H     int    `json:"h"`
}

// Frame is one picture of a window.
type Frame struct {
	// Pix holds the picture as RGBA, four bytes a pixel, rows from top to
	// bottom without padding.
	Pix  []byte
	W, H int
	// X, Y is where the picture's top-left pixel sits on the desktop, in the
	// coordinates input.Injector.MoveAbs takes. One pixel of the picture is
	// one unit of those coordinates.
	X, Y  int
	Title string
}

// Capturer takes pictures of one window.
type Capturer interface {
	// Capture takes a picture into f, reusing f.Pix when it is large enough.
	Capture(f *Frame) error
	// Front reports whether the window is the one the user works in.
	Front() bool
	// Raise brings the window in front of the others, so that clicks at its
	// position reach it.
	Raise()
	Close()
}

// ErrGone means the window no longer exists.
var ErrGone = errors.New("the window was closed")

// System is the set of windows of this computer.
type System struct{}

// grow returns a slice of n bytes, reusing b when it is large enough.
func grow(b []byte, n int) []byte {
	if cap(b) >= n {
		return b[:n]
	}
	return make([]byte, n)
}
