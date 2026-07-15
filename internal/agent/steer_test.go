package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

// scriptedProvider returns a fixed sequence of responses and invokes a hook at
// the start of each turn, so a test can enqueue steering at a precise boundary.
type scriptedProvider struct {
	responses []llm.Response
	calls     int
	onCall    func(turn int)
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) Generate(context.Context, []llm.Message, []llm.ToolSpec) (llm.Response, error) {
	turn := p.calls
	p.calls++
	if p.onCall != nil {
		p.onCall(turn)
	}
	if turn < len(p.responses) {
		return p.responses[turn], nil
	}
	return llm.Response{Text: "done"}, nil
}

func roles(msgs []llm.Message) []llm.Role {
	rs := make([]llm.Role, len(msgs))
	for i, m := range msgs {
		rs[i] = m.Role
	}
	return rs
}

func findUser(msgs []llm.Message, content string) int {
	for i, m := range msgs {
		if m.Role == llm.RoleUser && m.Content == content {
			return i
		}
	}
	return -1
}

func TestSteerInjectedAfterToolResult(t *testing.T) {
	p := &scriptedProvider{
		responses: []llm.Response{
			{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Args: json.RawMessage(`{}`)}}},
			{Text: "acknowledged"},
		},
	}
	a := New(p, tool.NewRegistry(), "sys")
	// Enqueue steering during the first turn; it must land after that turn's
	// tool result and before the next generation.
	p.onCall = func(turn int) {
		if turn == 0 {
			a.Steer("actually, stop")
		}
	}

	if _, err := a.Run(context.Background(), "go", nil, func(Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	idx := findUser(a.transcript, "actually, stop")
	if idx < 0 {
		t.Fatalf("steering message not injected; roles=%v", roles(a.transcript))
	}
	if prev := a.transcript[idx-1].Role; prev != llm.RoleTool {
		t.Fatalf("steering injected after %v, want it to follow the tool result", prev)
	}
	if next := a.transcript[idx+1].Role; next != llm.RoleModel {
		t.Fatalf("message after steering = %v, want the model's follow-up turn", next)
	}
}

func TestSteerContinuesAfterFinalReply(t *testing.T) {
	p := &scriptedProvider{
		responses: []llm.Response{
			{Text: "first"},
			{Text: "second"},
		},
	}
	a := New(p, tool.NewRegistry(), "")
	// Steer while the first (final-looking) reply is being produced: the turn
	// should continue instead of returning.
	p.onCall = func(turn int) {
		if turn == 0 {
			a.Steer("keep going")
		}
	}

	out, err := a.Run(context.Background(), "start", nil, func(Event) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "second" {
		t.Fatalf("final reply = %q, want %q (steering should continue the turn)", out, "second")
	}

	idx := findUser(a.transcript, "keep going")
	if idx < 0 {
		t.Fatalf("steering message not injected; roles=%v", roles(a.transcript))
	}
	if prev := a.transcript[idx-1].Role; prev != llm.RoleModel {
		t.Fatalf("steering injected after %v, want it to follow the first reply", prev)
	}
}

func TestTakeSteeringDrains(t *testing.T) {
	a := New(llm.Stub{}, tool.NewRegistry(), "")
	a.Steer("one")
	a.Steer("two")
	got := a.TakeSteering()
	if len(got) != 2 || got[0].Text != "one" || got[1].Text != "two" {
		t.Fatalf("TakeSteering = %v, want [one two]", got)
	}
	if a.hasSteering() {
		t.Fatalf("queue should be empty after TakeSteering")
	}
}
