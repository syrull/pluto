package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

// ctxWindowProvider is a stub that also reports a context window, so
// ContextUsage returns a usable ratio.
type ctxWindowProvider struct{ llm.Stub }

func (ctxWindowProvider) ContextWindow() int { return 1000 }

func TestLoadSeedsContextUsage(t *testing.T) {
	a := New(ctxWindowProvider{}, tool.NewRegistry(), "sys")
	a.Load([]llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("hello ", 100)},
		{Role: llm.RoleModel, Content: strings.Repeat("world ", 100)},
	})
	used, window, ok := a.ContextUsage()
	if !ok || window != 1000 {
		t.Fatalf("ContextUsage ok=%v window=%d, want ok with window 1000", ok, window)
	}
	if used <= 0 {
		t.Fatalf("a resumed conversation should report nonzero context usage, got %d", used)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	a := New(llm.Stub{}, tool.NewRegistry(), "sys")
	a.transcript = append(a.transcript, llm.Message{Role: llm.RoleUser, Content: "hi"})

	snap := a.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Snapshot len = %d, want 2", len(snap))
	}
	snap[0].Content = "mutated"
	if a.transcript[0].Content == "mutated" {
		t.Fatal("Snapshot aliases the agent transcript; mutating it changed the agent")
	}
}

func TestLoadReinstatesSystemPromptAndDropsSaved(t *testing.T) {
	a := New(llm.Stub{}, tool.NewRegistry(), "fresh system prompt")

	saved := []llm.Message{
		{Role: llm.RoleSystem, Content: "stale saved prompt"},
		{Role: llm.RoleUser, Content: "read the file"},
		{
			Role:      llm.RoleModel,
			Content:   "on it",
			Thinking:  "reasoning",
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Args: json.RawMessage(`{"path":"x"}`)}},
		},
		{Role: llm.RoleTool, ToolName: "read", ToolCallID: "c1", Content: "data"},
	}
	a.Load(saved)

	got := a.Snapshot()
	if got[0].Role != llm.RoleSystem || got[0].Content != "fresh system prompt" {
		t.Fatalf("Load should reinstate the current system prompt, got %+v", got[0])
	}
	// Exactly one system message: the reinstated one, not the saved one.
	systems := 0
	for _, m := range got {
		if m.Role == llm.RoleSystem {
			systems++
		}
	}
	if systems != 1 {
		t.Fatalf("system message count = %d, want 1", systems)
	}
	if len(got) != 4 { // fresh system + user + model + tool
		t.Fatalf("loaded transcript len = %d, want 4", len(got))
	}
	if got[2].Thinking != "reasoning" || len(got[2].ToolCalls) != 1 {
		t.Fatalf("thinking/tool calls not preserved on load: %+v", got[2])
	}
}

func TestLoadRoundTripsThroughSnapshot(t *testing.T) {
	a := New(llm.Stub{}, tool.NewRegistry(), "")
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "q"},
		{Role: llm.RoleModel, Content: "a"},
	}
	a.Load(msgs)
	got := a.Snapshot()
	if len(got) != 2 || got[0].Content != "q" || got[1].Content != "a" {
		t.Fatalf("round-trip without system prompt = %+v, want [q a]", got)
	}
}
