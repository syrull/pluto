package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/tools"
	"github.com/syrull/pluto/internal/workdir"
)

// bashInlineMsg carries the result of an inline `!` shell command back to the
// UI once it exits. epoch fences a stale result from a canceled/superseded run.
type bashInlineMsg struct {
	epoch   int
	command string
	output  string
}

// inlineBudget caps the copy of inline output folded into the model's context
// so a huge result can't blow the token budget; the on-screen copy stays full.
const inlineBudget = 8 * 1024

// handleInline runs an inline `!` shell command. A lone `!` is a hint no-op, and
// only one inline command runs at a time.
func (m *model) handleInline(in string) tea.Cmd {
	command := strings.TrimSpace(strings.TrimPrefix(in, "!"))
	if command == "" {
		m.notice = "type a shell command after ! — e.g. !git status"
		return nil
	}
	if m.inlineCancel != nil {
		m.notice = "✗ a command is already running — esc to cancel it"
		return nil
	}
	m.input.Reset()
	m.pushText(m.renderBashLine(command))
	return m.runInline(command)
}

// runInline launches command in a subshell as a background command, returning
// its full output as a bashInlineMsg.
func (m *model) runInline(command string) tea.Cmd {
	m.inlineEpoch++
	epoch := m.inlineEpoch
	ctx, cancel := context.WithCancel(workdir.With(context.Background(), m.activeCwd()))
	m.inlineCancel = cancel
	m.notice = "running: " + oneLine(command) + " — esc to cancel"
	return func() tea.Msg {
		defer cancel()
		return bashInlineMsg{epoch: epoch, command: command, output: tools.RunInline(ctx, command)}
	}
}

// applyInlineResult renders an inline command's full output and folds the
// command + output into the conversation so the agent sees it next turn.
func (m *model) applyInlineResult(msg bashInlineMsg) {
	m.inlineCancel = nil
	m.notice = ""
	m.appendInlineResult(msg.command, msg.output)
	ctx := inlineContext(msg.command, msg.output)
	// A turn in flight owns the transcript, so queue via the mutex-guarded
	// steering path; otherwise append directly and persist.
	if m.busy {
		m.agent.Steer(ctx)
	} else {
		m.agent.AddContext(ctx)
		m.autosave()
	}
}

// renderBashLine renders a submitted inline command with a red `$` marker
// matching the input prompt, distinct from a normal user line.
func (m *model) renderBashLine(command string) string {
	prefix := styleBashPrompt.Render("$ ")
	return wrapBody(prefix, command, styleToolArgs, m.contentWidth())
}

// appendInlineResult renders an inline command's output like a bash tool result,
// retaining the full, untruncated text behind a [Show] modal.
func (m *model) appendInlineResult(command, output string) {
	text := renderToolResult(m.contentWidth(), "bash", output)
	id := 0
	if full, ok := resultTruncated("bash", output); ok {
		m.outputs = append(m.outputs, toolOutput{title: "! " + oneLine(command), full: full})
		id = len(m.outputs)
		text += "\n  " + m.showAffordance()
	}
	m.lines = append(m.lines, entry{text: text, outputID: id})
}

// inlineContext builds the conversation-history copy of an inline command and
// its output, tail-trimmed to inlineBudget so a large result stays bounded for
// the model even though the on-screen copy is shown in full.
func inlineContext(command, output string) string {
	out := output
	if len(out) > inlineBudget {
		out = "…(truncated)\n" + out[len(out)-inlineBudget:]
	}
	return fmt.Sprintf("I ran a shell command inline:\n$ %s\n\n%s", command, out)
}
