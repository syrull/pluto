package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
	"github.com/syrull/pluto/internal/tools"
)

// bashRegistry builds a registry with only the bash tool registered.
func bashRegistry(t *testing.T) *tool.Registry {
	t.Helper()
	reg := tool.NewRegistry()
	if err := reg.Register(tools.Bash{}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// fakeApprover returns a canned decision and records what it was asked.
type fakeApprover struct {
	dec    ApprovalDecision
	err    error
	calls  int
	gotRR  ReviewResult
	gotCmd string
}

func (f *fakeApprover) Approve(_ context.Context, call llm.ToolCall, rr ReviewResult) (ApprovalDecision, error) {
	f.calls++
	f.gotRR = rr
	f.gotCmd = call.Name
	return f.dec, f.err
}

// approvalGate returns a fixed review and records any allowlist additions, so a
// test can assert both the approval flow and the "allow this pattern" write-back.
type approvalGate struct {
	res     ReviewResult
	allowed []string
}

func (g *approvalGate) Review(context.Context, llm.ToolCall) ReviewResult { return g.res }
func (g *approvalGate) Allow(pattern string)                              { g.allowed = append(g.allowed, pattern) }

func needsApproval(fallbackAllowed bool, pattern string) ReviewResult {
	return ReviewResult{Allowed: fallbackAllowed, NeedsApproval: true, Source: "judge-error", Pattern: pattern}
}

func runWithApproval(t *testing.T, gate Gate, ap Approver) ([]Event, *Agent) {
	t.Helper()
	reg := bashRegistry(t)
	a := New(&bashOnceProvider{command: "echo hi"}, reg, "", WithGate(gate), WithApprover(ap))
	var evs []Event
	if _, err := a.Run(context.Background(), "go", nil, func(e Event) { evs = append(evs, e) }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return evs, a
}

func TestApprovalYesRunsCommand(t *testing.T) {
	gate := &approvalGate{res: needsApproval(false, "echo hi")}
	ap := &fakeApprover{dec: ApprovalDecision{Choice: ApprovalYes}}

	evs, _ := runWithApproval(t, gate, ap)

	if ap.calls != 1 {
		t.Fatalf("approver consulted %d times, want 1", ap.calls)
	}
	if got := kinds(evs); got != "tool_review,tool_call,tool_result,text" {
		t.Fatalf("event kinds = %q, want tool_review,tool_call,tool_result,text", got)
	}
	for _, e := range evs {
		if e.Kind == "tool_review" && !strings.Contains(e.Text, "approved") {
			t.Fatalf("review line should reflect approval, got %q", e.Text)
		}
	}
	if len(gate.allowed) != 0 {
		t.Fatalf("plain yes must not add an allowlist entry, got %v", gate.allowed)
	}
}

func TestApprovalNoBlocksCommand(t *testing.T) {
	gate := &approvalGate{res: needsApproval(true, "echo hi")} // fallback would allow
	ap := &fakeApprover{dec: ApprovalDecision{Choice: ApprovalNo}}

	evs, a := runWithApproval(t, gate, ap)

	if got := kinds(evs); got != "tool_review,tool_call,error,text" {
		t.Fatalf("event kinds = %q, want tool_review,tool_call,error,text", got)
	}
	for _, e := range evs {
		if e.Kind == "tool_result" {
			t.Fatal("a denied command must not produce a tool_result")
		}
	}
	last := a.transcript[len(a.transcript)-2] // tool refusal precedes final model text
	if last.Role != llm.RoleTool || !strings.Contains(last.Content, "denied") {
		t.Fatalf("expected a RoleTool refusal citing denial, got %+v", last)
	}
}

func TestApprovalPatternRecordsAllowlist(t *testing.T) {
	gate := &approvalGate{res: needsApproval(false, "echo hi")}
	ap := &fakeApprover{dec: ApprovalDecision{Choice: ApprovalPattern, Pattern: "echo hi"}}

	evs, _ := runWithApproval(t, gate, ap)

	if got := kinds(evs); got != "tool_review,tool_call,tool_result,text" {
		t.Fatalf("event kinds = %q, want tool_review,tool_call,tool_result,text", got)
	}
	if len(gate.allowed) != 1 || gate.allowed[0] != "echo hi" {
		t.Fatalf("allow-pattern should record the pattern, got %v", gate.allowed)
	}
}

func TestApprovalFallbackWithoutApprover(t *testing.T) {
	// No approver: a fail-open fallback runs, a fail-closed fallback blocks.
	openGate := fixedGate{needsApproval(true, "echo hi")}
	reg := bashRegistry(t)
	a := New(&bashOnceProvider{command: "echo hi"}, reg, "", WithGate(openGate))
	var evs []Event
	if _, err := a.Run(context.Background(), "go", nil, func(e Event) { evs = append(evs, e) }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := kinds(evs); got != "tool_review,tool_call,tool_result,text" {
		t.Fatalf("fail-open fallback kinds = %q, want it to run", got)
	}

	closedGate := fixedGate{needsApproval(false, "echo hi")}
	reg2 := bashRegistry(t)
	a2 := New(&bashOnceProvider{command: "echo hi"}, reg2, "", WithGate(closedGate))
	var evs2 []Event
	if _, err := a2.Run(context.Background(), "go", nil, func(e Event) { evs2 = append(evs2, e) }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := kinds(evs2); got != "tool_review,tool_call,error,text" {
		t.Fatalf("fail-closed fallback kinds = %q, want it to block", got)
	}
}

func TestApproverNotConsultedForDecidedReviews(t *testing.T) {
	gate := fixedGate{ReviewResult{Allowed: true, Source: "judge"}}
	ap := &fakeApprover{dec: ApprovalDecision{Choice: ApprovalNo}}

	runWithApproval(t, gate, ap)

	if ap.calls != 0 {
		t.Fatalf("approver must not run for a review that is already decided, calls = %d", ap.calls)
	}
}

func TestApprovalEscalationAndDecisionLogged(t *testing.T) {
	read := capturePolicyDebug(t)
	gate := &approvalGate{res: needsApproval(false, "echo hi")}
	ap := &fakeApprover{dec: ApprovalDecision{Choice: ApprovalPattern, Pattern: "echo hi"}}

	runWithApproval(t, gate, ap)

	out := read()
	if !strings.Contains(out, "judge error escalated to human approval") {
		t.Fatalf("escalation not logged:\n%s", out)
	}
	if !strings.Contains(out, "command approved by user") || !strings.Contains(out, "scope=pattern") {
		t.Fatalf("approval decision not logged:\n%s", out)
	}
	if !strings.Contains(out, "approval wait") {
		t.Fatalf("approval latency timer not logged:\n%s", out)
	}
}

func TestApprovalFallbackLogged(t *testing.T) {
	read := capturePolicyDebug(t)
	closedGate := fixedGate{needsApproval(false, "echo hi")}
	reg := bashRegistry(t)
	a := New(&bashOnceProvider{command: "echo hi"}, reg, "", WithGate(closedGate))
	if _, err := a.Run(context.Background(), "go", nil, func(Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := read()
	if !strings.Contains(out, "judge error fallback (no approver)") {
		t.Fatalf("non-interactive fallback not logged:\n%s", out)
	}
}

// capturePolicyDebug enables the debug logger scoped to the "policy" component.
func capturePolicyDebug(t *testing.T) func() string {
	t.Helper()
	_ = debug.Close()
	path := filepath.Join(t.TempDir(), "pluto-debug.log")
	t.Setenv("PLUTO_DEBUG", "1")
	t.Setenv("PLUTO_DEBUG_FILE", path)
	t.Setenv("PLUTO_DEBUG_LEVEL", "debug")
	t.Setenv("PLUTO_DEBUG_COMPONENTS", "policy")
	t.Setenv("PLUTO_DEBUG_FRAMES", "")
	if _, err := debug.Init(); err != nil {
		t.Fatalf("debug.Init: %v", err)
	}
	t.Cleanup(func() { _ = debug.Close() })
	return func() string {
		_ = debug.Close()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return string(data)
	}
}
