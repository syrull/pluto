package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

// fakeGenProvider is a non-streaming provider returning a fixed response.
type fakeGenProvider struct{ resp llm.Response }

func (fakeGenProvider) Name() string { return "fake-gen" }
func (p fakeGenProvider) Generate(context.Context, []llm.Message, []llm.ToolSpec) (llm.Response, error) {
	return p.resp, nil
}

// fakeStreamProvider replays a fixed set of deltas, then returns a response.
type fakeStreamProvider struct {
	deltas []llm.StreamDelta
	resp   llm.Response
}

func (fakeStreamProvider) Name() string { return "fake-stream" }
func (p fakeStreamProvider) Generate(context.Context, []llm.Message, []llm.ToolSpec) (llm.Response, error) {
	return p.resp, nil
}
func (p fakeStreamProvider) GenerateStream(_ context.Context, _ []llm.Message, _ []llm.ToolSpec, onDelta func(llm.StreamDelta)) (llm.Response, error) {
	for _, d := range p.deltas {
		onDelta(d)
	}
	return p.resp, nil
}

func TestServerToolUseNonStreamingSurfacesEvents(t *testing.T) {
	p := fakeGenProvider{resp: llm.Response{
		Text: "answer",
		ServerToolUses: []llm.ServerToolUse{
			{Name: "web_search", Args: `{"query":"go"}`, Result: "Go (https://go.dev)"},
		},
	}}
	a := New(p, tool.NewRegistry(), "")

	evs := collect(t, a, "hi")
	if got := kinds(evs); got != "tool_call,tool_result,text" {
		t.Fatalf("kinds = %q, want tool_call,tool_result,text", got)
	}
	if evs[0].Tool != "web_search" || !strings.Contains(evs[0].Text, "go") {
		t.Fatalf("tool_call event = %+v, want web_search with query", evs[0])
	}
	if evs[1].Tool != "web_search" || !strings.Contains(evs[1].Text, "go.dev") {
		t.Fatalf("tool_result event = %+v, want web_search with result", evs[1])
	}
}

func TestServerToolUseStreamingSurfacesEventsInOrder(t *testing.T) {
	p := fakeStreamProvider{
		deltas: []llm.StreamDelta{
			{Kind: llm.DeltaServerToolCall, Tool: "web_search", Text: `{"query":"go"}`},
			{Kind: llm.DeltaServerToolResult, Tool: "web_search", Text: "Go (https://go.dev)"},
			{Kind: llm.DeltaText, Text: "answer"},
		},
		resp: llm.Response{Text: "answer"},
	}
	a := New(p, tool.NewRegistry(), "")

	evs := collect(t, a, "hi")
	if got := kinds(evs); got != "tool_call,tool_result,text_delta" {
		t.Fatalf("kinds = %q, want tool_call,tool_result,text_delta", got)
	}
	if evs[0].Tool != "web_search" || evs[1].Tool != "web_search" {
		t.Fatalf("web search events missing tool name: %+v", evs)
	}
}
