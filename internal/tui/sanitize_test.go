package tui

import (
	"strings"
	"testing"
)

// ansiPayload combines the escape-sequence vectors from the injection report.
const ansiPayload = "\x1b]8;;file:///etc/passwd\x1b\\click\x1b]8;;\x1b\\" +
	"\x1b[2J\x1b[H" +
	"\x1b]0;PWNED\x07" +
	"\x1b[31m" +
	"visible text"

func assertNoESC(t *testing.T, label, out string) {
	t.Helper()
	if strings.ContainsRune(out, 0x1b) {
		t.Fatalf("ESC byte survived %s — control sequences not stripped: %q", label, out)
	}
}

func TestRenderToolResultStripsANSI(t *testing.T) {
	assertNoESC(t, "renderToolResult(bash)", renderToolResult(80, "bash", ansiPayload))
	assertNoESC(t, "renderToolResult(find)", renderToolResult(80, "find", strings.Repeat(ansiPayload+"\n", 3)))
}

func TestRenderToolCallStripsANSI(t *testing.T) {
	args := `{"command":"echo one\n` + "\x1b[2Jecho\x1b]0;X\x07 two" + `"}`
	assertNoESC(t, "renderToolCall bash box", renderToolCall(40, "bash", args))
	assertNoESC(t, "renderToolCall find", renderToolCall(40, "find", `{"pattern":"`+"\x1b[2Jp"+`"}`))
}

func TestWrapBodyStripsANSI(t *testing.T) {
	assertNoESC(t, "wrapBody", wrapBody("← ", ansiPayload, styleToolBody, 80))
}
