package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tui/widgets"
)

// Approver bridges an agent's blocking approval request (a judge outage) to the
// interactive TUI. The agent goroutine calls Approve, which hands the request to
// the model over a channel and blocks on a reply; the model routes the request to
// the workspace that raised it and prompts the user only while that agent is in
// front, so a background agent's approval never hijacks the one on screen. One
// Approver is shared by every agent; each request carries the id of the agent it
// belongs to (see agentIDKey), so the model can hold one pending approval per
// agent simultaneously.
type Approver struct {
	requests chan *approvalRequest
}

// approvalRequest is one pending human-in-the-loop decision: the id of the agent
// that raised it, the proposed call, the review that flagged it, and the channel
// the model replies on.
type approvalRequest struct {
	id    int
	call  llm.ToolCall
	rr    agent.ReviewResult
	reply chan agent.ApprovalDecision
}

// agentIDKey carries the raising agent's workspace id on the Run context so the
// Approver can tag each request and the TUI can route it to the owning agent.
type agentIDKey struct{}

// withAgentID returns ctx carrying the workspace id id.
func withAgentID(ctx context.Context, id int) context.Context {
	return context.WithValue(ctx, agentIDKey{}, id)
}

// agentIDFrom returns the workspace id carried by ctx, or 0 when none is set (the
// bare/test model, or a single-agent run).
func agentIDFrom(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	if id, ok := ctx.Value(agentIDKey{}).(int); ok {
		return id
	}
	return 0
}

// NewApprover builds an Approver ready to wire into agents (agent.WithApprover)
// and the TUI (tui.New).
func NewApprover() *Approver {
	return &Approver{requests: make(chan *approvalRequest)}
}

// Approve implements agent.Approver, blocking until the user answers or ctx is
// canceled (e.g. the run is interrupted). The request is tagged with the raising
// agent's id (from ctx) so the model prompts only on that agent.
func (a *Approver) Approve(ctx context.Context, call llm.ToolCall, rr agent.ReviewResult) (agent.ApprovalDecision, error) {
	req := &approvalRequest{id: agentIDFrom(ctx), call: call, rr: rr, reply: make(chan agent.ApprovalDecision, 1)}
	select {
	case a.requests <- req:
	case <-ctx.Done():
		return agent.ApprovalDecision{}, ctx.Err()
	}
	select {
	case dec := <-req.reply:
		return dec, nil
	case <-ctx.Done():
		return agent.ApprovalDecision{}, ctx.Err()
	}
}

// approvalReqMsg delivers a pending approval request to the model.
type approvalReqMsg struct{ req *approvalRequest }

// listenApproval waits for the next approval request from the shared Approver. It
// returns nil (no-op) when no approver is wired, so background/headless runs fall
// back to the OnJudgeError policy instead.
func listenApproval(a *Approver) tea.Cmd {
	if a == nil {
		return nil
	}
	return func() tea.Msg {
		return approvalReqMsg{req: <-a.requests}
	}
}

// answerApproval resolves the active agent's pending approval with the user's
// choice and sends it back to that blocked agent. The listener is re-armed when
// each request arrives (see the approvalReqMsg handler), not here, so answering
// one agent never disturbs another agent's still-pending prompt.
func (m *model) answerApproval(choice agent.ApprovalChoice) tea.Cmd {
	req := m.approval
	m.approval = nil
	if req == nil {
		return nil
	}
	dec := agent.ApprovalDecision{Choice: choice}
	if choice == agent.ApprovalPattern {
		dec.Pattern = req.rr.Pattern
	}
	cmd, _, _ := approvalArgs(req.call)
	debug.Info(dbgTUI, "approval decision", "id", req.id, "choice", approvalChoiceName(choice),
		"tool", req.call.Name, "cmd", truncCells(oneLine(cmd), 200), "pattern", truncCells(dec.Pattern, 200))
	req.reply <- dec
	m.notice = approvalNotice(choice, dec.Pattern)
	m.syncViewport()
	return nil
}

// approvalArgs extracts the command, intent, and why from a bash tool call.
func approvalArgs(call llm.ToolCall) (cmd, intent, why string) {
	var a struct {
		Command string `json:"command"`
		Intent  string `json:"intent"`
		Why     string `json:"why"`
	}
	_ = json.Unmarshal(call.Args, &a)
	return a.Command, a.Intent, a.Why
}

// approvalChoiceName renders an approval choice for logging.
func approvalChoiceName(c agent.ApprovalChoice) string {
	switch c {
	case agent.ApprovalYes:
		return "yes"
	case agent.ApprovalPattern:
		return "allow-pattern"
	default:
		return "no"
	}
}

// approvalNotice is the transient status shown after a decision.
func approvalNotice(choice agent.ApprovalChoice, pattern string) string {
	switch choice {
	case agent.ApprovalYes:
		return "✓ approved — running once"
	case agent.ApprovalPattern:
		return fmt.Sprintf("✓ approved — allowing %q this session", pattern)
	default:
		return "✗ blocked command"
	}
}

// renderApprovalPrompt renders the human-in-the-loop approval as an inline box in
// the conversation flow (just above the still-live input, not a full-screen
// takeover) showing the call, the pattern an "allow" would remember, and the
// three keybindings. A bash call shows its command plus intent/why; any other
// tool shows its name and a JSON argument preview.
func renderApprovalPrompt(width int, req *approvalRequest) string {
	inner := width
	if inner < 24 {
		inner = 24
	}
	if inner > 76 {
		inner = 76
	}
	bodyW := inner - 4
	if bodyW < 10 {
		bodyW = 10
	}

	var b strings.Builder
	if req.call.Name == "bash" {
		cmd, intent, why := approvalArgs(req.call)
		b.WriteString(styleReview.Bold(true).Render("⚠ judge unavailable — approve this command?"))
		b.WriteString("\n\n")
		b.WriteString(styleToolName.Render("→ bash"))
		b.WriteString("\n")
		b.WriteString(styleToolArgs.Width(bodyW).Render(widgets.Sanitize(strings.TrimRight(cmd, "\n"))))
		if s := strings.TrimSpace(intent); s != "" {
			b.WriteString("\n")
			b.WriteString(styleHint.Render("intent: ") + styleToolBody.Render(oneLine(s)))
		}
		if s := strings.TrimSpace(why); s != "" {
			b.WriteString("\n")
			b.WriteString(styleHint.Render("why: ") + styleToolBody.Render(oneLine(s)))
		}
	} else {
		b.WriteString(styleReview.Bold(true).Render("⚠ external MCP tool — approve this call?"))
		b.WriteString("\n\n")
		b.WriteString(styleToolName.Render("→ " + req.call.Name))
		if args := strings.TrimSpace(string(req.call.Args)); args != "" && args != "{}" {
			b.WriteString("\n")
			b.WriteString(styleToolArgs.Width(bodyW).Render(widgets.Sanitize(oneLine(args))))
		}
	}
	if p := strings.TrimSpace(req.rr.Pattern); p != "" {
		b.WriteString("\n")
		b.WriteString(styleHint.Render("allow pattern: ") + styleReview.Render(oneLine(p)))
	}
	b.WriteString("\n\n")
	b.WriteString(approvalOptionsLine())

	return styleModalBox.Width(inner).Render(b.String())
}

// approvalOptionsLine renders the three keybinding chips.
func approvalOptionsLine() string {
	sep := styleHint.Render("   ·   ")
	return stylePickSel.Render(" y ") + styleHint.Render(" yes") + sep +
		stylePickSel.Render(" a ") + styleHint.Render(" allow this pattern") + sep +
		stylePickSel.Render(" n ") + styleHint.Render(" no")
}
