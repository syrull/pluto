// Package worker runs parallel sub-agents on behalf of a persistent
// orchestrator. Each worker is a lightweight agent loop in its own goroutine
// with its own conversation, a scoped tool subset (least privilege), preloaded
// skills, and a hard budget. Dispatch is non-blocking: the orchestrator launches
// workers, keeps reasoning, and pulls concise structured results on demand via
// Poll — worker transcripts never enter the orchestrator's context. Coordination
// happens through a shared, append-only blackboard (see internal/session).
package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/session"
	"github.com/syrull/pluto/internal/skills"
	"github.com/syrull/pluto/internal/tool"
	"github.com/syrull/pluto/internal/workdir"
)

// State is a worker's lifecycle state.
type State string

const (
	// StatePending means the worker is dispatched but waiting on a concurrency or
	// rate slot before it can start.
	StatePending State = "pending"
	// StateRunning means the worker's agent loop is executing.
	StateRunning State = "running"
	// StateDone means the worker finished — either it completed on its own or a
	// budget reaped it (see Status.StopReason).
	StateDone State = "done"
	// StateFailed means the worker stopped on an error other than a budget.
	StateFailed State = "failed"
	// StateCanceled means the worker was canceled before or during its run.
	StateCanceled State = "canceled"
)

// maxTranscriptEvents bounds a worker's retained transcript so a long-running or
// looping worker can't grow the pool's memory without bound.
const maxTranscriptEvents = 500

// maxRetainedWorkers caps how many workers the pool keeps so a long session with
// many fan-outs can't grow its memory (and a poll-all response) without bound.
// Oldest terminal workers are pruned first; live workers are always retained.
var maxRetainedWorkers = 256

// defaultBudget bounds a worker whose dispatch omitted a budget, so the promise
// that "a stuck or looping worker is reaped" holds even when the orchestrator
// forgets to set one. It is deliberately generous; an explicit budget overrides it.
var defaultBudget = agent.Budget{Turns: 40, Wall: 10 * time.Minute}

// Spec describes a worker to dispatch: a scoped task, a least-privilege tool
// subset, skills to preload, a hard budget, and the target scope (also the
// per-target rate-limit key).
type Spec struct {
	ID     string
	Task   string
	Tools  []string
	Skills []string
	Budget agent.Budget
	Scope  string
}

// Event is one coalesced entry in a worker's live transcript, kept for TUI
// inspection and debug reconstruction. Consecutive text/thinking chunks are
// merged so the transcript stays compact.
type Event struct {
	Kind string // "text", "thinking", "tool_call", "tool_result", "error"
	Tool string
	Text string
}

// Results is the concise, structured outcome the orchestrator pulls for a
// worker. It merges cleanly into the blackboard (the fact lists come straight
// from what the worker appended) and never carries the raw transcript.
type Results struct {
	Summary   string   `json:"summary,omitempty"`
	Hosts     []string `json:"hosts,omitempty"`
	Services  []string `json:"services,omitempty"`
	Creds     []string `json:"creds,omitempty"`
	Footholds []string `json:"footholds,omitempty"`
	Vulns     []string `json:"vulns,omitempty"`
	Flags     []string `json:"flags,omitempty"`
	Notes     []string `json:"notes,omitempty"`
}

// Status is a worker's observable state, returned by Poll (non-blocking) and
// used to render the live workers panel.
type Status struct {
	ID          string
	Task        string
	Scope       string
	State       State
	CurrentTool string
	StopReason  string
	ToolCalls   int
	Tokens      int
	Elapsed     time.Duration
	Budget      agent.Budget
	Results     Results
	Err         string
}

// Config wires a Pool to the rest of pluto. The Pool builds each worker agent
// from these shared parts so scope/judge context propagates to children and
// workers never re-fight the safety gate.
type Config struct {
	// Provider is the model backend every worker runs on (same as the orchestrator).
	Provider llm.Provider
	// Registry is the base tool registry a worker's allowed subset is drawn from.
	Registry *tool.Registry
	// Gate is the shared review gate (guard + judge); passing it to workers is how
	// scope/judge context propagates so children don't each re-fight the judge. Nil
	// leaves workers ungated (matching an orchestrator with auto mode off).
	Gate agent.Gate
	// Summarize compacts a worker's own context when it overflows; optional.
	Summarize func(context.Context, string) (string, error)
	// SkillsDir is where preloaded skills are read from.
	SkillsDir string
	// Board is the shared append-only blackboard workers append facts to.
	Board *session.Blackboard
	// Limits bound per-target concurrency and rate, and total concurrency.
	Limits Limits
}

// worker is the live, mutable state behind a dispatched Spec.
type worker struct {
	mu          sync.RWMutex
	spec        Spec
	state       State
	currentTool string
	stopReason  string
	toolCalls   int
	summary     string
	err         error
	startedAt   time.Time
	endedAt     time.Time
	events      []Event
	ag          *agent.Agent // set once the run starts; read for live token spend
	cancel      context.CancelFunc
}

// Pool owns the dispatched workers and their concurrency. It is safe for
// concurrent use: the orchestrator's dispatch tool and the TUI observe it from
// different goroutines.
type Pool struct {
	cfg     Config
	base    context.Context
	limiter *limiter

	mu      sync.Mutex
	workers map[string]*worker
	order   []string
	seq     int
}

// NewPool builds a Pool. base is the pool's lifetime context — workers derive
// their run context from it, not from any single orchestrator turn, so they
// outlive the dispatch call. A nil base defaults to context.Background().
func NewPool(base context.Context, cfg Config) *Pool {
	if base == nil {
		base = context.Background()
	}
	if cfg.Board == nil {
		cfg.Board = session.NewBlackboard()
	}
	return &Pool{
		cfg:     cfg,
		base:    base,
		limiter: newLimiter(cfg.Limits),
		workers: make(map[string]*worker),
	}
}

// Board returns the shared blackboard the workers append to.
func (p *Pool) Board() *session.Blackboard { return p.cfg.Board }

// Dispatch launches one worker per spec and returns their ids immediately —
// this is the non-blocking fan-out. ctx supplies the working directory workers
// inherit (via workdir); its cancellation does not stop the workers, which run
// on the pool's lifetime context so the orchestrator can keep going. A spec with
// a blank Task is skipped.
func (p *Pool) Dispatch(ctx context.Context, specs []Spec) []string {
	dir := workdir.From(ctx)
	ids := make([]string, 0, len(specs))
	for _, spec := range specs {
		spec.Task = strings.TrimSpace(spec.Task)
		if spec.Task == "" {
			debug.Warn("orchestrator", "dispatch skipped (empty task)")
			continue
		}
		// A budget is mandatory; supply a default when the orchestrator omitted one
		// so every worker is reapable.
		if spec.Budget.IsZero() {
			spec.Budget = defaultBudget
			debug.Debug("orchestrator", "worker budget defaulted",
				"turns", spec.Budget.Turns, "wall", spec.Budget.Wall)
		}
		id := p.register(spec)
		spec.ID = id
		w := p.get(id)
		wctx, cancel := context.WithCancel(workdir.With(p.base, dir))
		w.setCancel(cancel)
		debug.Info("orchestrator", "dispatch worker", "id", id, "scope", spec.Scope,
			"tools", strings.Join(spec.Tools, ","), "skills", strings.Join(spec.Skills, ","),
			"budget_turns", spec.Budget.Turns, "budget_tokens", spec.Budget.Tokens, "budget_wall", spec.Budget.Wall)
		go p.run(wctx, w)
		ids = append(ids, id)
	}
	debug.Info("orchestrator", "dispatch done", "launched", len(ids), "requested", len(specs))
	return ids
}

// Poll returns the current status of the named workers, or of every worker when
// ids is empty. It never blocks: it is how the orchestrator gathers results on
// its own schedule without stalling.
func (p *Pool) Poll(ids []string) []Status {
	var selected []*worker
	p.mu.Lock()
	if len(ids) == 0 {
		for _, id := range p.order {
			selected = append(selected, p.workers[id])
		}
	} else {
		for _, id := range ids {
			if w, ok := p.workers[id]; ok {
				selected = append(selected, w)
			}
		}
	}
	p.mu.Unlock()

	out := make([]Status, 0, len(selected))
	for _, w := range selected {
		out = append(out, p.status(w))
	}
	debug.Debug("orchestrator", "poll", "requested", len(ids), "returned", len(out))
	return out
}

// Cancel stops the named workers and returns the ids actually canceled (a worker
// already finished is not reported). Cancellation reclaims its concurrency slot.
func (p *Pool) Cancel(ids []string) []string {
	var canceled []string
	for _, id := range ids {
		w := p.get(id)
		if w == nil {
			continue
		}
		w.mu.Lock()
		terminal := w.state == StateDone || w.state == StateFailed || w.state == StateCanceled
		cancel := w.cancel
		w.mu.Unlock()
		if terminal || cancel == nil {
			continue
		}
		cancel()
		canceled = append(canceled, id)
		debug.Info("orchestrator", "cancel worker", "id", id)
	}
	return canceled
}

// Snapshot returns the status of every worker in dispatch order, for the TUI.
func (p *Pool) Snapshot() []Status { return p.Poll(nil) }

// register creates a pending worker, assigning an id when the spec has none, and
// returns the id.
func (p *Pool) register(spec Spec) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	id := strings.TrimSpace(spec.ID)
	if id == "" || p.workers[id] != nil {
		id = fmt.Sprintf("w%d", p.seq)
	}
	spec.ID = id
	p.workers[id] = &worker{spec: spec, state: StatePending}
	p.order = append(p.order, id)
	p.pruneLocked()
	return id
}

// pruneLocked drops the oldest terminal workers once the pool exceeds
// maxRetainedWorkers, bounding memory and the poll-all response on a long
// session. Live (pending/running) workers are never pruned. The caller holds p.mu.
func (p *Pool) pruneLocked() {
	excess := len(p.order) - maxRetainedWorkers
	if excess <= 0 {
		return
	}
	kept := make([]string, 0, len(p.order))
	pruned := 0
	for _, id := range p.order {
		if pruned < excess {
			w := p.workers[id]
			w.mu.RLock()
			terminal := w.state == StateDone || w.state == StateFailed || w.state == StateCanceled
			w.mu.RUnlock()
			if terminal {
				delete(p.workers, id)
				pruned++
				continue
			}
		}
		kept = append(kept, id)
	}
	p.order = kept
	if pruned > 0 {
		debug.Info("orchestrator", "pruned terminal workers", "pruned", pruned, "retained", len(p.order))
	}
}

func (p *Pool) get(id string) *worker {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.workers[id]
}

// provider returns the pool's current model backend under the lock, so a
// concurrent SetProvider (from /login) never races a worker being built.
func (p *Pool) provider() llm.Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cfg.Provider
}

// SetProvider swaps the backend future workers run on, so a mid-session /login
// (e.g. upgrading from the offline stub) reaches the pool too. Workers already
// running keep the provider they started with. A nil provider is ignored.
func (p *Pool) SetProvider(prov llm.Provider) {
	if prov == nil {
		return
	}
	p.mu.Lock()
	p.cfg.Provider = prov
	p.mu.Unlock()
	debug.Info("orchestrator", "pool provider upgraded", "provider", prov.Name())
}

// run executes one worker: it waits for a concurrency/rate slot, builds a scoped
// agent, runs the task to its budget, and classifies the outcome.
func (p *Pool) run(ctx context.Context, w *worker) {
	// Contain a worker fault to that worker: without this, a panic in the agent
	// loop, a scoped tool, or an MCP tool would take down the whole process (and
	// with it the orchestrator and every sibling worker).
	defer func() {
		if r := recover(); r != nil {
			debug.Error("worker", "panic recovered", "id", w.spec.ID, "panic", fmt.Sprint(r))
			w.finish(StateFailed, "panic", fmt.Errorf("worker panicked: %v", r))
		}
	}()
	spec := w.spec
	spawn := debug.NewTimer("worker", "spawn→acquire")
	if err := p.limiter.acquire(ctx, spec.Scope); err != nil {
		spawn.Stop("id", spec.ID, "outcome", "canceled")
		w.finish(StateCanceled, "canceled", err)
		debug.Warn("worker", "canceled before start", "id", spec.ID, "err", err)
		return
	}
	spawn.Stop("id", spec.ID, "outcome", "acquired")
	defer p.limiter.release(spec.Scope)

	// A worker canceled while queued must not then start running.
	if err := ctx.Err(); err != nil {
		w.finish(StateCanceled, "canceled", err)
		return
	}

	ag := p.buildAgent(spec)
	w.setRunning(ag)
	debug.Info("worker", "run start", "id", spec.ID, "scope", spec.Scope, "task", truncate(spec.Task, 200))

	runTimer := debug.NewTimer("worker", "run done")
	summary, err := ag.Run(ctx, spec.Task, nil, w.emit)
	state, reason := classify(err)
	w.recordSummary(summary)
	w.finish(state, reason, err)
	runTimer.Stop("id", spec.ID, "state", string(state), "reason", reason,
		"tool_calls", w.snapshotToolCalls(), "tokens", ag.Spent(), "err", errText(err))
	debug.Info("worker", "run finished", "id", spec.ID, "state", string(state), "reason", reason)
}

// classify maps a Run error onto a terminal state and a stop reason. A budget or
// wall-clock stop is a normal terminal outcome (StateDone), not a failure.
func classify(err error) (State, string) {
	switch {
	case err == nil:
		return StateDone, "completed"
	case errors.Is(err, agent.ErrBudgetExhausted):
		reason := "budget"
		if msg := err.Error(); strings.Contains(msg, "(turns)") {
			reason = "budget:turns"
		} else if strings.Contains(msg, "(tokens)") {
			reason = "budget:tokens"
		}
		return StateDone, reason
	case errors.Is(err, context.DeadlineExceeded):
		return StateDone, "budget:wall"
	case errors.Is(err, context.Canceled):
		return StateCanceled, "canceled"
	default:
		return StateFailed, "error"
	}
}

// buildAgent constructs a worker's agent: a fresh registry holding only the
// allowed tool subset plus the worker's own note tool, the shared review gate
// (so scope/judge context propagates), and the spec's hard budget.
func (p *Pool) buildAgent(spec Spec) *agent.Agent {
	reg := tool.NewRegistry()
	// Register the note tool first so a granted tool that happens to be named
	// "note" fails a harmless duplicate Register (below) instead of tripping a
	// MustRegister panic that would crash the whole process.
	reg.MustRegister(newNoteTool(p.cfg.Board, spec.ID))
	var granted, missing []string
	for _, name := range spec.Tools {
		if t, ok := p.cfg.Registry.Lookup(name); ok {
			if err := reg.Register(t); err == nil {
				granted = append(granted, name)
			}
			continue
		}
		missing = append(missing, name)
	}
	debug.Info("worker", "tools scoped", "id", spec.ID,
		"granted", strings.Join(granted, ","), "missing", strings.Join(missing, ","))

	sys := p.workerPrompt(spec)
	opts := []agent.Option{agent.WithBudget(spec.Budget)}
	if p.cfg.Gate != nil {
		opts = append(opts, agent.WithGate(p.cfg.Gate))
	}
	if p.cfg.Summarize != nil {
		opts = append(opts, agent.WithSummarizer(p.cfg.Summarize))
	}
	return agent.New(p.provider(), reg, sys, opts...)
}

// workerPrompt frames a worker as an autonomous specialist: it states the scope
// (rules of engagement), preloads the requested skills so the worker is an
// instant expert, and tells it to record findings on the blackboard and finish
// with a concise summary rather than a long transcript.
func (p *Pool) workerPrompt(spec Spec) string {
	var b strings.Builder
	b.WriteString("You are a worker sub-agent dispatched by an orchestrator to complete one focused task ")
	b.WriteString("autonomously and report back concisely. Work only within your task and the tools you were given. ")
	b.WriteString("As you make concrete findings, record each one on the shared blackboard with the note tool ")
	b.WriteString("(kind + value) so the orchestrator and other workers can act on it immediately. ")
	b.WriteString("Do not ask the orchestrator questions — decide and act. When finished, end with a short, ")
	b.WriteString("structured summary of what you found and did; keep it tight, the orchestrator only reads the summary.")
	if s := strings.TrimSpace(spec.Scope); s != "" {
		b.WriteString("\n\n--- Scope / rules of engagement (stay strictly within this) ---\n")
		b.WriteString(s)
	}
	for _, name := range spec.Skills {
		body, err := skills.Load(p.cfg.SkillsDir, name)
		if err != nil {
			debug.Warn("worker", "skill preload failed", "id", spec.ID, "skill", name, "err", err)
			continue
		}
		debug.Info("worker", "skill preloaded", "id", spec.ID, "skill", name, "chars", len(body))
		fmt.Fprintf(&b, "\n\n--- Preloaded skill: %s ---\n%s", name, body)
	}
	return b.String()
}

// emit folds one agent event into the worker's live state: it tracks the current
// tool, counts tool calls, and appends a coalesced transcript entry.
func (w *worker) emit(ev agent.Event) {
	w.mu.Lock()
	switch ev.Kind {
	case "text_delta":
		w.appendTextLocked("text", ev.Text)
	case "thinking_delta":
		w.appendTextLocked("thinking", ev.Text)
	case "text":
		w.appendTextLocked("text", ev.Text)
	case "tool_call":
		w.currentTool = ev.Tool
		w.toolCalls++
		w.appendEventLocked(Event{Kind: "tool_call", Tool: ev.Tool, Text: truncate(ev.Text, 400)})
	case "tool_result":
		w.currentTool = ""
		w.appendEventLocked(Event{Kind: "tool_result", Tool: ev.Tool, Text: truncate(ev.Text, 400)})
	case "error":
		w.currentTool = ""
		w.appendEventLocked(Event{Kind: "error", Tool: ev.Tool, Text: truncate(ev.Text, 400)})
	}
	w.mu.Unlock()
	debug.Trace("worker", "event", "id", w.spec.ID, "kind", ev.Kind, "tool", ev.Tool, "chars", len(ev.Text))
}

// appendTextLocked merges a text/thinking chunk into the trailing event of the
// same kind, or starts a new one. The caller holds w.mu.
func (w *worker) appendTextLocked(kind, text string) {
	if text == "" {
		return
	}
	if n := len(w.events); n > 0 && w.events[n-1].Kind == kind {
		w.events[n-1].Text = truncate(w.events[n-1].Text+text, 4000)
		return
	}
	w.appendEventLocked(Event{Kind: kind, Text: truncate(text, 4000)})
}

// appendEventLocked appends an event, dropping the oldest once the cap is hit so
// the retained transcript stays bounded. The caller holds w.mu.
func (w *worker) appendEventLocked(ev Event) {
	w.events = append(w.events, ev)
	if len(w.events) > maxTranscriptEvents {
		w.events = w.events[len(w.events)-maxTranscriptEvents:]
	}
}

// setCancel stores the run's cancel func under the lock, so Cancel (which may run
// on a different goroutine, e.g. the TUI) never races the dispatch that sets it.
func (w *worker) setCancel(cancel context.CancelFunc) {
	w.mu.Lock()
	w.cancel = cancel
	w.mu.Unlock()
}

func (w *worker) setRunning(ag *agent.Agent) {
	w.mu.Lock()
	w.state = StateRunning
	w.startedAt = time.Now()
	w.ag = ag
	w.mu.Unlock()
}

func (w *worker) recordSummary(summary string) {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}
	w.mu.Lock()
	w.summary = summary
	w.mu.Unlock()
}

func (w *worker) finish(state State, reason string, err error) {
	w.mu.Lock()
	w.state = state
	w.stopReason = reason
	w.err = err
	if w.endedAt.IsZero() {
		w.endedAt = time.Now()
	}
	w.currentTool = ""
	w.mu.Unlock()
}

func (w *worker) snapshotToolCalls() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.toolCalls
}

// Transcript returns a copy of the worker's live transcript, for TUI inspection.
func (p *Pool) Transcript(id string) ([]Event, bool) {
	w := p.get(id)
	if w == nil {
		return nil, false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]Event, len(w.events))
	copy(out, w.events)
	return out, true
}

// status renders a worker's observable Status, folding in the structured
// results it appended to the blackboard.
func (p *Pool) status(w *worker) Status {
	w.mu.RLock()
	st := Status{
		ID:          w.spec.ID,
		Task:        w.spec.Task,
		Scope:       w.spec.Scope,
		State:       w.state,
		CurrentTool: w.currentTool,
		StopReason:  w.stopReason,
		ToolCalls:   w.toolCalls,
		Budget:      w.spec.Budget,
		Results:     Results{Summary: w.summary},
	}
	if w.ag != nil {
		st.Tokens = w.ag.Spent()
	}
	if !w.startedAt.IsZero() {
		end := w.endedAt
		if end.IsZero() {
			end = time.Now()
		}
		st.Elapsed = end.Sub(w.startedAt)
	}
	if w.err != nil {
		st.Err = w.err.Error()
	}
	w.mu.RUnlock()

	st.Results = mergeResults(st.Results.Summary, p.cfg.Board.FactsBy(w.spec.ID))
	return st
}

// resultValueCap bounds a single fact value and resultSummaryCap the summary, so
// polling can never balloon the orchestrator's context no matter what a worker
// wrote — only concise structured results cross back.
const (
	resultValueCap   = 500
	resultSummaryCap = 2000
)

// mergeResults groups a worker's blackboard facts into the structured Results
// the orchestrator consumes, truncating each value so poll output stays concise.
func mergeResults(summary string, facts []session.Fact) Results {
	r := Results{Summary: truncate(summary, resultSummaryCap)}
	for _, f := range facts {
		v := f.Value
		if f.Detail != "" {
			v += " (" + f.Detail + ")"
		}
		v = truncate(v, resultValueCap)
		switch f.Kind {
		case session.FactHost:
			r.Hosts = append(r.Hosts, v)
		case session.FactService:
			r.Services = append(r.Services, v)
		case session.FactCred:
			r.Creds = append(r.Creds, v)
		case session.FactFoothold:
			r.Footholds = append(r.Footholds, v)
		case session.FactVuln:
			r.Vulns = append(r.Vulns, v)
		case session.FactFlag:
			r.Flags = append(r.Flags, v)
		case session.FactSummary:
			if r.Summary == "" {
				r.Summary = v
			}
		default:
			r.Notes = append(r.Notes, v)
		}
	}
	return r
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return fmt.Sprintf("%s…(+%d)", string(r[:n]), len(r)-n)
}
