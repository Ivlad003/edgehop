package rview

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// A window that was dragged over should appear as a window of its own, where
// the cursor is - not as one more tab somewhere. Chrome and the browsers
// built on it can do that: started with --app they show a page in a plain
// window of a given size at a given place. Other browsers cannot, and the
// caller falls back to opening the link the ordinary way.

// ErrNoAppBrowser means no browser that can show a page as a plain window
// was found on this computer.
var ErrNoAppBrowser = errors.New("no browser that can open a page as a separate window was found")

// appBrowsers lists where such browsers usually live.
func appBrowsers() []string {
	switch runtime.GOOS {
	case "windows":
		var out []string
		roots := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")}
		for _, rel := range []string{
			`Google\Chrome\Application\chrome.exe`,
			`Microsoft\Edge\Application\msedge.exe`,
			`BraveSoftware\Brave-Browser\Application\brave.exe`,
			`Chromium\Application\chrome.exe`,
		} {
			for _, root := range roots {
				if root != "" {
					out = append(out, filepath.Join(root, rel))
				}
			}
		}
		return out
	case "darwin":
		var out []string
		home, _ := os.UserHomeDir()
		for _, app := range []string{
			"Google Chrome.app/Contents/MacOS/Google Chrome",
			"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"Brave Browser.app/Contents/MacOS/Brave Browser",
			"Chromium.app/Contents/MacOS/Chromium",
		} {
			out = append(out, filepath.Join("/Applications", app))
			if home != "" {
				out = append(out, filepath.Join(home, "Applications", app))
			}
		}
		return out
	default:
		var out []string
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"} {
			if path, err := exec.LookPath(name); err == nil {
				out = append(out, path)
			}
		}
		return out
	}
}

// appWindowArgs are the arguments that make such a browser show link as a
// plain window of w x h at x, y.
func appWindowArgs(link string, x, y, w, h int) []string {
	return []string{
		"--app=" + link,
		fmt.Sprintf("--window-size=%d,%d", w, h),
		fmt.Sprintf("--window-position=%d,%d", x, y),
	}
}

// OpenAppWindow shows link in a window of its own, w x h at x, y on the
// desktop. It returns ErrNoAppBrowser when this computer has no browser that
// can do that.
func OpenAppWindow(link string, x, y, w, h int) error {
	for _, path := range appBrowsers() {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			continue
		}
		cmd := exec.Command(path, appWindowArgs(link, x, y, w, h)...)
		if err := cmd.Start(); err != nil {
			continue
		}
		go cmd.Wait() // the browser hands the window to its running copy and exits, or stays; either is fine
		return nil
	}
	return ErrNoAppBrowser
}
