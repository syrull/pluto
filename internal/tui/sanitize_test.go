package tui

import (
	"regexp"
	"strings"
	"testing"
)

// ansiPayload combines the escape-sequence vectors from the injection report.
const ansiPayload = "\x1b]8;;file:///etc/passwd\x1b\\click\x1b]8;;\x1b\\" +
	"\x1b[2J\x1b[H" +
	"\x1b]0;PWNED\x07" +
	"\x1b[31m" +
	"visible text"

// sgrRe matches the SGR color/style sequences lipgloss legitimately emits when
// rendering. Stripping these leaves only bytes that came from the (untrusted)
// content, which Sanitize must have neutralized.
var sgrRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// assertSanitized checks that, once lipgloss's own SGR styling is removed, no
// control sequences from untrusted content survive in out.
func assertSanitized(t *testing.T, label, out string) {
	t.Helper()
	for _, r := range sgrRe.ReplaceAllString(out, "") {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			t.Fatalf("control byte %#x survived %s — content not sanitized: %q", r, label, out)
		}
	}
}

func TestRenderToolResultStripsANSI(t *testing.T) {
	assertSanitized(t, "renderToolResult(bash)", renderToolResult(80, "bash", ansiPayload))
	assertSanitized(t, "renderToolResult(find)", renderToolResult(80, "find", strings.Repeat(ansiPayload+"\n", 3)))
}

func TestRenderToolCallStripsANSI(t *testing.T) {
	args := `{"command":"echo one\n` + "\x1b[2Jecho\x1b]0;X\x07 two" + `"}`
	assertSanitized(t, "renderToolCall bash box", renderToolCall(40, "bash", args))
	assertSanitized(t, "renderToolCall find", renderToolCall(40, "find", `{"pattern":"`+"\x1b[2Jp"+`"}`))
}

func TestWrapBodyStripsANSI(t *testing.T) {
	assertSanitized(t, "wrapBody", wrapBody("← ", ansiPayload, styleToolBody, 80))
}
