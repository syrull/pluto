// Package agent drives the think→act→observe loop.
package agent

import (
	"context"
	"fmt"

	"github.com/pluto/harness/internal/debug"
	"github.com/pluto/harness/internal/llm"
	"github.com/pluto/harness/internal/tool"
)

const maxSteps = 1000

// Event is an observable step emitted during Run, for UIs to render progress.
type Event struct {
	// Kind is one of: "text", "text_delta", "thinking_delta", "tool_call",
	// "tool_result", "error". The *_delta kinds are incremental streaming
	// chunks; "text" is a complete (non-streamed) reply.
	Kind string
	// Text carries the payload appropriate to Kind.
	Text string
	// Tool names the tool for tool_call / tool_result events.
	Tool string
}

// Agent owns a provider, a tool registry, and the running transcript.
type Agent struct {
	provider     llm.Provider
	registry     *tool.Registry
	systemPrompt string
	transcript   []llm.Message
}

// New constructs an Agent seeded with an optional system prompt.
func New(p llm.Provider, r *tool.Registry, systemPrompt string) *Agent {
	a := &Agent{provider: p, registry: r, systemPrompt: systemPrompt}
	if systemPrompt != "" {
		a.transcript = append(a.transcript, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
	}
	return a
}

// Reset discards the running transcript and starts a fresh conversation.
func (a *Agent) Reset() {
	a.transcript = nil
	if a.systemPrompt != "" {
		a.transcript = append(a.transcript, llm.Message{Role: llm.RoleSystem, Content: a.systemPrompt})
	}
}

// ProviderName returns the backend name, for display.
func (a *Agent) ProviderName() string { return a.provider.Name() }

// Switcher exposes the provider's runtime model-switching capability.
func (a *Agent) Switcher() (llm.Switchable, bool) {
	s, ok := a.provider.(llm.Switchable)
	return s, ok
}

// Thinker exposes the provider's runtime thinking-toggle capability.
func (a *Agent) Thinker() (llm.Thinkable, bool) {
	t, ok := a.provider.(llm.Thinkable)
	return t, ok
}

// SetProvider swaps the active provider.
func (a *Agent) SetProvider(p llm.Provider) { a.provider = p }

func (a *Agent) specs() []llm.ToolSpec {
	tools := a.registry.Tools()
	specs := make([]llm.ToolSpec, len(tools))
	for i, t := range tools {
		specs[i] = llm.ToolSpec{Name: t.Name(), Description: t.Description(), Schema: t.Schema()}
	}
	return specs
}

// Run processes one user input to completion.
func (a *Agent) Run(ctx context.Context, input string, emit func(Event)) (string, error) {
	debug.Logf("agent", "run input=%q provider=%s", truncate(input, 256), a.provider.Name())
	a.transcript = append(a.transcript, llm.Message{Role: llm.RoleUser, Content: input})

	for step := range maxSteps {
		debug.Logf("agent", "step %d/%d", step+1, maxSteps)
		if err := ctx.Err(); err != nil {
			return "", err
		}

		resp, streamed, err := a.generate(ctx, emit)
		if err != nil {
			debug.Logf("agent", "generate error: %v", err)
			emit(Event{Kind: "error", Text: err.Error()})
			return "", err
		}

		// Plain text reply: record and finish. When streamed, the text already
		// reached the UI via deltas, so only emit a final "text" event on the
		// non-streaming path.
		if len(resp.ToolCalls) == 0 {
			debug.Logf("agent", "final reply (%d chars, streamed=%t)", len(resp.Text), streamed)
			a.transcript = append(a.transcript, llm.Message{
				Role: llm.RoleModel, Content: resp.Text,
				Thinking: resp.Thinking, ThinkingSig: resp.ThinkingSig,
			})
			if !streamed {
				emit(Event{Kind: "text", Text: resp.Text})
			}
			return resp.Text, nil
		}

		// Record the assistant tool-use turn verbatim (thinking first, then any
		// leading text) so the provider can replay it on the next request.
		debug.Logf("agent", "model requested %d tool call(s)", len(resp.ToolCalls))
		a.transcript = append(a.transcript, llm.Message{
			Role: llm.RoleModel, Content: resp.Text, ToolCalls: resp.ToolCalls,
			Thinking: resp.Thinking, ThinkingSig: resp.ThinkingSig,
		})
		if resp.Text != "" && !streamed {
			emit(Event{Kind: "text", Text: resp.Text})
		}

		// Execute every requested call and feed each result back, correlated
		// by call ID. Calls in one turn are independent (may run concurrently);
		// here we run them sequentially for simplicity.
		for _, call := range resp.ToolCalls {
			debug.Logf("tool", "invoke %s args=%s", call.Name, truncate(string(call.Args), 512))
			emit(Event{Kind: "tool_call", Tool: call.Name, Text: string(call.Args)})

			result, err := a.registry.Invoke(ctx, call.Name, call.Args)
			if err != nil {
				debug.Logf("tool", "%s failed: %v", call.Name, err)
				result = "error: " + err.Error()
				emit(Event{Kind: "error", Tool: call.Name, Text: err.Error()})
			} else {
				debug.Logf("tool", "%s ok result=%s", call.Name, truncate(result, 512))
				emit(Event{Kind: "tool_result", Tool: call.Name, Text: result})
			}
			a.transcript = append(a.transcript, llm.Message{
				Role: llm.RoleTool, ToolName: call.Name, ToolCallID: call.ID, Content: result,
			})
		}
	}

	debug.Logf("agent", "step budget exhausted after %d steps", maxSteps)
	err := fmt.Errorf("agent: exceeded %d steps without completing", maxSteps)
	emit(Event{Kind: "error", Text: err.Error()})
	return "", err
}

func (a *Agent) generate(ctx context.Context, emit func(Event)) (resp llm.Response, streamed bool, err error) {
	if sp, ok := a.provider.(llm.StreamingProvider); ok {
		debug.Logf("llm", "GenerateStream: %d msg(s), %d tool spec(s)", len(a.transcript), len(a.specs()))
		resp, err = sp.GenerateStream(ctx, a.transcript, a.specs(), func(d llm.StreamDelta) {
			switch d.Kind {
			case llm.DeltaText:
				emit(Event{Kind: "text_delta", Text: d.Text})
			case llm.DeltaThinking:
				emit(Event{Kind: "thinking_delta", Text: d.Text})
			}
		})
		debug.Logf("llm", "GenerateStream done: text=%d chars, tools=%d, err=%v", len(resp.Text), len(resp.ToolCalls), err)
		return resp, true, err
	}
	debug.Logf("llm", "Generate: %d msg(s), %d tool spec(s)", len(a.transcript), len(a.specs()))
	resp, err = a.provider.Generate(ctx, a.transcript, a.specs())
	debug.Logf("llm", "Generate done: text=%d chars, tools=%d, err=%v", len(resp.Text), len(resp.ToolCalls), err)
	return resp, false, err
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return fmt.Sprintf("%s…(+%d)", string(r[:n]), len(r)-n)
}
