package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
	"github.com/syrull/pluto/internal/tools"
)

// bashOnceProvider requests one bash call, then replies with text.
type bashOnceProvider struct {
	command string
	step    int
}

func (*bashOnceProvider) Name() string { return "bash-once" }

func (p *bashOnceProvider) Generate(context.Context, []llm.Message, []llm.ToolSpec) (llm.Response, error) {
	p.step++
	if p.step == 1 {
		args, _ := json.Marshal(map[string]string{"command": p.command, "intent": "x", "why": "y"})
		return llm.Response{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "bash", Args: args}}}, nil
	}
	return llm.Response{Text: "done"}, nil
}

// fixedGate returns the same verdict for every call.
type fixedGate struct{ res ReviewResult }

func (g fixedGate) Review(context.Context, llm.ToolCall) ReviewResult { return g.res }

func bashAgent(t *testing.T, cmd string, gate Gate) (*Agent, *tool.Registry) {
	t.Helper()
	reg := tool.NewRegistry()
	if err := reg.Register(tools.Bash{}); err != nil {
		t.Fatal(err)
	}
	return New(&bashOnceProvider{command: cmd}, reg, "", WithGate(gate)), reg
}

func TestGateBlocksBash(t *testing.T) {
	a, _ := bashAgent(t, "rm -rf /", fixedGate{ReviewResult{Allowed: false, Source: "guard", Reason: "nope"}})

	var evs []Event
	if _, err := a.Run(context.Background(), "go", nil, func(e Event) { evs = append(evs, e) }); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := kinds(evs); got != "tool_review,tool_call,error,text" {
		t.Fatalf("event kinds = %q, want tool_review,tool_call,error,text", got)
	}
	for _, e := range evs {
		if e.Kind == "tool_result" {
			t.Fatal("blocked bash must not produce a tool_result")
		}
	}
	last := a.transcript[len(a.transcript)-2] // tool result precedes final model text
	if last.Role != llm.RoleTool || last.ToolName != "bash" {
		t.Fatalf("expected a RoleTool refusal on the transcript, got %+v", last)
	}
}

func TestGateAllowsBash(t *testing.T) {
	a, _ := bashAgent(t, "echo hi", fixedGate{ReviewResult{Allowed: true, Source: "fast-path"}})

	var evs []Event
	if _, err := a.Run(context.Background(), "go", nil, func(e Event) { evs = append(evs, e) }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := kinds(evs); got != "tool_review,tool_call,tool_result,text" {
		t.Fatalf("event kinds = %q, want tool_review,tool_call,tool_result,text", got)
	}
	for _, e := range evs {
		if e.Kind == "tool_result" && e.Text != "hi" {
			t.Fatalf("bash result = %q, want %q", e.Text, "hi")
		}
	}
}

func TestNoGateNoReview(t *testing.T) {
	a, _ := bashAgent(t, "echo hi", nil)
	var evs []Event
	if _, err := a.Run(context.Background(), "go", nil, func(e Event) { evs = append(evs, e) }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := kinds(evs); got != "tool_call,tool_result,text" {
		t.Fatalf("event kinds = %q, want tool_call,tool_result,text (no review)", got)
	}
}
