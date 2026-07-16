package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tui/widgets"
)

// Approver bridges an agent's blocking judge-error approval request to the
// interactive TUI. The agent goroutine calls Approve, which hands the request to
// the model over a channel and blocks on a reply; the model renders a prompt and
// sends the user's choice back. One Approver is shared by every agent, so
// simultaneous requests queue and are answered one at a time.
type Approver struct {
	requests chan *approvalRequest
}

// approvalRequest is one pending human-in-the-loop decision: the proposed call,
// the review that flagged it, and the channel the model replies on.
type approvalRequest struct {
	call  llm.ToolCall
	rr    agent.ReviewResult
	reply chan agent.ApprovalDecision
}

// NewApprover builds an Approver ready to wire into agents (agent.WithApprover)
// and the TUI (tui.New).
func NewApprover() *Approver {
	return &Approver{requests: make(chan *approvalRequest)}
}

// Approve implements agent.Approver, blocking until the user answers or ctx is
// canceled (e.g. the run is interrupted).
func (a *Approver) Approve(ctx context.Context, call llm.ToolCall, rr agent.ReviewResult) (agent.ApprovalDecision, error) {
	req := &approvalRequest{call: call, rr: rr, reply: make(chan agent.ApprovalDecision, 1)}
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

// answerApproval resolves the pending approval with the user's choice, sends it
// back to the blocked agent, and re-arms the listener for the next request.
func (m *model) answerApproval(choice agent.ApprovalChoice) tea.Cmd {
	req := m.approval
	m.approval = nil
	if req == nil {
		return listenApproval(m.approver)
	}
	dec := agent.ApprovalDecision{Choice: choice}
	if choice == agent.ApprovalPattern {
		dec.Pattern = req.rr.Pattern
	}
	cmd, _, _ := approvalArgs(req.call)
	debug.Info(dbgTUI, "approval decision", "choice", approvalChoiceName(choice),
		"cmd", truncCells(oneLine(cmd), 200), "pattern", truncCells(dec.Pattern, 200))
	req.reply <- dec
	m.notice = approvalNotice(choice, dec.Pattern)
	m.syncViewport()
	return listenApproval(m.approver)
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

// renderApprovalPrompt renders the human-in-the-loop approval as a centered
// overlay showing the command, its intent/why, the pattern an "allow" would
// remember, and the three keybindings.
func renderApprovalPrompt(width, height int, req *approvalRequest) string {
	inner := width - 8
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

	cmd, intent, why := approvalArgs(req.call)

	var b strings.Builder
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
	if p := strings.TrimSpace(req.rr.Pattern); p != "" {
		b.WriteString("\n")
		b.WriteString(styleHint.Render("allow pattern: ") + styleReview.Render(oneLine(p)))
	}
	b.WriteString("\n\n")
	b.WriteString(approvalOptionsLine())

	box := styleModalBox.Width(inner).Render(b.String())
	if width > 0 && height > 0 {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}

// approvalOptionsLine renders the three keybinding chips.
func approvalOptionsLine() string {
	sep := styleHint.Render("   ·   ")
	return stylePickSel.Render(" y ") + styleHint.Render(" yes") + sep +
		stylePickSel.Render(" a ") + styleHint.Render(" allow this pattern") + sep +
		stylePickSel.Render(" n ") + styleHint.Render(" no")
}
