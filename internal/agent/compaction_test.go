package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syrull/pluto/internal/llm"
)

// fakeSummarizer records its calls and returns summary (or err), so tests can
// drive compaction deterministically without a real provider.
func fakeSummarizer(summary string, err error) (func(context.Context, string) (string, error), *[]string) {
	var prompts []string
	fn := func(_ context.Context, prompt string) (string, error) {
		prompts = append(prompts, prompt)
		return summary, err
	}
	return fn, &prompts
}

// countMemory reports how many compacted memory turns the transcript holds.
func countMemory(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == llm.RoleUser && strings.HasPrefix(m.Content, memoryPrefix) {
			n++
		}
	}
	return n
}

func overBudgetTranscript(big string) []llm.Message {
	return []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "u1 " + big},
		{Role: llm.RoleModel, Content: "m1 " + big, ToolCalls: []llm.ToolCall{{ID: "t1", Name: "read", Args: []byte(`{}`)}}},
		{Role: llm.RoleTool, ToolCallID: "t1", ToolName: "read", Content: "r1 " + big},
		{Role: llm.RoleUser, Content: "u2 " + big},
		{Role: llm.RoleModel, Content: "m2 " + big},
		{Role: llm.RoleUser, Content: "u3 " + big},
	}
}

func TestCompactionSummarizesEvictedExchanges(t *testing.T) {
	big := strings.Repeat("x", 800) // ~204 est. tokens per message
	fn, prompts := fakeSummarizer("SUMMARY: read a file, decided X", nil)
	a := &Agent{provider: llm.Stub{}, contextLimit: 700, summarize: fn}
	a.transcript = overBudgetTranscript(big)

	a.compactTranscript(context.Background())

	if len(*prompts) != 1 {
		t.Fatalf("summarizer called %d times, want 1", len(*prompts))
	}
	// The dropped exchange (its user turn, tool call, and result) must appear in
	// the summarizer input so its content is preserved, not silently lost.
	if p := (*prompts)[0]; !strings.Contains(p, "u1 ") || !strings.Contains(p, "read") || !strings.Contains(p, "r1 ") {
		t.Fatalf("summarizer prompt missing evicted content:\n%s", truncate(p, 300))
	}

	got := a.transcript
	if got[0].Role != llm.RoleSystem {
		t.Fatalf("system message dropped: %+v", got[0])
	}
	if got[1].Role != llm.RoleUser || !strings.HasPrefix(got[1].Content, memoryPrefix) {
		t.Fatalf("expected a memory turn at index 1, got %+v", got[1])
	}
	if !strings.Contains(got[1].Content, "SUMMARY: read a file") {
		t.Fatalf("memory turn missing summary text: %q", got[1].Content)
	}
	if countMemory(got) != 1 {
		t.Fatalf("expected exactly one memory turn, got %d", countMemory(got))
	}
	// The raw evicted exchange is gone; the retained tail survives verbatim.
	if findUser(got, "u1 "+big) != -1 {
		t.Fatalf("evicted exchange u1 should be gone, roles=%v", roles(got))
	}
	if findUser(got, "u2 "+big) == -1 || findUser(got, "u3 "+big) == -1 {
		t.Fatalf("retained tail exchanges missing, roles=%v", roles(got))
	}
	if got[len(got)-1].Content != "u3 "+big {
		t.Fatalf("current exchange dropped; last=%q", truncate(got[len(got)-1].Content, 8))
	}
	assertToolPairing(t, got)
}

func TestCompactionFallsBackWhenNoSummarizer(t *testing.T) {
	big := strings.Repeat("x", 800)
	base := overBudgetTranscript(big)

	// No summarizer: compaction must degrade to plain boundary-safe eviction.
	noSum := &Agent{provider: llm.Stub{}, contextLimit: 700}
	noSum.transcript = slices.Clone(base)
	noSum.compactTranscript(context.Background())

	plain := &Agent{provider: llm.Stub{}, contextLimit: 700}
	plain.transcript = slices.Clone(base)
	plain.trimTranscript()

	if !slices.EqualFunc(noSum.transcript, plain.transcript, func(x, y llm.Message) bool {
		return x.Role == y.Role && x.Content == y.Content
	}) {
		t.Fatalf("no-summarizer compaction diverged from plain eviction:\ngot  %v\nwant %v",
			roles(noSum.transcript), roles(plain.transcript))
	}
	if countMemory(noSum.transcript) != 0 {
		t.Fatalf("fallback path must not add a memory turn")
	}
}

func TestCompactionFallsBackWhenSummarizerErrors(t *testing.T) {
	big := strings.Repeat("x", 800)
	fn, _ := fakeSummarizer("", errors.New("offline"))
	a := &Agent{provider: llm.Stub{}, contextLimit: 700, summarize: fn}
	a.transcript = overBudgetTranscript(big)

	a.compactTranscript(context.Background())

	if countMemory(a.transcript) != 0 {
		t.Fatalf("a failed summary must fall back to eviction, not insert a memory turn")
	}
	// Plain eviction keeps a valid, boundary-cut transcript.
	if a.transcript[1].Role != llm.RoleUser || !strings.HasPrefix(a.transcript[1].Content, "u2 ") {
		t.Fatalf("fallback eviction should keep from the u2 boundary, got %+v", a.transcript[1])
	}
	assertToolPairing(t, a.transcript)
}

func TestCompactionEmptySummaryFallsBack(t *testing.T) {
	big := strings.Repeat("x", 800)
	fn, _ := fakeSummarizer("   \n  ", nil) // whitespace-only ⇒ treated as unavailable
	a := &Agent{provider: llm.Stub{}, contextLimit: 700, summarize: fn}
	a.transcript = overBudgetTranscript(big)

	a.compactTranscript(context.Background())

	if countMemory(a.transcript) != 0 {
		t.Fatalf("an empty summary must fall back to eviction")
	}
}

func TestCompactionNoOpWithinBudget(t *testing.T) {
	fn, prompts := fakeSummarizer("unused", nil)
	a := &Agent{provider: llm.Stub{}, contextLimit: 100_000, summarize: fn}
	a.transcript = []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleModel, Content: "hi"},
	}
	before := slices.Clone(a.transcript)

	a.compactTranscript(context.Background())

	if len(*prompts) != 0 {
		t.Fatalf("summarizer should not run when within budget, calls=%d", len(*prompts))
	}
	if !slices.EqualFunc(a.transcript, before, func(x, y llm.Message) bool { return x.Content == y.Content }) {
		t.Fatalf("transcript changed while within budget: %v", roles(a.transcript))
	}
}

func TestCompactionRepeatsAcrossLongSession(t *testing.T) {
	big := strings.Repeat("x", 800) // ~204 est. tokens per message
	calls := 0
	fn := func(_ context.Context, _ string) (string, error) {
		calls++
		return fmt.Sprintf("SUMMARY #%d of earlier work", calls), nil
	}
	// Budget fits at most the current exchange, forcing compaction every turn.
	a := &Agent{provider: llm.Stub{}, contextLimit: 400, summarize: fn}
	a.transcript = []llm.Message{{Role: llm.RoleSystem, Content: "sys"}}

	const turns = 25
	for i := range turns {
		a.appendMessages(
			llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("u%d %s", i, big)},
			llm.Message{Role: llm.RoleModel, Content: fmt.Sprintf("m%d %s", i, big)},
		)
		a.compactTranscript(context.Background())

		// The memory turn is re-compacted, never accumulated: at most one exists,
		// and the transcript stays bounded no matter how long the session runs.
		if n := countMemory(a.transcript); n > 1 {
			t.Fatalf("turn %d: %d memory turns, want at most 1 (re-compaction must fold the old one in)", i, n)
		}
		if len(a.transcript) > 6 {
			t.Fatalf("turn %d: transcript grew to %d messages; compaction is not bounding it", i, len(a.transcript))
		}
		assertToolPairing(t, a.transcript)
	}

	if calls < 2 {
		t.Fatalf("expected repeated compaction over a long session, summarizer ran %d times", calls)
	}
	if countMemory(a.transcript) != 1 {
		t.Fatalf("expected exactly one live memory turn at end, got %d", countMemory(a.transcript))
	}
	// The final memory reflects the latest re-summarization, not the first.
	last := a.transcript[1]
	if !strings.HasPrefix(last.Content, memoryPrefix) || !strings.Contains(last.Content, fmt.Sprintf("SUMMARY #%d", calls)) {
		t.Fatalf("final memory turn is stale: %q", truncate(last.Content, 60))
	}
	// The most recent user exchange is always retained verbatim.
	if got := a.transcript[len(a.transcript)-2].Content; !strings.HasPrefix(got, fmt.Sprintf("u%d ", turns-1)) {
		t.Fatalf("latest exchange not retained, got %q", truncate(got, 8))
	}
}

// TestCompactionSnapshotNoRace runs compaction (whose summarizer call happens
// off-lock) while Snapshot reads concurrently, as autosave would. Must be
// race-free under `go test -race`.
func TestCompactionSnapshotNoRace(t *testing.T) {
	big := strings.Repeat("x", 800)
	fn := func(context.Context, string) (string, error) {
		time.Sleep(time.Millisecond) // widen the off-lock window
		return "SUMMARY", nil
	}
	a := &Agent{provider: llm.Stub{}, contextLimit: 400, summarize: fn}
	a.transcript = []llm.Message{{Role: llm.RoleSystem, Content: "sys"}}
	for i := range 10 {
		a.appendMessages(
			llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("u%d %s", i, big)},
			llm.Message{Role: llm.RoleModel, Content: fmt.Sprintf("m%d %s", i, big)},
		)
	}

	done := make(chan struct{})
	go func() {
		a.compactTranscript(context.Background())
		close(done)
	}()
	for {
		select {
		case <-done:
			return
		default:
			_ = a.Snapshot()
			_, _, _ = a.ContextUsage()
		}
	}
}

// assertToolPairing verifies every tool_result answers a tool_use that precedes
// it in the same assistant turn, so compaction never orphans a tool result.
func assertToolPairing(t *testing.T, msgs []llm.Message) {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleModel:
			for _, c := range m.ToolCalls {
				seen[c.ID] = true
			}
		case llm.RoleTool:
			if !seen[m.ToolCallID] {
				t.Fatalf("orphaned tool_result %q: no preceding tool_use, roles=%v", m.ToolCallID, roles(msgs))
			}
		}
	}
}
