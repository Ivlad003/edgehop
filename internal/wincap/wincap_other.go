//go:build !windows && !darwin

package wincap

import "errors"

var errUnsupported = errors.New("showing this computer's windows on another computer is not supported on this operating system yet")

// Supported reports whether windows can be listed and captured here.
func Supported() error { return errUnsupported }

// List returns the windows that can be shown remotely.
func (System) List() ([]Window, error) { return nil, errUnsupported }

// Open prepares capturing the window with the given id.
func (System) Open(id uint64) (Capturer, error) { return nil, errUnsupported }

// RestoreHidden has nothing to do on this system.
func RestoreHidden() int { return 0 }

// Dragged returns the window the user is moving with the mouse right now.
// This system offers no way to tell, so dragging a window to another
// computer starts only on Windows.
func (System) Dragged() (Window, bool) { return Window{}, false }

// Frontmost returns the window the user works in right now.
func (System) Frontmost() (Window, bool) { return Window{}, false }
