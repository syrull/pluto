package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/session"
	"github.com/syrull/pluto/internal/tool"
)

// echoTool is a trivial scoped tool used to prove tool subsetting and gating.
type echoTool struct{ name string }

func (t echoTool) Name() string          { return t.name }
func (t echoTool) Description() string   { return "echo " + t.name }
func (echoTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (echoTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "ok", nil
}

// scriptProvider drives a worker's agent from a per-conversation step function,
// so each worker steps independently even though they share the provider. step
// is the number of model turns already in this worker's transcript.
type scriptProvider struct {
	gen func(step int, transcript []llm.Message, tools []llm.ToolSpec, ctx context.Context) (llm.Response, error)
}

func (*scriptProvider) Name() string { return "script" }

func (p *scriptProvider) Generate(ctx context.Context, transcript []llm.Message, tools []llm.ToolSpec) (llm.Response, error) {
	step := 0
	for _, m := range transcript {
		if m.Role == llm.RoleModel {
			step++
		}
	}
	return p.gen(step, transcript, tools, ctx)
}

func finalText(text string) llm.Response {
	return llm.Response{Text: text, Usage: llm.Usage{InputTokens: 5}}
}

func callTool(id, name, args string) llm.Response {
	return llm.Response{
		ToolCalls: []llm.ToolCall{{ID: id, Name: name, Args: json.RawMessage(args)}},
		Usage:     llm.Usage{InputTokens: 5},
	}
}

// firstTask returns the first user message (the worker's task prompt).
func firstTask(transcript []llm.Message) string {
	for _, m := range transcript {
		if m.Role == llm.RoleUser {
			return m.Content
		}
	}
	return ""
}

func baseRegistry() *tool.Registry {
	reg := tool.NewRegistry()
	reg.MustRegister(echoTool{name: "read"})
	reg.MustRegister(echoTool{name: "write"})
	reg.MustRegister(echoTool{name: "bash"})
	return reg
}

// waitFor polls cond until true or the deadline, failing the test on timeout.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func statesOf(sts []Status) map[string]State {
	m := make(map[string]State, len(sts))
	for _, s := range sts {
		m[s.ID] = s.State
	}
	return m
}

// TestWorkersRunConcurrently proves fan-out is real: N workers are all inside
// their model call at the same time before any is released.
func TestWorkersRunConcurrently(t *testing.T) {
	const n = 4
	var mu sync.Mutex
	concurrent, max := 0, 0
	release := make(chan struct{})

	prov := &scriptProvider{gen: func(step int, _ []llm.Message, _ []llm.ToolSpec, ctx context.Context) (llm.Response, error) {
		mu.Lock()
		concurrent++
		if concurrent > max {
			max = concurrent
		}
		mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		}
		mu.Lock()
		concurrent--
		mu.Unlock()
		return finalText("done"), nil
	}}

	p := NewPool(context.Background(), Config{Provider: prov, Registry: baseRegistry(), Board: session.NewBlackboard()})
	specs := make([]Spec, n)
	for i := range specs {
		specs[i] = Spec{Task: fmt.Sprintf("task %d", i)}
	}
	ids := p.Dispatch(context.Background(), specs)
	if len(ids) != n {
		t.Fatalf("Dispatch returned %d ids, want %d", len(ids), n)
	}

	// Non-blocking: Dispatch returned while all workers are still blocked in their
	// model call (none finished).
	for _, s := range p.Poll(ids) {
		if s.State == StateDone || s.State == StateFailed {
			t.Fatalf("worker %s already terminal right after Dispatch — dispatch blocked", s.ID)
		}
	}

	waitFor(t, "all workers concurrent", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return max == n
	})
	close(release)

	waitFor(t, "all workers done", func() bool {
		for _, s := range statesOf(p.Poll(ids)) {
			if s != StateDone {
				return false
			}
		}
		return true
	})
}

func TestDispatchSkipsEmptyTask(t *testing.T) {
	prov := &scriptProvider{gen: func(int, []llm.Message, []llm.ToolSpec, context.Context) (llm.Response, error) {
		return finalText("done"), nil
	}}
	p := NewPool(context.Background(), Config{Provider: prov, Registry: baseRegistry()})
	ids := p.Dispatch(context.Background(), []Spec{{Task: "  "}, {Task: "real"}})
	if len(ids) != 1 {
		t.Fatalf("launched %d workers, want 1 (blank task skipped)", len(ids))
	}
}

// TestWorkerToolSubset checks least privilege: a worker only sees the tools it
// was granted plus its own note tool.
func TestWorkerToolSubset(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	prov := &scriptProvider{gen: func(step int, _ []llm.Message, tools []llm.ToolSpec, _ context.Context) (llm.Response, error) {
		if step == 0 {
			mu.Lock()
			for _, ts := range tools {
				seen = append(seen, ts.Name)
			}
			mu.Unlock()
		}
		return finalText("done"), nil
	}}
	p := NewPool(context.Background(), Config{Provider: prov, Registry: baseRegistry()})
	ids := p.Dispatch(context.Background(), []Spec{{Task: "t", Tools: []string{"read", "nonexistent"}}})
	waitFor(t, "worker done", func() bool { return p.Poll(ids)[0].State == StateDone })

	mu.Lock()
	sort.Strings(seen)
	mu.Unlock()
	want := []string{"note", "read"}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("worker tools = %v, want %v (subset + note, unknown dropped)", seen, want)
	}
}

// TestGatePropagates proves the shared review gate reaches workers so they don't
// re-fight the judge: a gate that denies "bash" blocks the worker's bash call.
func TestGatePropagates(t *testing.T) {
	gate := &recordingGate{deny: map[string]bool{"bash": true}}
	prov := &scriptProvider{gen: func(step int, _ []llm.Message, _ []llm.ToolSpec, _ context.Context) (llm.Response, error) {
		if step == 0 {
			return callTool("c1", "bash", `{"command":"id"}`), nil
		}
		return finalText("done"), nil
	}}
	p := NewPool(context.Background(), Config{Provider: prov, Registry: baseRegistry(), Gate: gate})
	ids := p.Dispatch(context.Background(), []Spec{{Task: "t", Tools: []string{"bash"}}})
	waitFor(t, "worker done", func() bool { return p.Poll(ids)[0].State == StateDone })

	if gate.count() == 0 {
		t.Fatal("gate was never consulted — scope/judge context did not propagate to the worker")
	}
	tr, _ := p.Transcript(ids[0])
	var refused bool
	for _, e := range tr {
		if e.Kind == "error" && e.Tool == "bash" {
			refused = true
		}
	}
	if !refused {
		t.Fatal("expected the denied bash call to be recorded as an error in the worker transcript")
	}
}

// TestWorkerRecordsFactsToBlackboard checks a worker's note-tool facts surface in
// its polled Results and on the shared blackboard.
func TestWorkerRecordsFactsToBlackboard(t *testing.T) {
	prov := &scriptProvider{gen: func(step int, _ []llm.Message, _ []llm.ToolSpec, _ context.Context) (llm.Response, error) {
		switch step {
		case 0:
			return callTool("c1", "note", `{"kind":"flag","value":"CTF{pwned}"}`), nil
		default:
			return finalText("found the flag"), nil
		}
	}}
	board := session.NewBlackboard()
	p := NewPool(context.Background(), Config{Provider: prov, Registry: baseRegistry(), Board: board})
	ids := p.Dispatch(context.Background(), []Spec{{Task: "find flag"}})
	waitFor(t, "worker done", func() bool { return p.Poll(ids)[0].State == StateDone })

	st := p.Poll(ids)[0]
	if len(st.Results.Flags) != 1 || st.Results.Flags[0] != "CTF{pwned}" {
		t.Fatalf("results flags = %v, want [CTF{pwned}]", st.Results.Flags)
	}
	if st.Results.Summary != "found the flag" {
		t.Fatalf("summary = %q, want the final reply text", st.Results.Summary)
	}
	if len(board.State().Flags) != 1 {
		t.Fatalf("blackboard flags = %d, want 1", len(board.State().Flags))
	}
}

// TestWorkerBudgetReaped proves a stuck/looping worker is reaped by its turn
// budget and reported as done-by-budget, not failed.
func TestWorkerBudgetReaped(t *testing.T) {
	prov := &scriptProvider{gen: func(int, []llm.Message, []llm.ToolSpec, context.Context) (llm.Response, error) {
		return callTool("c", "read", `{}`), nil // never finishes on its own
	}}
	p := NewPool(context.Background(), Config{Provider: prov, Registry: baseRegistry()})
	ids := p.Dispatch(context.Background(), []Spec{{
		Task: "loop", Tools: []string{"read"}, Budget: agent.Budget{Turns: 3},
	}})
	waitFor(t, "worker reaped", func() bool { return p.Poll(ids)[0].State == StateDone })

	st := p.Poll(ids)[0]
	if st.StopReason != "budget:turns" {
		t.Fatalf("stop reason = %q, want budget:turns", st.StopReason)
	}
}

// TestCancelStopsWorker proves cancel reaps a running worker and reclaims it.
func TestCancelStopsWorker(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	prov := &scriptProvider{gen: func(step int, _ []llm.Message, _ []llm.ToolSpec, ctx context.Context) (llm.Response, error) {
		select {
		case <-block:
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		}
		return finalText("done"), nil
	}}
	p := NewPool(context.Background(), Config{Provider: prov, Registry: baseRegistry()})
	ids := p.Dispatch(context.Background(), []Spec{{Task: "t"}})
	waitFor(t, "worker running", func() bool { return p.Poll(ids)[0].State == StateRunning })

	if got := p.Cancel(ids); len(got) != 1 {
		t.Fatalf("Cancel returned %v, want 1 id", got)
	}
	waitFor(t, "worker canceled", func() bool { return p.Poll(ids)[0].State == StateCanceled })

	// Cancelling an already-terminal worker is a no-op.
	if got := p.Cancel(ids); len(got) != 0 {
		t.Fatalf("second Cancel returned %v, want none", got)
	}
}

// TestPerTargetConcurrency proves the per-target cap serializes workers on the
// same scope while a different scope runs in parallel. The provider identifies a
// worker's target from its task text (first char), so the cap is not inferred
// from timing alone.
func TestPerTargetConcurrency(t *testing.T) {
	var mu sync.Mutex
	perTarget := map[byte]int{}
	maxPerTarget := map[byte]int{}
	release := make(chan struct{})

	prov := &scriptProvider{gen: func(step int, transcript []llm.Message, _ []llm.ToolSpec, ctx context.Context) (llm.Response, error) {
		target := taskByte(firstTask(transcript))
		mu.Lock()
		perTarget[target]++
		if perTarget[target] > maxPerTarget[target] {
			maxPerTarget[target] = perTarget[target]
		}
		mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		}
		mu.Lock()
		perTarget[target]--
		mu.Unlock()
		return finalText("done"), nil
	}}

	p := NewPool(context.Background(), Config{
		Provider: prov, Registry: baseRegistry(), Limits: Limits{PerTarget: 1},
	})
	// Two workers on target A (must serialize) and one on target B (runs alongside).
	ids := p.Dispatch(context.Background(), []Spec{
		{Task: "a1", Scope: "A"}, {Task: "a2", Scope: "A"}, {Task: "b1", Scope: "B"},
	})

	// Wait until B is running and exactly one A is running.
	waitFor(t, "one-A-and-B running", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return maxPerTarget['b'] == 1 && perTarget['a'] == 1
	})
	// Give a (wrongly) queued second A a chance to slip its cap.
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	overA := maxPerTarget['a']
	mu.Unlock()
	if overA > 1 {
		t.Fatalf("max concurrent on target A = %d, want 1 (per-target cap)", overA)
	}
	close(release)
	waitFor(t, "all done", func() bool {
		for _, s := range statesOf(p.Poll(ids)) {
			if s != StateDone {
				return false
			}
		}
		return true
	})
}

// TestPollResultsAreBounded proves a worker that produces a large summary and a
// large fact still yields concise, capped results — so polling can never balloon
// the orchestrator's context.
func TestPollResultsAreBounded(t *testing.T) {
	bigSummary := strings.Repeat("s", 10_000)
	bigValue := strings.Repeat("v", 10_000)
	prov := &scriptProvider{gen: func(step int, _ []llm.Message, _ []llm.ToolSpec, _ context.Context) (llm.Response, error) {
		if step == 0 {
			return callTool("c1", "note", fmt.Sprintf(`{"kind":"note","value":%q}`, bigValue)), nil
		}
		return finalText(bigSummary), nil
	}}
	p := NewPool(context.Background(), Config{Provider: prov, Registry: baseRegistry()})
	ids := p.Dispatch(context.Background(), []Spec{{Task: "produce a lot"}})
	waitFor(t, "worker done", func() bool { return p.Poll(ids)[0].State == StateDone })

	st := p.Poll(ids)[0]
	if len(st.Results.Summary) > resultSummaryCap+16 {
		t.Fatalf("summary len = %d, want <= ~%d (bounded)", len(st.Results.Summary), resultSummaryCap)
	}
	if len(st.Results.Notes) != 1 || len(st.Results.Notes[0]) > resultValueCap+16 {
		t.Fatalf("note len = %d, want <= ~%d (bounded)", len(st.Results.Notes[0]), resultValueCap)
	}
}

// TestWorkerDefaultBudgetApplied proves a dispatch that omits a budget still gets
// one, so an otherwise-unbounded worker is reaped rather than looping to maxSteps.
func TestWorkerDefaultBudgetApplied(t *testing.T) {
	orig := defaultBudget
	defaultBudget = agent.Budget{Turns: 3}
	defer func() { defaultBudget = orig }()

	prov := &scriptProvider{gen: func(int, []llm.Message, []llm.ToolSpec, context.Context) (llm.Response, error) {
		return callTool("c", "read", `{}`), nil // never finishes on its own
	}}
	p := NewPool(context.Background(), Config{Provider: prov, Registry: baseRegistry()})
	ids := p.Dispatch(context.Background(), []Spec{{Task: "loop", Tools: []string{"read"}}}) // no budget set
	waitFor(t, "worker reaped by default budget", func() bool { return p.Poll(ids)[0].State == StateDone })

	st := p.Poll(ids)[0]
	if st.StopReason != "budget:turns" {
		t.Fatalf("stop reason = %q, want budget:turns (default budget)", st.StopReason)
	}
	if st.Budget.Turns != 3 {
		t.Fatalf("budget turns = %d, want the defaulted 3", st.Budget.Turns)
	}
}

// TestPoolPrunesTerminalWorkers proves the pool bounds its retained workers,
// dropping the oldest terminal ones so a long session can't grow without bound.
func TestPoolPrunesTerminalWorkers(t *testing.T) {
	orig := maxRetainedWorkers
	maxRetainedWorkers = 3
	defer func() { maxRetainedWorkers = orig }()

	p := newFinishingPool()
	for i := 0; i < 5; i++ {
		ids := p.Dispatch(context.Background(), []Spec{{Task: fmt.Sprintf("t%d", i)}})
		waitFor(t, "worker done", func() bool { return p.Poll(ids)[0].State == StateDone })
	}
	if got := len(p.Snapshot()); got > 3 {
		t.Fatalf("retained %d workers, want <= 3 (oldest terminal pruned)", got)
	}
}

// panicTool always panics, to prove a worker fault is contained instead of
// crashing the whole process.
type panicTool struct{}

func (panicTool) Name() string            { return "boom" }
func (panicTool) Description() string     { return "panics" }
func (panicTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (panicTool) Execute(context.Context, json.RawMessage) (string, error) {
	panic("kaboom")
}

// TestWorkerPanicIsContained proves a panic in a worker's tool is recovered and
// reported as a failed worker, not propagated to crash the process.
func TestWorkerPanicIsContained(t *testing.T) {
	reg := tool.NewRegistry()
	reg.MustRegister(panicTool{})
	prov := &scriptProvider{gen: func(step int, _ []llm.Message, _ []llm.ToolSpec, _ context.Context) (llm.Response, error) {
		if step == 0 {
			return callTool("c1", "boom", `{}`), nil
		}
		return finalText("done"), nil
	}}
	p := NewPool(context.Background(), Config{Provider: prov, Registry: reg})
	ids := p.Dispatch(context.Background(), []Spec{{Task: "t", Tools: []string{"boom"}}})
	waitFor(t, "worker failed by panic", func() bool { return p.Poll(ids)[0].State == StateFailed })

	if st := p.Poll(ids)[0]; st.StopReason != "panic" {
		t.Fatalf("stop reason = %q, want panic", st.StopReason)
	}
}

// countingProvider records how many times it generated, to prove which backend a
// worker ran on.
type countingProvider struct {
	mu    sync.Mutex
	calls int
}

func (*countingProvider) Name() string { return "counting" }
func (c *countingProvider) Generate(context.Context, []llm.Message, []llm.ToolSpec) (llm.Response, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return finalText("done"), nil
}
func (c *countingProvider) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// TestPoolSetProviderSwapsBackend proves a mid-session provider swap (e.g. a
// /login upgrade from the offline stub) reaches workers dispatched afterward.
func TestPoolSetProviderSwapsBackend(t *testing.T) {
	old := &countingProvider{}
	p := NewPool(context.Background(), Config{Provider: old, Registry: baseRegistry()})
	neu := &countingProvider{}
	p.SetProvider(neu)

	ids := p.Dispatch(context.Background(), []Spec{{Task: "t"}})
	waitFor(t, "worker done", func() bool { return p.Poll(ids)[0].State == StateDone })

	if neu.count() == 0 {
		t.Fatal("swapped-in provider was not used")
	}
	if old.count() != 0 {
		t.Fatalf("old provider used %d times after swap, want 0", old.count())
	}
}

func taskByte(task string) byte {
	task = strings.TrimSpace(task)
	if task == "" {
		return '?'
	}
	return task[0]
}

// recordingGate is a test gate that counts reviews and denies named tools.
type recordingGate struct {
	mu       sync.Mutex
	reviewed int
	deny     map[string]bool
}

func (g *recordingGate) Review(_ context.Context, call llm.ToolCall) agent.ReviewResult {
	g.mu.Lock()
	g.reviewed++
	deny := g.deny[call.Name]
	g.mu.Unlock()
	if deny {
		return agent.ReviewResult{Allowed: false, Source: "test", Reason: "denied by test gate"}
	}
	return agent.ReviewResult{Allowed: true, Source: "test"}
}

func (g *recordingGate) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reviewed
}
