package debug

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setup points the logger at a temp file with the given env and returns a reader
// for the captured output. It resets any prior state first.
func setup(t *testing.T, env map[string]string) func() string {
	t.Helper()
	_ = Close()
	path := filepath.Join(t.TempDir(), "pluto-debug.log")
	t.Setenv("PLUTO_DEBUG", "1")
	t.Setenv("PLUTO_DEBUG_FILE", path)
	t.Setenv("PLUTO_DEBUG_LEVEL", "")
	t.Setenv("PLUTO_DEBUG_COMPONENTS", "")
	t.Setenv("PLUTO_DEBUG_FRAMES", "")
	for k, v := range env {
		t.Setenv(k, v)
	}
	if _, err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = Close() })
	return func() string {
		_ = Close()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return string(data)
	}
}

func TestDisabledIsNoOp(t *testing.T) {
	_ = Close()
	t.Setenv("PLUTO_DEBUG", "")
	if _, err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if Enabled() {
		t.Fatal("Enabled() = true with PLUTO_DEBUG unset")
	}
	// These must not panic and must write nothing.
	Info("agent", "hello", "k", "v")
	Debug("tui", "frame")
	NewTimer("tool", "exec").Stop()
	Frame("tui", "fp", "body")
	if Should("agent", LevelError) {
		t.Fatal("Should() = true while disabled")
	}
}

func TestLevelFiltering(t *testing.T) {
	read := setup(t, map[string]string{"PLUTO_DEBUG_LEVEL": "warn"})
	Debug("agent", "debug-line")
	Info("agent", "info-line")
	Warn("agent", "warn-line")
	Error("agent", "error-line")
	out := read()
	if strings.Contains(out, "debug-line") || strings.Contains(out, "info-line") {
		t.Errorf("below-threshold lines were logged:\n%s", out)
	}
	if !strings.Contains(out, "warn-line") || !strings.Contains(out, "error-line") {
		t.Errorf("at/above-threshold lines missing:\n%s", out)
	}
}

func TestComponentIncludeFilter(t *testing.T) {
	read := setup(t, map[string]string{"PLUTO_DEBUG_COMPONENTS": "tui,agent"})
	Info("tui", "tui-line")
	Info("agent", "agent-line")
	Info("llm", "llm-line")
	out := read()
	if !strings.Contains(out, "tui-line") || !strings.Contains(out, "agent-line") {
		t.Errorf("included components missing:\n%s", out)
	}
	if strings.Contains(out, "llm-line") {
		t.Errorf("excluded component leaked:\n%s", out)
	}
}

func TestComponentExcludeFilter(t *testing.T) {
	read := setup(t, map[string]string{"PLUTO_DEBUG_COMPONENTS": "-llm"})
	Info("tui", "tui-line")
	Info("llm", "llm-line")
	out := read()
	if !strings.Contains(out, "tui-line") {
		t.Errorf("non-excluded component missing:\n%s", out)
	}
	if strings.Contains(out, "llm-line") {
		t.Errorf("excluded component leaked:\n%s", out)
	}
}

func TestStructuredFields(t *testing.T) {
	read := setup(t, nil)
	Info("tui", "frame update", "trigger", "KeyMsg", "key", "tab", "w", 120, "h", 40)
	out := read()
	if !strings.Contains(out, "INFO  [tui] frame update trigger=KeyMsg key=tab w=120 h=40") {
		t.Errorf("structured line not rendered as expected:\n%s", out)
	}
}

func TestValueQuoting(t *testing.T) {
	read := setup(t, nil)
	Info("agent", "msg", "text", "hello world", "empty", "")
	out := read()
	if !strings.Contains(out, `text="hello world"`) {
		t.Errorf("value with space not quoted:\n%s", out)
	}
	if !strings.Contains(out, `empty=""`) {
		t.Errorf("empty value not rendered:\n%s", out)
	}
}

func TestRedactNeverLeaksSecret(t *testing.T) {
	read := setup(t, nil)
	const secret = "sk-ant-oat01-SUPERSECRETTOKEN-abcdef0123456789"
	Info("auth", "login success", "token", Redact(secret), "scopes", []string{"a", "b"})
	out := read()
	if strings.Contains(out, secret) {
		t.Fatalf("secret leaked into log:\n%s", out)
	}
	if strings.Contains(out, "SUPERSECRET") {
		t.Fatalf("secret substring leaked into log:\n%s", out)
	}
	if !strings.Contains(out, "<redacted") {
		t.Errorf("redaction marker missing:\n%s", out)
	}
}

func TestRedactEmpty(t *testing.T) {
	if got := Redact(""); got != "<empty>" {
		t.Errorf("Redact(\"\") = %q, want <empty>", got)
	}
}

func TestFrameCoalescing(t *testing.T) {
	read := setup(t, map[string]string{"PLUTO_DEBUG_LEVEL": "trace", "PLUTO_DEBUG_FRAMES": "coalesced"})
	Frame("tui", "A", "")
	Frame("tui", "A", "")
	Frame("tui", "A", "")
	Frame("tui", "B", "")
	out := read()
	if !strings.Contains(out, "frame unchanged repeated=2") {
		t.Errorf("identical frames not coalesced:\n%s", out)
	}
	renders := strings.Count(out, "frame render")
	if renders != 2 {
		t.Errorf("frame render count = %d, want 2:\n%s", renders, out)
	}
}

func TestFramesRequireTrace(t *testing.T) {
	// Default level is DEBUG, so TRACE frames should be dropped.
	read := setup(t, nil)
	if FramesEnabled("tui") {
		t.Fatal("FramesEnabled() = true at DEBUG level")
	}
	Frame("tui", "A", "")
	out := read()
	if strings.Contains(out, "frame render") {
		t.Errorf("frame logged at DEBUG level:\n%s", out)
	}
}

func TestFramesFullDumpsBody(t *testing.T) {
	read := setup(t, map[string]string{"PLUTO_DEBUG_LEVEL": "trace", "PLUTO_DEBUG_FRAMES": "full"})
	Frame("tui", "A", "RENDERED-BODY-CONTENT")
	out := read()
	if !strings.Contains(out, "RENDERED-BODY-CONTENT") {
		t.Errorf("full frame body not dumped:\n%s", out)
	}
}

func TestTimerLogsDuration(t *testing.T) {
	read := setup(t, nil)
	NewTimer("tool", "exec").Stop("tool", "bash")
	out := read()
	if !strings.Contains(out, "[tool] exec") || !strings.Contains(out, "dur=") {
		t.Errorf("timer did not log a duration:\n%s", out)
	}
}
