package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderToolCallBashBoxMultiline(t *testing.T) {
	args := `{"command":"echo one\necho two\necho three"}`
	got := renderToolCall(40, "bash", args)
	if !strings.Contains(got, "echo two") || !strings.Contains(got, "echo three") {
		t.Fatalf("multi-line bash box should show the full command, got:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if w := lipgloss.Width(l); w > 40 {
			t.Fatalf("bash box line width = %d, want <= 40:\n%q", w, l)
		}
	}
}

func TestRenderToolCallSingleLineStaysInline(t *testing.T) {
	got := renderToolCall(80, "bash", `{"command":"ls -la"}`)
	if strings.Contains(got, "╭") {
		t.Fatalf("single-line bash command should not be boxed, got:\n%s", got)
	}
}

func TestRenderToolCallWrapsLongCommand(t *testing.T) {
	args := `{"command":"` + strings.Repeat("echo hello world; ", 10) + `"}`
	got := renderToolCall(40, "bash", args)
	for _, l := range strings.Split(got, "\n") {
		if w := lipgloss.Width(l); w > 40 {
			t.Fatalf("renderToolCall line width = %d, want <= 40:\n%q", w, l)
		}
	}
}

func TestRenderToolResultWrapsLongLine(t *testing.T) {
	got := renderToolResult(40, "bash", strings.Repeat("a", 200))
	for _, l := range strings.Split(got, "\n") {
		if w := lipgloss.Width(l); w > 40 {
			t.Fatalf("renderToolResult line width = %d, want <= 40:\n%q", w, l)
		}
	}
}

func TestResultTruncated(t *testing.T) {
	if _, ok := resultTruncated("bash", strings.Repeat("x\n", 20)); !ok {
		t.Fatal("expected truncated for 20 lines")
	}
	if _, ok := resultTruncated("bash", "a\nb"); ok {
		t.Fatal("did not expect truncated for 2 lines")
	}
	if _, ok := resultTruncated("write", strings.Repeat("x\n", 20)); ok {
		t.Fatal("write results should never truncate here")
	}
}
