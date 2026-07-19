package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

// budgetLoopProvider always asks for one more tool call, so the run only ever
// stops because a budget reaps it. Each turn reports a fixed token cost and can
// pause so a wall-clock budget has time to trip before maxSteps is reached.
type budgetLoopProvider struct {
	turns         int
	tokensPerTurn int
	sleep         time.Duration
}

func (p *budgetLoopProvider) Name() string       { return "budget-loop" }
func (p *budgetLoopProvider) ContextWindow() int { return 200_000 }

func (p *budgetLoopProvider) Generate(ctx context.Context, _ []llm.Message, _ []llm.ToolSpec) (llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	if p.sleep > 0 {
		select {
		case <-time.After(p.sleep):
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		}
	}
	p.turns++
	return llm.Response{
		ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("c%d", p.turns), Name: "noop", Args: json.RawMessage(`{}`)}},
		// Real providers always report the prompt size; the budget sums input+output.
		Usage: llm.Usage{InputTokens: p.tokensPerTurn},
	}, nil
}

func newBudgetAgent(t *testing.T, p llm.Provider, b Budget) *Agent {
	t.Helper()
	reg := tool.NewRegistry()
	reg.MustRegister(raceNoopTool{})
	return New(p, reg, "sys", WithBudget(b))
}

func TestBudgetTurnsStopsRun(t *testing.T) {
	p := &budgetLoopProvider{}
	a := newBudgetAgent(t, p, Budget{Turns: 3})

	_, err := a.Run(context.Background(), "go", nil, func(Event) {})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("Run err = %v, want ErrBudgetExhausted", err)
	}
	if p.turns != 3 {
		t.Fatalf("provider turns = %d, want 3 (turn budget)", p.turns)
	}
}

func TestBudgetTokensStopsRun(t *testing.T) {
	// 40 tokens/turn with a 100-token budget: turns 0 and 1 run (spend 0, then
	// 40, then 80), turn 2 sees 80 < 100 and runs, turn 3 sees 120 >= 100 → stop.
	p := &budgetLoopProvider{tokensPerTurn: 40}
	a := newBudgetAgent(t, p, Budget{Tokens: 100})

	_, err := a.Run(context.Background(), "go", nil, func(Event) {})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("Run err = %v, want ErrBudgetExhausted", err)
	}
	if p.turns != 3 {
		t.Fatalf("provider turns = %d, want 3 (token budget)", p.turns)
	}
}

func TestBudgetWallStopsRun(t *testing.T) {
	p := &budgetLoopProvider{sleep: 5 * time.Millisecond}
	a := newBudgetAgent(t, p, Budget{Wall: 20 * time.Millisecond})

	start := time.Now()
	_, err := a.Run(context.Background(), "go", nil, func(Event) {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("run took %v, wall budget should have reaped it far sooner", elapsed)
	}
}

func TestZeroBudgetIsUnbounded(t *testing.T) {
	// A finite provider (finishes after two calls) must complete normally with no
	// budget set — the default top-level agent must be unaffected.
	p := &raceLoopProvider{maxCalls: 2}
	reg := tool.NewRegistry()
	reg.MustRegister(raceNoopTool{})
	a := New(p, reg, "sys")

	out, err := a.Run(context.Background(), "go", nil, func(Event) {})
	if err != nil {
		t.Fatalf("unbounded Run err = %v, want nil", err)
	}
	if out != "done" {
		t.Fatalf("unbounded Run out = %q, want \"done\"", out)
	}
}
