// Package debug provides a lightweight, file-backed debug logger for pluto.
package debug

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultFile = "pluto-debug.log"

var (
	mu      sync.Mutex
	logger  *log.Logger // nil when disabled
	closer  io.Closer   // the open log file, closed by Close
	enabled bool
)

// Init configures the logger from the environment.
func Init() (path string, err error) {
	mu.Lock()
	defer mu.Unlock()
	if logger != nil {
		return "", nil // already initialized
	}
	if !truthy(os.Getenv("PLUTO_DEBUG")) {
		return "", nil
	}

	path = os.Getenv("PLUTO_DEBUG_FILE")
	if path == "" {
		path = defaultFile
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", fmt.Errorf("debug: open log file %q: %w", path, err)
	}

	enabled = true
	closer = f
	// Microsecond timestamps; no std flags on the message body so component
	// tags line up.
	logger = log.New(f, "", 0)
	writeLine("=== pluto debug log opened %s ===", time.Now().Format(time.RFC3339))
	return path, nil
}

// Enabled reports whether debug logging is active.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

// Log writes a single line tagged with the given component.
func Log(component, msg string) {
	mu.Lock()
	defer mu.Unlock()
	if logger == nil {
		return
	}
	writeLine("[%s] %s", component, msg)
}

// Logf is Log with printf-style formatting.
func Logf(component, format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if logger == nil {
		return
	}
	writeLine("[%s] %s", component, fmt.Sprintf(format, args...))
}

// Close flushes and closes the underlying log file.
func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if logger == nil {
		return nil
	}
	writeLine("=== pluto debug log closed %s ===", time.Now().Format(time.RFC3339))
	err := closer.Close()
	logger, closer, enabled = nil, nil, false
	return err
}

func writeLine(format string, args ...any) {
	ts := time.Now().Format("15:04:05.000000")
	logger.Printf("%s %s", ts, fmt.Sprintf(format, args...))
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes", "y":
		return true
	default:
		return false
	}
}
