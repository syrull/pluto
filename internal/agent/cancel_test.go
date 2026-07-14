package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

// cancelDuringGenerate cancels the run from inside Generate, standing in for a
// user hitting esc while the model is producing a reply.
type cancelDuringGenerate struct{ cancel context.CancelFunc }

func (cancelDuringGenerate) Name() string { return "cancel-gen" }

func (p cancelDuringGenerate) Generate(ctx context.Context, _ []llm.Message, _ []llm.ToolSpec) (llm.Response, error) {
	p.cancel()
	return llm.Response{}, ctx.Err()
}

func TestRunCanceledDuringGenerateStaysQuiet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := New(cancelDuringGenerate{cancel: cancel}, tool.NewRegistry(), "")

	var events []Event
	_, err := a.Run(ctx, "go", nil, func(e Event) { events = append(events, e) })

	if err == nil {
		t.Fatal("Run should return the cancellation error")
	}
	for _, e := range events {
		if e.Kind == "error" {
			t.Fatalf("cancellation should not emit an error event, got %q", e.Text)
		}
	}
}

// cancelTool cancels the run mid-execution and reports the resulting context
// error, standing in for a long-running tool the user aborts.
type cancelTool struct{ cancel context.CancelFunc }

func (cancelTool) Name() string        { return "slow" }
func (cancelTool) Description() string { return "test tool" }
func (cancelTool) Schema() json.RawMessage {
	return tool.ObjectSchema(map[string]tool.Property{}).MustJSON()
}
func (t cancelTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	t.cancel()
	return "", ctx.Err()
}

func TestRunCanceledMidToolKeepsTranscriptValid(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reg := tool.NewRegistry()
	reg.MustRegister(cancelTool{cancel: cancel})

	p := &scriptedProvider{responses: []llm.Response{{
		ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "slow", Args: json.RawMessage(`{}`)},
			{ID: "c2", Name: "slow", Args: json.RawMessage(`{}`)},
		},
	}}}
	a := New(p, reg, "")

	if _, err := a.Run(ctx, "go", nil, func(Event) {}); err == nil {
		t.Fatal("Run should return the cancellation error")
	}

	// Every requested tool_use must be paired with a tool_result so a later turn
	// re-sending the transcript stays valid.
	results := map[string]bool{}
	for _, m := range a.transcript {
		if m.Role == llm.RoleTool {
			results[m.ToolCallID] = true
		}
	}
	if !results["c1"] || !results["c2"] {
		t.Fatalf("both tool calls should have results after cancellation, got %v", results)
	}
}
