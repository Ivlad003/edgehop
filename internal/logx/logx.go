// Package logx is a minimal leveled logger.
package logx

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var (
	verbose atomic.Bool
	mu      sync.Mutex
	out     io.Writer = os.Stderr
)

// SetVerbose turns debug output on or off.
func SetVerbose(v bool) { verbose.Store(v) }

// Verbose reports whether debug output is on.
func Verbose() bool { return verbose.Load() }

// SetOutput redirects the log (used by tests).
func SetOutput(w io.Writer) {
	mu.Lock()
	out = w
	mu.Unlock()
}

func write(level, format string, args []any) {
	line := fmt.Sprintf(format, args...)
	mu.Lock()
	fmt.Fprintf(out, "%s %s%s\n", time.Now().Format("15:04:05.000"), level, line)
	mu.Unlock()
}

// Infof logs a normal message.
func Infof(format string, args ...any) { write("", format, args) }

// Warnf logs a problem the program can live with.
func Warnf(format string, args ...any) { write("warning: ", format, args) }

// Debugf logs only in verbose mode.
func Debugf(format string, args ...any) {
	if verbose.Load() {
		write("debug: ", format, args)
	}
}
