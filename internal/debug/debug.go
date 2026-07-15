// Package debug provides a lightweight, file-backed structured logger for pluto.
//
// When enabled (PLUTO_DEBUG), it records timestamped, leveled, component-tagged
// events with stable key=value fields so a whole session — including per-frame
// TUI renders — can be reconstructed from the log. When disabled it is a no-op
// with a fast nil-logger path. See the "Debugging" section of the README for the
// full configuration surface (PLUTO_DEBUG_LEVEL / _COMPONENTS / _FRAMES / _FILE).
package debug

import (
	"fmt"
	"io"
	"log"
	"os"
	rdebug "runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultFile = "pluto-debug.log"

// Level is an event severity. Higher is more severe; events below the configured
// minimum (PLUTO_DEBUG_LEVEL, default DEBUG) are dropped. TRACE is reserved for
// high-volume events (per-frame renders) so it can be toggled independently.
type Level int

const (
	LevelTrace Level = iota
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
)

// FramesMode controls the UI frame-render firehose (PLUTO_DEBUG_FRAMES).
type FramesMode int

const (
	// FramesOff suppresses frame renders entirely.
	FramesOff FramesMode = iota
	// FramesCoalesced (default) collapses identical consecutive frames into a
	// single "frame unchanged xN" line emitted when the frame next changes.
	FramesCoalesced
	// FramesFull additionally dumps the full rendered body of each frame.
	FramesFull
)

var (
	mu      sync.Mutex
	logger  *log.Logger // nil when disabled
	closer  io.Closer   // the open log file, closed by Close
	enabled bool

	minLevel   = LevelDebug
	components componentFilter
	frames     FramesMode

	// Frame coalescing state (guarded by mu).
	lastFrameFP string
	frameRepeat int
	haveFrame   bool
)

// Init configures the logger from the environment. It is idempotent: a second
// call while already initialized is a no-op.
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
	minLevel = parseLevel(os.Getenv("PLUTO_DEBUG_LEVEL"), LevelDebug)
	components = parseComponents(os.Getenv("PLUTO_DEBUG_COMPONENTS"))
	frames = parseFrames(os.Getenv("PLUTO_DEBUG_FRAMES"), FramesCoalesced)
	lastFrameFP, frameRepeat, haveFrame = "", 0, false
	// Empty prefix + no std flags so our own timestamp/level/component columns
	// line up and stay diffable across runs.
	logger = log.New(f, "", 0)
	writeLine("=== pluto debug log opened %s (level=%s components=%q frames=%s) ===",
		time.Now().Format(time.RFC3339), levelName(minLevel), os.Getenv("PLUTO_DEBUG_COMPONENTS"), framesName(frames))
	return path, nil
}

// Enabled reports whether debug logging is active.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

// Frames reports the configured frame-render mode.
func Frames() FramesMode {
	mu.Lock()
	defer mu.Unlock()
	return frames
}

// Should reports whether an event for component at level would be emitted. Hot
// paths use it to gate expensive field formatting before calling Event.
func Should(component string, level Level) bool {
	mu.Lock()
	defer mu.Unlock()
	return logger != nil && level >= minLevel && components.allows(component)
}

// FramesEnabled reports whether frame renders for component would be logged
// (frames mode on, TRACE reachable, component allowed). Callers use it to skip
// building a frame body when nothing would be written.
func FramesEnabled(component string) bool {
	mu.Lock()
	defer mu.Unlock()
	return logger != nil && frames != FramesOff && LevelTrace >= minLevel && components.allows(component)
}

// Event writes one structured line: TS LEVEL [component] msg key=value ... The
// kv pairs are rendered in the order given, so key order is stable and diffable.
func Event(component string, level Level, msg string, kv ...any) {
	mu.Lock()
	defer mu.Unlock()
	emitLocked(level, component, msg, kv...)
}

// Trace/Debug/Info/Warn/Error are level-specific shorthands for Event.
func Trace(component, msg string, kv ...any) { Event(component, LevelTrace, msg, kv...) }
func Debug(component, msg string, kv ...any) { Event(component, LevelDebug, msg, kv...) }
func Info(component, msg string, kv ...any)  { Event(component, LevelInfo, msg, kv...) }
func Warn(component, msg string, kv ...any)  { Event(component, LevelWarn, msg, kv...) }
func Error(component, msg string, kv ...any) { Event(component, LevelError, msg, kv...) }

// Log writes a single DEBUG line tagged with component (compatibility shim).
func Log(component, msg string) { Event(component, LevelDebug, msg) }

// Logf is Log with printf-style formatting (compatibility shim).
func Logf(component, format string, args ...any) {
	Event(component, LevelDebug, fmt.Sprintf(format, args...))
}

// Timer measures a duration and logs it as a dur= field on Stop.
type Timer struct {
	component string
	msg       string
	level     Level
	start     time.Time
	on        bool
}

// NewTimer starts a DEBUG-level timer for msg. Stop appends dur=… and any extra
// fields. When logging is disabled the timer is inert.
func NewTimer(component, msg string) *Timer {
	return &Timer{component: component, msg: msg, level: LevelDebug, start: time.Now(), on: Enabled()}
}

// Stop logs the elapsed time (dur=…) plus any extra fields. Safe on a nil timer.
func (t *Timer) Stop(kv ...any) {
	if t == nil || !t.on {
		return
	}
	kv = append(kv, "dur", time.Since(t.start))
	Event(t.component, t.level, t.msg, kv...)
}

// Frame records a UI frame render at TRACE, coalescing identical consecutive
// frames per the configured FramesMode. fingerprint uniquely captures the
// visible state; body is the full rendered string, included only in FramesFull.
func Frame(component, fingerprint, body string, kv ...any) {
	mu.Lock()
	defer mu.Unlock()
	if logger == nil || frames == FramesOff || LevelTrace < minLevel || !components.allows(component) {
		return
	}
	if haveFrame && fingerprint == lastFrameFP {
		frameRepeat++
		return
	}
	flushFrameLocked(component)
	lastFrameFP, frameRepeat, haveFrame = fingerprint, 0, true
	emitLocked(LevelTrace, component, "frame render", kv...)
	if frames == FramesFull && body != "" {
		writeLine("--- frame body ---\n%s\n--- end frame ---", body)
	}
}

// flushFrameLocked emits a pending "frame unchanged xN" line for coalesced runs.
func flushFrameLocked(component string) {
	if frameRepeat > 0 {
		emitLocked(LevelTrace, component, "frame unchanged", "repeated", frameRepeat)
		frameRepeat = 0
	}
}

// Redact returns a token/secret rendered so the value never appears in the log,
// keeping only its length for correlation. Always use it in the auth path.
func Redact(secret string) string {
	if secret == "" {
		return "<empty>"
	}
	return fmt.Sprintf("<redacted %dch>", len(secret))
}

// LogPanic, deferred in main, records a panic (with stack) then re-panics so the
// process still crashes with its normal traceback.
func LogPanic() {
	if r := recover(); r != nil {
		Event("lifecycle", LevelError, "panic", "value", fmt.Sprint(r), "stack", "\n"+string(rdebug.Stack()))
		panic(r)
	}
}

// Close flushes and closes the underlying log file.
func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if logger == nil {
		return nil
	}
	flushFrameLocked("tui")
	writeLine("=== pluto debug log closed %s ===", time.Now().Format(time.RFC3339))
	err := closer.Close()
	logger, closer, enabled = nil, nil, false
	lastFrameFP, frameRepeat, haveFrame = "", 0, false
	return err
}

// emitLocked renders and writes one event; the caller holds mu.
func emitLocked(level Level, component, msg string, kv ...any) {
	if logger == nil || level < minLevel || !components.allows(component) {
		return
	}
	var b strings.Builder
	b.WriteString(levelName(level))
	b.WriteString(" [")
	b.WriteString(component)
	b.WriteString("] ")
	b.WriteString(msg)
	appendFields(&b, kv)
	writeLine("%s", b.String())
}

// appendFields renders kv as space-separated key=value pairs.
func appendFields(b *strings.Builder, kv []any) {
	for i := 0; i < len(kv); i += 2 {
		b.WriteByte(' ')
		b.WriteString(fmt.Sprint(kv[i]))
		b.WriteByte('=')
		if i+1 < len(kv) {
			b.WriteString(formatValue(kv[i+1]))
		} else {
			b.WriteString("?") // dangling key with no value
		}
	}
}

// formatValue renders a field value, quoting strings that contain whitespace or
// separators so pairs stay unambiguous.
func formatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "<nil>"
	case string:
		return quoteIfNeeded(x)
	case time.Duration:
		return x.String()
	case error:
		return quoteIfNeeded(x.Error())
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	default:
		return quoteIfNeeded(fmt.Sprint(v))
	}
}

func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\n\"=") {
		return strconv.Quote(s)
	}
	return s
}

// writeLine prefixes a microsecond timestamp and writes one log record.
func writeLine(format string, args ...any) {
	ts := time.Now().Format("15:04:05.000000")
	logger.Printf("%s %s", ts, fmt.Sprintf(format, args...))
}

func levelName(l Level) string {
	switch l {
	case LevelTrace:
		return "TRACE"
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO "
	case LevelWarn:
		return "WARN "
	case LevelError:
		return "ERROR"
	default:
		return "?????"
	}
}

func parseLevel(s string, def Level) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return LevelTrace
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return def
	}
}

func framesName(m FramesMode) string {
	switch m {
	case FramesOff:
		return "off"
	case FramesFull:
		return "full"
	default:
		return "coalesced"
	}
}

func parseFrames(s string, def FramesMode) FramesMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "0", "false", "no":
		return FramesOff
	case "full":
		return FramesFull
	case "coalesced", "on", "1", "true", "yes":
		return FramesCoalesced
	default:
		return def
	}
}

// componentFilter includes and/or excludes components. A "-" prefix in
// PLUTO_DEBUG_COMPONENTS excludes; bare names include. When any include is
// present, only included (and not excluded) components pass.
type componentFilter struct {
	include map[string]bool
	exclude map[string]bool
}

func parseComponents(s string) componentFilter {
	f := componentFilter{include: map[string]bool{}, exclude: map[string]bool{}}
	for _, part := range strings.Split(s, ",") {
		p := strings.ToLower(strings.TrimSpace(part))
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "-") {
			if name := strings.TrimSpace(strings.TrimPrefix(p, "-")); name != "" {
				f.exclude[name] = true
			}
			continue
		}
		f.include[p] = true
	}
	return f
}

func (f componentFilter) allows(component string) bool {
	c := strings.ToLower(component)
	if f.exclude[c] {
		return false
	}
	if len(f.include) > 0 {
		return f.include[c]
	}
	return true
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes", "y":
		return true
	default:
		return false
	}
}
