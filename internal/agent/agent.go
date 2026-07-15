// Package agent drives the think→act→observe loop.
package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

const maxSteps = 1000

const (
	// defaultContextBudget caps the transcript sent per turn when the provider
	// can't report a context window, and also caps the derived budget on
	// large-window models so long sessions don't re-send an unbounded history.
	defaultContextBudget = 200_000
	// contextBudgetFraction trims once the transcript would exceed this share of
	// the model's context window.
	contextBudgetFraction = 0.8
)

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

// WithContextLimit caps the approximate token size of the transcript re-sent on
// each turn; older exchanges are dropped once it is exceeded. A non-positive
// value falls back to the window-derived default.
func WithContextLimit(tokens int) Option {
	return func(a *Agent) {
		if tokens > 0 {
			a.contextLimit = tokens
		}
	}
}

// Agent owns a provider, a tool registry, and the running transcript.
type Agent struct {
	provider     llm.Provider
	registry     *tool.Registry
	systemPrompt string

	// mu guards transcript and lastUsage. A single Agent runs one turn at a time,
	// but Snapshot/ContextUsage can be called from the UI goroutine (e.g. autosave
	// snapshotting every workspace) while another agent's Run appends to its own
	// transcript, so those accesses must be synchronized. mu is never held across
	// network I/O — only around the transcript/usage reads and writes themselves.
	mu           sync.RWMutex
	transcript   []llm.Message
	lastUsage    llm.Usage // token accounting from the most recent turn
	gate         Gate      // optional pre-execution review; nil ⇒ allow-all
	contextLimit int       // token budget for the re-sent transcript; 0 ⇒ derive from window

	steerMu sync.Mutex     // guards steer
	steer   []SteerMessage // user messages queued to fold into a running turn
}

// SteerMessage is a user turn queued to fold into a running turn: its text plus
// any attachments (e.g. images) that should ride along with it.
type SteerMessage struct {
	Text        string
	Attachments []llm.Attachment
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
	a.TakeSteering()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.transcript = nil
	a.lastUsage = llm.Usage{}
	if a.systemPrompt != "" {
		a.transcript = append(a.transcript, llm.Message{Role: llm.RoleSystem, Content: a.systemPrompt})
	}
}

// Snapshot returns a copy of the running transcript, safe for the caller to
// persist or inspect without racing the agent's own mutations.
func (a *Agent) Snapshot() []llm.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]llm.Message, len(a.transcript))
	copy(out, a.transcript)
	return out
}

// Load replaces the transcript with a previously saved one so the conversation
// can be resumed. Saved system messages are dropped and the agent's current
// system prompt is reinstated (so the fresh tool listing and project context
// ride along), then the transcript is trimmed to the context budget so a large
// restored history is bounded on the next turn.
func (a *Agent) Load(msgs []llm.Message) {
	a.TakeSteering()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.transcript = nil
	if a.systemPrompt != "" {
		a.transcript = append(a.transcript, llm.Message{Role: llm.RoleSystem, Content: a.systemPrompt})
	}
	for _, m := range msgs {
		if m.Role == llm.RoleSystem {
			continue
		}
		a.transcript = append(a.transcript, m)
	}
	a.trimTranscriptLocked()
	// Seed usage from the restored transcript so the context indicator reflects a
	// resumed conversation before the next turn reports real token counts.
	a.lastUsage = llm.Usage{InputTokens: estimateTokens(a.transcript)}
}

// Steer queues a user message (with optional attachments) to fold into a running
// turn at the next step boundary, letting the user redirect the agent mid-task.
// It is safe to call concurrently with Run; a message sent while Run is idle is
// picked up by the next Run. The UI drains any leftover with TakeSteering once a
// Run ends.
func (a *Agent) Steer(input string, attachments ...llm.Attachment) {
	a.steerMu.Lock()
	a.steer = append(a.steer, SteerMessage{Text: input, Attachments: attachments})
	a.steerMu.Unlock()
}

// TakeSteering removes and returns the queued steering messages, if any.
func (a *Agent) TakeSteering() []SteerMessage {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	q := a.steer
	a.steer = nil
	return q
}

// hasSteering reports whether any steering messages are queued.
func (a *Agent) hasSteering() bool {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	return len(a.steer) > 0
}

// injectSteering appends any queued steering messages as user turns and re-trims
// the transcript. It reports whether anything was injected.
func (a *Agent) injectSteering() bool {
	msgs := a.TakeSteering()
	if len(msgs) == 0 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, msg := range msgs {
		debug.Logf("agent", "steer input=%q attachments=%d", truncate(msg.Text, 256), len(msg.Attachments))
		a.transcript = append(a.transcript, llm.Message{
			Role: llm.RoleUser, Content: msg.Text, Attachments: msg.Attachments,
		})
	}
	a.trimTranscriptLocked()
	return true
}

// AddContext folds out-of-band text into the conversation as a user turn
// without triggering a generation, so the next turn sees it — e.g. the output
// of an inline shell command the user ran. It mutates the transcript directly
// and so must only be called while no Run is in flight; use Steer to inject
// into a running turn.
func (a *Agent) AddContext(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.transcript = append(a.transcript, llm.Message{Role: llm.RoleUser, Content: text})
	a.trimTranscriptLocked()
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
	a.mu.RLock()
	used = a.lastUsage.InputTokens + a.lastUsage.OutputTokens
	a.mu.RUnlock()
	return used, cw.ContextWindow(), true
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

// Run processes one user input (with optional attachments) to completion.
func (a *Agent) Run(ctx context.Context, input string, attachments []llm.Attachment, emit func(Event)) (string, error) {
	debug.Logf("agent", "run input=%q attachments=%d provider=%s", truncate(input, 256), len(attachments), a.provider.Name())
	a.appendMessages(llm.Message{Role: llm.RoleUser, Content: input, Attachments: attachments})
	a.trimTranscript()

	for step := range maxSteps {
		debug.Logf("agent", "step %d/%d", step+1, maxSteps)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Fold in any user messages queued since the previous step so the model
		// sees them before generating its next turn.
		a.injectSteering()

		resp, streamed, err := a.generate(ctx, emit)
		if err != nil {
			debug.Logf("agent", "generate error: %v", err)
			// A canceled run unwinds quietly: the UI already knows and a spurious
			// error line would only add noise.
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			emit(Event{Kind: "error", Text: err.Error()})
			return "", err
		}

		// Plain text reply: record and finish. When streamed, the text already
		// reached the UI via deltas, so only emit a final "text" event on the
		// non-streaming path.
		if len(resp.ToolCalls) == 0 {
			debug.Logf("agent", "final reply (%d chars, streamed=%t)", len(resp.Text), streamed)
			a.appendMessages(llm.Message{
				Role: llm.RoleModel, Content: resp.Text,
				Thinking: resp.Thinking, ThinkingSig: resp.ThinkingSig,
			})
			if !streamed {
				emit(Event{Kind: "text", Text: resp.Text})
			}
			// A message steered in while the reply was finishing keeps the
			// conversation going instead of ending the turn.
			if a.hasSteering() {
				continue
			}
			return resp.Text, nil
		}

		// Record the assistant tool-use turn verbatim (thinking first, then any
		// leading text) so the provider can replay it on the next request.
		debug.Logf("agent", "model requested %d tool call(s)", len(resp.ToolCalls))
		a.appendMessages(llm.Message{
			Role: llm.RoleModel, Content: resp.Text, ToolCalls: resp.ToolCalls,
			Thinking: resp.Thinking, ThinkingSig: resp.ThinkingSig,
		})
		if resp.Text != "" && !streamed {
			emit(Event{Kind: "text", Text: resp.Text})
		}

		// Execute every requested call and feed each result back, correlated
		// by call ID. Calls in one turn are independent (may run concurrently);
		// here we run them sequentially for simplicity.
		for i, call := range resp.ToolCalls {
			// A run canceled between calls stops here; record a synthetic result
			// for this and every remaining call so each tool_use stays paired with
			// a tool_result and the transcript remains valid for a later turn.
			if ctx.Err() != nil {
				a.recordCanceled(resp.ToolCalls[i:])
				return "", ctx.Err()
			}
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
					a.appendMessages(llm.Message{
						Role: llm.RoleTool, ToolName: call.Name, ToolCallID: call.ID, Content: result,
					})
					continue
				}
			}

			debug.Logf("tool", "invoke %s args=%s", call.Name, truncate(string(call.Args), 512))
			emit(Event{Kind: "tool_call", Tool: call.Name, Text: string(call.Args)})

			result, err := a.registry.Invoke(ctx, call.Name, call.Args)
			if err != nil && ctx.Err() != nil {
				// Canceled mid-tool: record canceled results for this and every
				// remaining call, then unwind quietly.
				a.recordCanceled(resp.ToolCalls[i:])
				return "", ctx.Err()
			}
			if err != nil {
				debug.Logf("tool", "%s failed: %v", call.Name, err)
				result = "error: " + err.Error()
				emit(Event{Kind: "error", Tool: call.Name, Text: err.Error()})
			} else {
				debug.Logf("tool", "%s ok result=%s", call.Name, truncate(result, 512))
				emit(Event{Kind: "tool_result", Tool: call.Name, Text: result})
			}
			a.appendMessages(llm.Message{
				Role: llm.RoleTool, ToolName: call.Name, ToolCallID: call.ID, Content: result,
			})
		}
	}

	debug.Logf("agent", "step budget exhausted after %d steps", maxSteps)
	err := fmt.Errorf("agent: exceeded %d steps without completing", maxSteps)
	emit(Event{Kind: "error", Text: err.Error()})
	return "", err
}

// recordCanceled appends a synthetic "canceled" tool_result for each call so a
// run aborted mid-turn leaves every tool_use paired with a result, keeping the
// transcript valid for a later turn.
func (a *Agent) recordCanceled(calls []llm.ToolCall) {
	msgs := make([]llm.Message, 0, len(calls))
	for _, c := range calls {
		msgs = append(msgs, llm.Message{
			Role: llm.RoleTool, ToolName: c.Name, ToolCallID: c.ID, Content: "canceled",
		})
	}
	a.appendMessages(msgs...)
}

// contextBudget is the approximate token ceiling for the re-sent transcript.
func (a *Agent) contextBudget() int {
	if a.contextLimit > 0 {
		return a.contextLimit
	}
	budget := defaultContextBudget
	if cw, ok := a.provider.(llm.ContextWindower); ok {
		if w := cw.ContextWindow(); w > 0 {
			if scaled := int(float64(w) * contextBudgetFraction); scaled < budget {
				budget = scaled
			}
		}
	}
	return budget
}

// appendMessages appends messages to the transcript under the lock.
func (a *Agent) appendMessages(msgs ...llm.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.transcript = append(a.transcript, msgs...)
}

// setUsage records the latest token accounting under the lock.
func (a *Agent) setUsage(u llm.Usage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastUsage = u
}

// trimTranscript trims the transcript under the lock; see trimTranscriptLocked.
func (a *Agent) trimTranscript() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.trimTranscriptLocked()
}

// trimTranscriptLocked drops the oldest exchanges once the transcript's estimated
// token size exceeds the context budget. It cuts only at human-input boundaries so
// tool_use/tool_result pairs and each assistant turn's leading thinking block stay
// intact, always keeps the leading system message, and never trims the in-progress
// exchange (the most recent user turn onward). The caller holds a.mu.
func (a *Agent) trimTranscriptLocked() {
	budget := a.contextBudget()
	if budget <= 0 || estimateTokens(a.transcript) <= budget {
		return
	}

	var boundaries []int // indices where a human turn begins
	for i, m := range a.transcript {
		if m.Role == llm.RoleUser {
			boundaries = append(boundaries, i)
		}
	}
	if len(boundaries) <= 1 {
		return // only the current exchange remains; nothing safe to drop
	}

	var head []llm.Message
	if a.transcript[0].Role == llm.RoleSystem {
		head = a.transcript[:1]
	}
	headTokens := estimateTokens(head)

	keepFrom := boundaries[len(boundaries)-1] // always keep the current exchange
	for i := len(boundaries) - 2; i >= 0; i-- {
		if headTokens+estimateTokens(a.transcript[boundaries[i]:]) > budget {
			break
		}
		keepFrom = boundaries[i]
	}

	trimmed := make([]llm.Message, 0, len(head)+len(a.transcript)-keepFrom)
	trimmed = append(trimmed, head...)
	trimmed = append(trimmed, a.transcript[keepFrom:]...)
	debug.Logf("agent", "trimmed transcript %d→%d msgs (budget %d tok)", len(a.transcript), len(trimmed), budget)
	a.transcript = trimmed
}

// imageTokenEstimate is a nominal per-image token cost for the trim heuristic.
// Image tokens scale with pixel dimensions, not byte size, so counting the raw
// (base64) bytes would wildly overstate them; a flat nominal cost keeps a
// picture-heavy transcript trimmable without that distortion.
const imageTokenEstimate = 1600

// estimateTokens approximates the token size of a slice of messages. Without a
// local tokenizer it uses a bytes/4 heuristic over all content that gets re-sent
// (text, thinking, and tool-call arguments), plus a small per-message overhead
// and a nominal per-attachment cost.
func estimateTokens(msgs []llm.Message) int {
	chars := 0
	tokens := 0
	for _, m := range msgs {
		chars += len(m.Content) + len(m.Thinking) + 16
		for _, c := range m.ToolCalls {
			chars += len(c.Args) + len(c.Name)
		}
		tokens += len(m.Attachments) * imageTokenEstimate
	}
	return chars/4 + tokens
}

func (a *Agent) generate(ctx context.Context, emit func(Event)) (resp llm.Response, streamed bool, err error) {
	// Record usage on both return paths; skip empty reports so an error turn
	// doesn't clobber the last good count.
	defer func() {
		if resp.Usage.InputTokens > 0 {
			a.setUsage(resp.Usage)
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
			case llm.DeltaServerToolCall:
				emit(Event{Kind: "tool_call", Tool: d.Tool, Text: d.Text})
			case llm.DeltaServerToolResult:
				emit(Event{Kind: "tool_result", Tool: d.Tool, Text: d.Text})
			}
		})
		debug.Logf("llm", "GenerateStream done: text=%d chars, tools=%d, err=%v", len(resp.Text), len(resp.ToolCalls), err)
		return resp, true, err
	}
	debug.Logf("llm", "Generate: %d msg(s), %d tool spec(s)", len(a.transcript), len(a.specs()))
	resp, err = a.provider.Generate(ctx, a.transcript, a.specs())
	debug.Logf("llm", "Generate done: text=%d chars, tools=%d, err=%v", len(resp.Text), len(resp.ToolCalls), err)
	// Non-streaming providers can't interleave; surface any server-side tool
	// use (e.g. web search) after the turn returns.
	if err == nil {
		for _, st := range resp.ServerToolUses {
			emit(Event{Kind: "tool_call", Tool: st.Name, Text: st.Args})
			emit(Event{Kind: "tool_result", Tool: st.Name, Text: st.Result})
		}
	}
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
