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
	// Kind is one of: "text", "text_delta", "thinking_delta", "tool_review",
	// "tool_call", "tool_result", "error". The *_delta kinds are incremental
	// streaming chunks; "text" is a complete (non-streamed) reply; "tool_review"
	// is a gate verdict emitted just before a reviewed tool_call.
	Kind string
	// Text carries the payload appropriate to Kind.
	Text string
	// Tool names the tool for tool_review / tool_call / tool_result events.
	Tool string
}

// Gate reviews a proposed tool call before the agent executes it.
type Gate interface {
	Review(ctx context.Context, call llm.ToolCall) ReviewResult
}

// ReviewResult is a Gate's decision about a proposed tool call.
type ReviewResult struct {
	Allowed bool
	// Source names the deciding layer: "fast-path", "guard", "guard-only",
	// "judge", "judge-error", or "off".
	Source string
	Risk   string
	Reason string
}

// AutoController is an optional capability a Gate exposes for runtime control.
type AutoController interface {
	AutoEnabled() bool
	SetAutoEnabled(on bool)
	JudgeName() string
}

// Option configures an Agent at construction.
type Option func(*Agent)

// WithGate installs a review gate consulted before each tool call. A nil gate is ignored.
func WithGate(g Gate) Option {
	return func(a *Agent) {
		if g != nil {
			a.gate = g
		}
	}
}

// Agent owns a provider, a tool registry, and the running transcript.
type Agent struct {
	provider     llm.Provider
	registry     *tool.Registry
	systemPrompt string
	transcript   []llm.Message
	lastUsage    llm.Usage // token accounting from the most recent turn
	gate         Gate      // optional pre-execution review; nil ⇒ allow-all
}

// New constructs an Agent seeded with an optional system prompt.
func New(p llm.Provider, r *tool.Registry, systemPrompt string, opts ...Option) *Agent {
	a := &Agent{provider: p, registry: r, systemPrompt: systemPrompt}
	if systemPrompt != "" {
		a.transcript = append(a.transcript, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Reset discards the running transcript and starts a fresh conversation.
func (a *Agent) Reset() {
	a.transcript = nil
	a.lastUsage = llm.Usage{}
	if a.systemPrompt != "" {
		a.transcript = append(a.transcript, llm.Message{Role: llm.RoleSystem, Content: a.systemPrompt})
	}
}

// ProviderName returns the backend name, for display.
func (a *Agent) ProviderName() string { return a.provider.Name() }

// ContextUsage reports the tokens consumed by the most recent turn and the
// active model's context window. ok is false when the provider cannot report a
// context window.
func (a *Agent) ContextUsage() (used, window int, ok bool) {
	cw, ok := a.provider.(llm.ContextWindower)
	if !ok {
		return 0, 0, false
	}
	return a.lastUsage.InputTokens + a.lastUsage.OutputTokens, cw.ContextWindow(), true
}

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

// Auto exposes the gate's runtime auto-mode control, when it has one.
func (a *Agent) Auto() (AutoController, bool) {
	c, ok := a.gate.(AutoController)
	return c, ok
}

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
			if a.gate != nil {
				rr := a.gate.Review(ctx, call)
				if call.Name == "bash" && rr.Source != "off" {
					emit(Event{Kind: "tool_review", Tool: call.Name, Text: reviewLine(rr)})
				}
				if !rr.Allowed {
					debug.Logf("tool", "%s refused by gate (%s): %s", call.Name, rr.Source, rr.Reason)
					emit(Event{Kind: "tool_call", Tool: call.Name, Text: string(call.Args)})
					result := fmt.Sprintf("refused by auto mode (%s): %s", rr.Source, rr.Reason)
					emit(Event{Kind: "error", Tool: call.Name, Text: result})
					a.transcript = append(a.transcript, llm.Message{
						Role: llm.RoleTool, ToolName: call.Name, ToolCallID: call.ID, Content: result,
					})
					continue
				}
			}

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
	// Record usage on both return paths; skip empty reports so an error turn
	// doesn't clobber the last good count.
	defer func() {
		if resp.Usage.InputTokens > 0 {
			a.lastUsage = resp.Usage
		}
	}()
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

// reviewLine renders a gate verdict as a concise one-line summary for the UI.
func reviewLine(r ReviewResult) string {
	verb := "allowed"
	if !r.Allowed {
		verb = "blocked"
	}
	s := "reviewed — " + verb
	if r.Source != "" {
		s += " (" + r.Source + ")"
	}
	if r.Risk != "" && r.Risk != "none" {
		s += " · risk " + r.Risk
	}
	if !r.Allowed && r.Reason != "" {
		s += ": " + r.Reason
	}
	return s
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return fmt.Sprintf("%s…(+%d)", string(r[:n]), len(r)-n)
}
