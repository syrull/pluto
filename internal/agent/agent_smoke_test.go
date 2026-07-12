package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
	"github.com/syrull/pluto/internal/tools"
)

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	reg := tool.NewRegistry()
	if err := reg.Register(tools.Read{}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(tools.Write{}); err != nil {
		t.Fatal(err)
	}
	return New(llm.Stub{}, reg, "test")
}

func collect(t *testing.T, a *Agent, input string) []Event {
	t.Helper()
	var evs []Event
	if _, err := a.Run(context.Background(), input, func(e Event) { evs = append(evs, e) }); err != nil {
		t.Fatalf("Run(%q): %v", input, err)
	}
	return evs
}

func TestWriteReadRoundTrip(t *testing.T) {
	a := newTestAgent(t)
	path := filepath.Join(t.TempDir(), "hi.txt")

	evs := collect(t, a, "write "+path+" hello world")
	if got := kinds(evs); got != "tool_call,tool_result,text" {
		t.Fatalf("write event kinds = %q, want tool_call,tool_result,text", got)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "hello world" {
		t.Fatalf("file content = %q err=%v, want %q", data, err, "hello world")
	}

	evs = collect(t, a, "read "+path)
	last := evs[len(evs)-1]
	if last.Kind != "text" || !strings.Contains(last.Text, "hello world") {
		t.Fatalf("read reply = %+v, want text containing file content", last)
	}
}

func TestUnknownToolReports(t *testing.T) {
	reg := tool.NewRegistry()
	// Register nothing; stub will still emit a read call.
	a := New(llm.Stub{}, reg, "")
	evs := collect(t, a, "read /nonexistent")
	if !slices.ContainsFunc(evs, func(e Event) bool { return e.Kind == "error" }) {
		t.Fatalf("expected an error event for unknown tool, got %q", kinds(evs))
	}
}

func TestResetClearsTranscriptButKeepsSystemPrompt(t *testing.T) {
	a := newTestAgent(t)
	path := filepath.Join(t.TempDir(), "hi.txt")
	collect(t, a, "write "+path+" hello world")

	if len(a.transcript) == 0 {
		t.Fatal("expected transcript to be populated before reset")
	}

	a.Reset()

	if len(a.transcript) != 1 {
		t.Fatalf("after Reset, transcript = %d messages, want 1 (system prompt only)", len(a.transcript))
	}
	if a.transcript[0].Role != llm.RoleSystem || a.transcript[0].Content != "test" {
		t.Fatalf("after Reset, transcript[0] = %+v, want system prompt", a.transcript[0])
	}
}

func TestResetWithoutSystemPromptLeavesEmptyTranscript(t *testing.T) {
	reg := tool.NewRegistry()
	a := New(llm.Stub{}, reg, "")
	collect(t, a, "read /nonexistent")

	a.Reset()

	if len(a.transcript) != 0 {
		t.Fatalf("after Reset with no system prompt, transcript = %d messages, want 0", len(a.transcript))
	}
}

func kinds(evs []Event) string {
	parts := make([]string, len(evs))
	for i, e := range evs {
		parts[i] = e.Kind
	}
	return strings.Join(parts, ",")
}
