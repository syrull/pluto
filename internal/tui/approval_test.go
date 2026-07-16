package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
)

func approvalBashCall(cmd string) llm.ToolCall {
	args, _ := json.Marshal(map[string]string{"command": cmd, "intent": "run tests", "why": "verify"})
	return llm.ToolCall{Name: "bash", Args: args}
}

// deliverApproval starts an Approve in a goroutine and returns the request the
// model would receive, plus a channel carrying Approve's eventual result.
type approveResult struct {
	dec agent.ApprovalDecision
	err error
}

func startApprove(t *testing.T, ap *Approver, ctx context.Context, rr agent.ReviewResult) chan approveResult {
	t.Helper()
	done := make(chan approveResult, 1)
	go func() {
		dec, err := ap.Approve(ctx, approvalBashCall("go test ./..."), rr)
		done <- approveResult{dec, err}
	}()
	return done
}

func nextRequest(t *testing.T, ap *Approver) *approvalRequest {
	t.Helper()
	select {
	case req := <-ap.requests:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for an approval request")
		return nil
	}
}

func TestApproveDeliversAndReplies(t *testing.T) {
	ap := NewApprover()
	rr := agent.ReviewResult{NeedsApproval: true, Source: "judge-error", Pattern: "go test"}
	done := startApprove(t, ap, context.Background(), rr)

	req := nextRequest(t, ap)
	if req.rr.Pattern != "go test" {
		t.Fatalf("request pattern = %q, want 'go test'", req.rr.Pattern)
	}
	req.reply <- agent.ApprovalDecision{Choice: agent.ApprovalYes}

	select {
	case res := <-done:
		if res.err != nil || res.dec.Choice != agent.ApprovalYes {
			t.Fatalf("Approve = %+v, %v; want yes, nil", res.dec, res.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Approve did not return after reply")
	}
}

func TestApproveCanceledContext(t *testing.T) {
	ap := NewApprover()
	ctx, cancel := context.WithCancel(context.Background())
	done := startApprove(t, ap, ctx, agent.ReviewResult{NeedsApproval: true})
	// Deliver the request but cancel instead of replying.
	nextRequest(t, ap)
	cancel()
	select {
	case res := <-done:
		if res.err == nil {
			t.Fatal("Approve should return the context error when canceled")
		}
	case <-time.After(time.Second):
		t.Fatal("Approve did not unblock on cancel")
	}
}

func TestAnswerApprovalYes(t *testing.T) {
	m := multiModel(1)
	m.approver = NewApprover()
	reply := make(chan agent.ApprovalDecision, 1)
	m.approval = &approvalRequest{
		call:  approvalBashCall("go test ./..."),
		rr:    agent.ReviewResult{Pattern: "go test"},
		reply: reply,
	}

	cmd := m.answerApproval(agent.ApprovalYes)

	if m.approval != nil {
		t.Fatal("answering should clear the pending approval")
	}
	if cmd == nil {
		t.Fatal("answering should re-arm the approval listener")
	}
	dec := <-reply
	if dec.Choice != agent.ApprovalYes || dec.Pattern != "" {
		t.Fatalf("decision = %+v, want yes with no pattern", dec)
	}
}

func TestAnswerApprovalPatternCarriesPattern(t *testing.T) {
	m := multiModel(1)
	m.approver = NewApprover()
	reply := make(chan agent.ApprovalDecision, 1)
	m.approval = &approvalRequest{
		call:  approvalBashCall("go test ./..."),
		rr:    agent.ReviewResult{Pattern: "go test"},
		reply: reply,
	}

	m.answerApproval(agent.ApprovalPattern)

	dec := <-reply
	if dec.Choice != agent.ApprovalPattern || dec.Pattern != "go test" {
		t.Fatalf("decision = %+v, want allow-pattern carrying 'go test'", dec)
	}
}

func TestApprovalKeysAreCaptured(t *testing.T) {
	for _, tc := range []struct {
		key  rune
		want agent.ApprovalChoice
	}{
		{'y', agent.ApprovalYes},
		{'a', agent.ApprovalPattern},
		{'n', agent.ApprovalNo},
	} {
		m := multiModel(1)
		m.approver = NewApprover()
		reply := make(chan agent.ApprovalDecision, 1)
		m.approval = &approvalRequest{call: approvalBashCall("go test ./..."), rr: agent.ReviewResult{Pattern: "go test"}, reply: reply}

		var tm tea.Model = *m
		tm, _ = tm.Update(tea.KeyPressMsg{Code: tc.key})

		if got := tm.(model).approval; got != nil {
			t.Fatalf("key %q should resolve the approval, still pending", tc.key)
		}
		dec := <-reply
		if dec.Choice != tc.want {
			t.Fatalf("key %q → %v, want %v", tc.key, dec.Choice, tc.want)
		}
	}
}

func TestApprovalEscDenies(t *testing.T) {
	m := multiModel(1)
	m.approver = NewApprover()
	reply := make(chan agent.ApprovalDecision, 1)
	m.approval = &approvalRequest{call: approvalBashCall("go test ./..."), rr: agent.ReviewResult{Pattern: "go test"}, reply: reply}

	var tm tea.Model = *m
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if tm.(model).approval != nil {
		t.Fatal("esc should resolve the approval")
	}
	if dec := <-reply; dec.Choice != agent.ApprovalNo {
		t.Fatalf("esc → %v, want no", dec.Choice)
	}
}

func TestApprovalPromptRenders(t *testing.T) {
	m := multiModel(1)
	m.width, m.height, m.ready = 100, 40, true
	m.approval = &approvalRequest{
		call: approvalBashCall("go test ./..."),
		rr:   agent.ReviewResult{Pattern: "go test", Reason: "judge unavailable"},
	}
	out := m.content()
	for _, want := range []string{"go test ./...", "approve this command", "yes", "allow this pattern", "no", "go test"} {
		if !strings.Contains(out, want) {
			t.Fatalf("approval prompt missing %q:\n%s", want, out)
		}
	}
}

func TestApprovalPromptShownLogged(t *testing.T) {
	read := enableTUILog(t, "info")
	m := multiModel(1)
	m.width, m.height, m.ready = 100, 40, true
	req := &approvalRequest{call: approvalBashCall("go test ./..."), rr: agent.ReviewResult{Pattern: "go test", Source: "judge-error"}}

	var tm tea.Model = *m
	tm, _ = tm.Update(approvalReqMsg{req: req})
	if tm.(model).approval == nil {
		t.Fatal("approval request should be stored for display")
	}

	out := read()
	if !strings.Contains(out, "approval prompt shown") {
		t.Fatalf("prompt-shown not logged:\n%s", out)
	}
}

func TestApprovalDecisionLogged(t *testing.T) {
	read := enableTUILog(t, "info")
	m := multiModel(1)
	m.approver = NewApprover()
	reply := make(chan agent.ApprovalDecision, 1)
	m.approval = &approvalRequest{call: approvalBashCall("go test ./..."), rr: agent.ReviewResult{Pattern: "go test"}, reply: reply}

	m.answerApproval(agent.ApprovalPattern)
	<-reply

	out := read()
	if !strings.Contains(out, "approval decision") || !strings.Contains(out, "choice=allow-pattern") {
		t.Fatalf("approval decision not logged:\n%s", out)
	}
}
