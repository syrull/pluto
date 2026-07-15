package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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

func TestRenderBashCallBoxSpansFullWidth(t *testing.T) {
	box, ok := renderBashCallBox(60, `{"command":"echo one\necho two"}`)
	if !ok {
		t.Fatal("multi-line bash command should produce a box")
	}
	if got := lipgloss.Width(box); got != 60 {
		t.Fatalf("bash box width = %d, want 60 (full pane interior)", got)
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

func TestRenderToolCallWebSearchShowsQuery(t *testing.T) {
	got := renderToolCall(80, "web_search", `{"query":"golang release notes"}`)
	if !strings.Contains(got, "web_search") || !strings.Contains(got, "golang release notes") {
		t.Fatalf("web_search call should show the query, got:\n%s", got)
	}
}

func TestFormatWebSearchArgs(t *testing.T) {
	if got := formatWebSearchArgs(`{"query":"golang"}`); got != "golang" {
		t.Fatalf("formatWebSearchArgs(query) = %q, want %q", got, "golang")
	}
	// A well-formed call with no query renders empty (→ web_search()), not "{}".
	if got := formatWebSearchArgs(`{}`); got != "" {
		t.Fatalf("formatWebSearchArgs({}) = %q, want empty", got)
	}
	// Malformed JSON falls back to the raw args for debugging.
	if got := formatWebSearchArgs(`not json`); got != "not json" {
		t.Fatalf("formatWebSearchArgs(malformed) = %q, want the raw args", got)
	}
}

func TestRenderToolResultWebSearchSummarizesResults(t *testing.T) {
	results := "Go (https://go.dev)\nGolang blog (https://go.dev/blog)\nWikipedia (https://en.wikipedia.org/wiki/Go)"
	got := renderToolResult(80, "web_search", results)
	if !strings.Contains(got, "3 result(s)") {
		t.Fatalf("web_search result should summarize the count, got:\n%s", got)
	}
	if !strings.Contains(got, "go.dev") {
		t.Fatalf("web_search result should preview results, got:\n%s", got)
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
