// Package agent drives the think→act→observe loop.
package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
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

// learnOverlay is appended to the system prompt while learn mode is on, turning
// the agent into a pair-programming tutor that teaches Go and the codebase as it
// works. Asides are optional and skimmable so they never block or slow the task.
const learnOverlay = "\n\n--- Learn mode (pair programming) ---\n" +
	"The user is learning Go and this codebase. Teach as you work, but never block or slow the task waiting on them.\n" +
	"- Before a non-trivial edit, add a one-line 'why': what you're changing and how it fits the code you just read (its callers, callees, and the package's role).\n" +
	"- When you use a Go idiom a newcomer may not know (pointer vs value receivers, goroutines and channels, interface satisfaction, defer, error wrapping, struct embedding), add a one- or two-line aside explaining it in this concrete context.\n" +
	"- Point at the exact file:line you're referring to so the user can read along.\n" +
	"- Keep asides short and skimmable; the user can ignore them. No quizzes, no asking them to confirm understanding, no waiting for a reply.\n" +
	"- Teaching augments the work; still complete the task."

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

// WithSummarizer installs a one-shot summarizer used to compact the oldest
// exchanges into a single "memory" turn when the transcript exceeds the context
// budget, instead of dropping them outright. A nil summarizer leaves compaction
// off and the agent falls back to boundary-safe eviction.
func WithSummarizer(fn func(context.Context, string) (string, error)) Option {
	return func(a *Agent) { a.summarize = fn }
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
	learnMode    bool      // when true, learnOverlay is appended to the system message
	// summarize compacts evicted exchanges into a memory turn; nil ⇒ plain eviction.
	summarize func(context.Context, string) (string, error)

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

// systemContentLocked returns the effective system message: the base prompt plus
// the learn overlay when learn mode is on. The caller holds a.mu.
func (a *Agent) systemContentLocked() string {
	if a.learnMode {
		return a.systemPrompt + learnOverlay
	}
	return a.systemPrompt
}

// SetLearnMode toggles the pair-programming teaching overlay and rewrites the
// live system message so the change takes effect on the next turn without
// discarding the conversation.
func (a *Agent) SetLearnMode(on bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.learnMode = on
	if len(a.transcript) > 0 && a.transcript[0].Role == llm.RoleSystem {
		a.transcript[0].Content = a.systemContentLocked()
	}
}

// LearnMode reports whether the teaching overlay is active.
func (a *Agent) LearnMode() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.learnMode
}

// Reset discards the running transcript and starts a fresh conversation.
func (a *Agent) Reset() {
	a.TakeSteering()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.transcript = nil
	a.lastUsage = llm.Usage{}
	if a.systemPrompt != "" {
		a.transcript = append(a.transcript, llm.Message{Role: llm.RoleSystem, Content: a.systemContentLocked()})
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
		a.transcript = append(a.transcript, llm.Message{Role: llm.RoleSystem, Content: a.systemContentLocked()})
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
		debug.Debug("agent", "steer", "input", truncate(msg.Text, 256), "attachments", len(msg.Attachments))
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
	debug.Info("agent", "run start", "input", truncate(input, 256), "attachments", len(attachments), "provider", a.provider.Name())
	runTimer := debug.NewTimer("agent", "run done")
	a.appendMessages(llm.Message{Role: llm.RoleUser, Content: input, Attachments: attachments})
	a.compactTranscript(ctx)

	for step := range maxSteps {
		debug.Debug("agent", "step", "n", step+1, "max", maxSteps)
		if err := ctx.Err(); err != nil {
			runTimer.Stop("outcome", "canceled", "step", step+1)
			return "", err
		}
		// Fold in any user messages queued since the previous step so the model
		// sees them before generating its next turn.
		a.injectSteering()

		resp, streamed, err := a.generate(ctx, emit)
		if err != nil {
			debug.Warn("agent", "generate error", "err", err)
			// A canceled run unwinds quietly: the UI already knows and a spurious
			// error line would only add noise.
			if ctx.Err() != nil {
				runTimer.Stop("outcome", "canceled", "step", step+1)
				return "", ctx.Err()
			}
			emit(Event{Kind: "error", Text: err.Error()})
			runTimer.Stop("outcome", "error", "step", step+1)
			return "", err
		}

		// Plain text reply: record and finish. When streamed, the text already
		// reached the UI via deltas, so only emit a final "text" event on the
		// non-streaming path.
		if len(resp.ToolCalls) == 0 {
			debug.Info("agent", "final reply", "chars", len(resp.Text), "streamed", streamed)
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
			runTimer.Stop("outcome", "reply", "step", step+1)
			return resp.Text, nil
		}

		// Record the assistant tool-use turn verbatim (thinking first, then any
		// leading text) so the provider can replay it on the next request.
		debug.Debug("agent", "model requested tool calls", "count", len(resp.ToolCalls))
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
				debug.Debug("tool", "gate review", "tool", call.Name, "allowed", rr.Allowed, "source", rr.Source, "risk", rr.Risk)
				if call.Name == "bash" && rr.Source != "off" {
					emit(Event{Kind: "tool_review", Tool: call.Name, Text: reviewLine(rr)})
				}
				if !rr.Allowed {
					debug.Warn("tool", "refused by gate", "tool", call.Name, "source", rr.Source, "reason", rr.Reason)
					emit(Event{Kind: "tool_call", Tool: call.Name, Text: string(call.Args)})
					result := fmt.Sprintf("refused by auto mode (%s): %s", rr.Source, rr.Reason)
					emit(Event{Kind: "error", Tool: call.Name, Text: result})
					a.appendMessages(llm.Message{
						Role: llm.RoleTool, ToolName: call.Name, ToolCallID: call.ID, Content: result,
					})
					continue
				}
			}

			debug.Debug("tool", "invoke", "tool", call.Name, "args", truncate(string(call.Args), 512))
			emit(Event{Kind: "tool_call", Tool: call.Name, Text: string(call.Args)})

			toolTimer := debug.NewTimer("tool", "result")
			result, err := a.registry.Invoke(ctx, call.Name, call.Args)
			if err != nil && ctx.Err() != nil {
				// Canceled mid-tool: record canceled results for this and every
				// remaining call, then unwind quietly.
				toolTimer.Stop("tool", call.Name, "outcome", "canceled")
				a.recordCanceled(resp.ToolCalls[i:])
				runTimer.Stop("outcome", "canceled", "step", step+1)
				return "", ctx.Err()
			}
			if err != nil {
				debug.Warn("tool", "failed", "tool", call.Name, "err", err)
				toolTimer.Stop("tool", call.Name, "outcome", "error")
				result = "error: " + err.Error()
				emit(Event{Kind: "error", Tool: call.Name, Text: err.Error()})
			} else {
				debug.Debug("tool", "ok", "tool", call.Name, "result", truncate(result, 512))
				toolTimer.Stop("tool", call.Name, "outcome", "ok", "chars", len(result))
				emit(Event{Kind: "tool_result", Tool: call.Name, Text: result})
			}
			a.appendMessages(llm.Message{
				Role: llm.RoleTool, ToolName: call.Name, ToolCallID: call.ID, Content: result,
			})
		}
	}

	debug.Warn("agent", "step budget exhausted", "steps", maxSteps)
	err := fmt.Errorf("agent: exceeded %d steps without completing", maxSteps)
	emit(Event{Kind: "error", Text: err.Error()})
	runTimer.Stop("outcome", "step-budget-exhausted", "steps", maxSteps)
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
// exchange (the most recent user turn onward). It is also the fallback for
// compactTranscript when no summarizer is available. The caller holds a.mu.
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
	debug.Debug("agent", "trimmed transcript", "from", len(a.transcript), "to", len(trimmed), "budget_tok", budget)
	a.transcript = trimmed
}

// memoryPrefix marks a compacted "memory" turn so the model reads it as a summary
// of earlier conversation rather than a fresh user request.
const memoryPrefix = "[Memory — a summary of earlier conversation, compacted to save context]\n"

const (
	// maxCompactionInputChars bounds the excerpt sent to the summarizer so
	// compaction stays cheap no matter how much history is being evicted.
	maxCompactionInputChars = 12_000
	// maxSummaryChars caps the memory turn so it stays small and re-compactable.
	maxSummaryChars = 4_000
)

// compactionPlan is the boundary-safe split computed under the lock and applied
// after summarizing off-lock: head (the system message) and the retained tail are
// kept verbatim, while evicted is condensed into a single memory turn.
type compactionPlan struct {
	head    []llm.Message
	evicted []llm.Message
	tail    []llm.Message
	budget  int
}

// compactTranscript keeps the transcript within the context budget by summarizing
// the oldest evictable exchanges into a single memory turn kept in their place,
// preserving early decisions and constraints instead of dropping them. When no
// summarizer is wired, the context is fine, or summarization fails, it falls back
// to boundary-safe eviction (trimTranscriptLocked). The summarizer call runs
// without a.mu held (a single agent runs one turn at a time, so no other writer
// mutates the transcript during the call).
func (a *Agent) compactTranscript(ctx context.Context) {
	if a.summarize == nil || ctx.Err() != nil {
		a.trimTranscript()
		return
	}

	a.mu.Lock()
	plan, ok := a.compactionPlanLocked()
	a.mu.Unlock()
	if !ok {
		return // within budget, or nothing safe to evict
	}

	debug.Info("agent", "compaction start", "evict_msgs", len(plan.evicted), "budget_tok", plan.budget)
	timer := debug.NewTimer("agent", "compaction summarize")
	summary, err := a.summarize(ctx, compactionPrompt(plan.evicted))
	timer.Stop("chars", len(summary), "err", errText(err))

	summary = truncate(strings.TrimSpace(summary), maxSummaryChars)
	if err != nil || summary == "" {
		debug.Warn("agent", "compaction fell back to eviction", "err", errText(err), "empty", summary == "")
		a.trimTranscript()
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	before := len(a.transcript)
	rebuilt := make([]llm.Message, 0, len(plan.head)+1+len(plan.tail))
	rebuilt = append(rebuilt, plan.head...)
	rebuilt = append(rebuilt, llm.Message{Role: llm.RoleUser, Content: memoryPrefix + summary})
	rebuilt = append(rebuilt, plan.tail...)
	a.transcript = rebuilt
	debug.Info("agent", "compacted transcript", "from", before, "to", len(a.transcript),
		"evicted", len(plan.evicted), "summary_chars", len(summary), "budget_tok", plan.budget)
}

// compactionPlanLocked mirrors trimTranscriptLocked's boundary math to decide
// which oldest exchanges to evict. It returns ok=false when the transcript is
// within budget or only the in-progress exchange remains (nothing safe to evict).
// The retained tail already fits the budget by construction, so a prior memory
// turn falls into evicted and is re-summarized on a very long session. The caller
// holds a.mu.
func (a *Agent) compactionPlanLocked() (compactionPlan, bool) {
	budget := a.contextBudget()
	if budget <= 0 || estimateTokens(a.transcript) <= budget {
		return compactionPlan{}, false
	}

	var boundaries []int // indices where a human turn begins
	for i, m := range a.transcript {
		if m.Role == llm.RoleUser {
			boundaries = append(boundaries, i)
		}
	}
	if len(boundaries) <= 1 {
		return compactionPlan{}, false // only the current exchange remains
	}

	headEnd := 0
	if a.transcript[0].Role == llm.RoleSystem {
		headEnd = 1
	}
	headTokens := estimateTokens(a.transcript[:headEnd])

	keepFrom := boundaries[len(boundaries)-1] // always keep the current exchange
	for i := len(boundaries) - 2; i >= 0; i-- {
		if headTokens+estimateTokens(a.transcript[boundaries[i]:]) > budget {
			break
		}
		keepFrom = boundaries[i]
	}
	if keepFrom <= headEnd {
		return compactionPlan{}, false // nothing ahead of the retained tail to evict
	}

	return compactionPlan{
		head:    slices.Clone(a.transcript[:headEnd]),
		evicted: slices.Clone(a.transcript[headEnd:keepFrom]),
		tail:    slices.Clone(a.transcript[keepFrom:]),
		budget:  budget,
	}, true
}

// compactionPrompt builds the one-shot summarization request for the evicted
// exchanges, asking for a terse, factual memory a coding agent can continue from.
func compactionPrompt(evicted []llm.Message) string {
	var b strings.Builder
	b.WriteString("You are compacting the earlier part of a long coding-agent conversation to preserve its ")
	b.WriteString("essential memory in far fewer tokens. Summarize the exchange below into a compact note that ")
	b.WriteString("lets the agent continue without re-reading the originals. Capture the user's goals and ")
	b.WriteString("constraints, decisions made and why, files and code changed, key discoveries, and any open ")
	b.WriteString("threads or next steps. Prefer terse bullet points, do not invent details, and omit greetings ")
	b.WriteString("and chatter.\n\n--- conversation to summarize ---")
	b.WriteString(renderEvicted(evicted))
	return b.String()
}

// renderEvicted renders the evicted messages as a compact, role-tagged transcript,
// truncating each part and the whole so the summarizer input stays bounded.
func renderEvicted(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		if b.Len() >= maxCompactionInputChars {
			b.WriteString("\n…(earlier content truncated)")
			break
		}
		switch m.Role {
		case llm.RoleUser:
			b.WriteString("\nUSER: ")
			b.WriteString(truncate(m.Content, 1000))
		case llm.RoleModel:
			if m.Thinking != "" {
				b.WriteString("\nASSISTANT (thinking): ")
				b.WriteString(truncate(m.Thinking, 500))
			}
			if m.Content != "" {
				b.WriteString("\nASSISTANT: ")
				b.WriteString(truncate(m.Content, 1000))
			}
			for _, c := range m.ToolCalls {
				b.WriteString("\nASSISTANT called ")
				b.WriteString(c.Name)
				b.WriteString(": ")
				b.WriteString(truncate(string(c.Args), 300))
			}
		case llm.RoleTool:
			b.WriteString("\nTOOL ")
			b.WriteString(m.ToolName)
			b.WriteString(" result: ")
			b.WriteString(truncate(m.Content, 500))
		}
	}
	return b.String()
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
		debug.Debug("llm", "GenerateStream request", "messages", len(a.transcript), "tools", len(a.specs()), "model", a.provider.Name())
		t := debug.NewTimer("llm", "GenerateStream done")
		resp, err = sp.GenerateStream(ctx, a.transcript, a.specs(), func(d llm.StreamDelta) {
			switch d.Kind {
			case llm.DeltaText:
				emit(Event{Kind: "text_delta", Text: d.Text})
			case llm.DeltaThinking:
				emit(Event{Kind: "thinking_delta", Text: d.Text})
			case llm.DeltaServerToolCall:
				debug.Debug("tool", "server tool call", "tool", d.Tool, "args", truncate(d.Text, 512))
				emit(Event{Kind: "tool_call", Tool: d.Tool, Text: d.Text})
			case llm.DeltaServerToolResult:
				debug.Debug("tool", "server tool result", "tool", d.Tool, "chars", len(d.Text), "result", truncate(d.Text, 512))
				emit(Event{Kind: "tool_result", Tool: d.Tool, Text: d.Text})
			}
		})
		t.Stop("chars", len(resp.Text), "tools", len(resp.ToolCalls),
			"in_tok", resp.Usage.InputTokens, "out_tok", resp.Usage.OutputTokens, "err", errText(err))
		return resp, true, err
	}
	debug.Debug("llm", "Generate request", "messages", len(a.transcript), "tools", len(a.specs()), "model", a.provider.Name())
	t := debug.NewTimer("llm", "Generate done")
	resp, err = a.provider.Generate(ctx, a.transcript, a.specs())
	t.Stop("chars", len(resp.Text), "tools", len(resp.ToolCalls),
		"in_tok", resp.Usage.InputTokens, "out_tok", resp.Usage.OutputTokens, "err", errText(err))
	// Non-streaming providers can't interleave; surface any server-side tool
	// use (e.g. web search) after the turn returns.
	if err == nil {
		for _, st := range resp.ServerToolUses {
			debug.Debug("tool", "server tool use", "tool", st.Name,
				"args", truncate(st.Args, 512), "result", truncate(st.Result, 512))
			emit(Event{Kind: "tool_call", Tool: st.Name, Text: st.Args})
			emit(Event{Kind: "tool_result", Tool: st.Name, Text: st.Result})
		}
	}
	return resp, false, err
}

// errText renders an error for a log field, or "" for nil so a clean turn logs err="".
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
