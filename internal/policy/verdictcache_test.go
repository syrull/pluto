package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/syrull/pluto/internal/judge"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/workdir"
)

// bashCallAnnotated builds a bash tool call with explicit intent/why so tests can
// prove those model-supplied fields never affect the cache key.
func bashCallAnnotated(cmd, intent, why string) llm.ToolCall {
	args, _ := json.Marshal(map[string]string{"command": cmd, "intent": intent, "why": why})
	return llm.ToolCall{Name: "bash", Args: args}
}

// countingJudge records how many times it was consulted and returns a canned verdict.
type countingJudge struct {
	verdict judge.Verdict
	calls   int
}

func (c *countingJudge) Assess(context.Context, judge.Request) (judge.Verdict, error) {
	c.calls++
	return c.verdict, nil
}

func TestJudgeVerdictCache(t *testing.T) {
	const cmd = "go run ./cmd/tool --flag"

	cases := []struct {
		name string
		run  func(t *testing.T, g *ReviewGate, j *countingJudge)
	}{
		{
			name: "repeat identical hits cache and skips judge",
			run: func(t *testing.T, g *ReviewGate, j *countingJudge) {
				first := g.Review(context.Background(), bashCall(cmd))
				if !first.Allowed || first.Source != "judge" {
					t.Fatalf("first review = %+v, want allowed judge", first)
				}
				second := g.Review(context.Background(), bashCall(cmd))
				if !second.Allowed || second.Source != "judge" {
					t.Fatalf("second review = %+v, want allowed judge", second)
				}
				if j.calls != 1 {
					t.Fatalf("judge called %d times, want 1 (second served from cache)", j.calls)
				}
			},
		},
		{
			name: "first call is a miss that consults the judge",
			run: func(t *testing.T, g *ReviewGate, j *countingJudge) {
				g.Review(context.Background(), bashCall(cmd))
				if j.calls != 1 {
					t.Fatalf("judge called %d times on first review, want 1", j.calls)
				}
			},
		},
		{
			name: "block verdicts are cached and keep their reason",
			run: func(t *testing.T, g *ReviewGate, j *countingJudge) {
				j.verdict = judge.Verdict{Decision: judge.DecisionBlock, Risk: "high", Reason: "nope"}
				first := g.Review(context.Background(), bashCall(cmd))
				second := g.Review(context.Background(), bashCall(cmd))
				if first.Allowed || second.Allowed {
					t.Fatalf("block should not be allowed: first=%+v second=%+v", first, second)
				}
				if second.Reason != "nope" || second.Risk != "high" {
					t.Fatalf("cached block lost its detail: %+v", second)
				}
				if j.calls != 1 {
					t.Fatalf("judge called %d times, want 1 (block cached)", j.calls)
				}
			},
		},
		{
			name: "different cwd is a different cache entry",
			run: func(t *testing.T, g *ReviewGate, j *countingJudge) {
				a := workdir.With(context.Background(), "/tmp/pluto-agent-a")
				b := workdir.With(context.Background(), "/tmp/pluto-agent-b")
				g.Review(a, bashCall(cmd))
				g.Review(b, bashCall(cmd))
				if j.calls != 2 {
					t.Fatalf("judge called %d times, want 2 (distinct cwd ⇒ distinct entry)", j.calls)
				}
			},
		},
		{
			name: "changed intent/why reuses the same entry",
			run: func(t *testing.T, g *ReviewGate, j *countingJudge) {
				g.Review(context.Background(), bashCallAnnotated(cmd, "intent-one", "why-one"))
				g.Review(context.Background(), bashCallAnnotated(cmd, "intent-two", "why-two"))
				if j.calls != 1 {
					t.Fatalf("judge called %d times, want 1 (intent/why must not bust cache)", j.calls)
				}
			},
		},
		{
			name: "cosmetic whitespace differences share a hit",
			run: func(t *testing.T, g *ReviewGate, j *countingJudge) {
				g.Review(context.Background(), bashCall("go   test    ./..."))
				g.Review(context.Background(), bashCall("go test ./..."))
				if j.calls != 1 {
					t.Fatalf("judge called %d times, want 1 (whitespace-normalized key)", j.calls)
				}
			},
		},
		{
			name: "LRU evicts under pressure",
			run: func(t *testing.T, g *ReviewGate, j *countingJudge) {
				g.cache = newVerdictCache(2)
				g.Review(context.Background(), bashCall("go run a")) // miss (calls=1)
				g.Review(context.Background(), bashCall("go run b")) // miss (calls=2)
				g.Review(context.Background(), bashCall("go run a")) // hit, promotes a
				g.Review(context.Background(), bashCall("go run c")) // miss (calls=3), evicts b
				g.Review(context.Background(), bashCall("go run a")) // hit (a still cached)
				if got := j.calls; got != 3 {
					t.Fatalf("judge called %d times before re-miss, want 3", got)
				}
				g.Review(context.Background(), bashCall("go run b")) // b was evicted ⇒ miss (calls=4)
				if j.calls != 4 {
					t.Fatalf("judge called %d times, want 4 (evicted entry re-judged)", j.calls)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := &countingJudge{verdict: judge.Verdict{Decision: judge.DecisionAllow, Risk: "low"}}
			g := newGate(t, j, func(c *Config) { c.FastPath = false })
			tc.run(t, g, j)
		})
	}
}

func TestJudgeCacheNeverBypassesGuard(t *testing.T) {
	// Prime the cache with an allow for a benign command, then confirm guard still
	// fires on every catastrophic command (it never reaches the cache lookup).
	j := &countingJudge{verdict: judge.Verdict{Decision: judge.DecisionAllow, Risk: "low"}}
	g := newGate(t, j, func(c *Config) { c.FastPath = false })
	g.Review(context.Background(), bashCall("go build ./..."))

	for i := 0; i < 2; i++ {
		rr := g.Review(context.Background(), bashCall("rm -rf /"))
		if rr.Allowed || rr.Source != "guard" {
			t.Fatalf("guard must block rm -rf / every call, got %+v", rr)
		}
	}
	if j.calls != 1 {
		t.Fatalf("judge called %d times, want 1 (guard blocks never consult judge)", j.calls)
	}
}

// TestJudgeCacheConcurrentReview hammers a single shared gate from many
// goroutines the way parallel workspace agents do, with a small cache so hits,
// puts, and evictions all race. It asserts nothing beyond correctness; the -race
// detector is the real subject under test.
func TestJudgeCacheConcurrentReview(t *testing.T) {
	g := newGate(t, judge.Fake{Verdict: judge.Verdict{Decision: judge.DecisionAllow, Risk: "low"}}, func(c *Config) { c.FastPath = false })
	g.cache = newVerdictCache(4) // tiny cap ⇒ constant eviction churn

	const workers, iters = 16, 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				// Overlapping keys across workers force cache hits; the rotating
				// suffix and per-worker cwd force puts and evictions.
				ctx := workdir.With(context.Background(), fmt.Sprintf("/tmp/agent-%d", w%3))
				cmd := fmt.Sprintf("go run ./cmd/tool --n %d", i%8)
				if rr := g.Review(ctx, bashCall(cmd)); !rr.Allowed || rr.Source != "judge" {
					t.Errorf("concurrent review = %+v, want allowed judge", rr)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}

func TestJudgeCacheHitMissLogged(t *testing.T) {
	read := capturePolicyDebug(t)
	j := &countingJudge{verdict: judge.Verdict{Decision: judge.DecisionAllow, Risk: "low"}}
	g := newGate(t, j, func(c *Config) { c.FastPath = false })

	g.Review(context.Background(), bashCall("go run ./x")) // miss
	g.Review(context.Background(), bashCall("go run ./x")) // hit

	out := read()
	if !strings.Contains(out, "judge cache miss") {
		t.Fatalf("expected a judge cache miss event:\n%s", out)
	}
	if !strings.Contains(out, "judge cache hit") {
		t.Fatalf("expected a judge cache hit event:\n%s", out)
	}
}
