package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

// submitInline types line into the input, presses enter, and (if the enter
// handler returned a command) runs it synchronously and delivers its message.
func submitInline(t *testing.T, m tea.Model, line string) model {
	t.Helper()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	got := m.(model)
	got.input.SetValue(line)
	m, cmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			m, _ = m.Update(msg)
		}
	}
	return m.(model)
}

func newInlineModel() tea.Model {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "sys")
	return model{agent: ag, md: newRenderer(80), input: newInput(80)}
}

func TestInlineCommandRunsAndRenders(t *testing.T) {
	got := submitInline(t, newInlineModel(), "!echo hi")

	if got.busy {
		t.Fatal("an inline command must not start an agent turn")
	}
	tr := got.transcript()
	if !strings.Contains(tr, "echo hi") {
		t.Fatalf("transcript should echo the command:\n%s", tr)
	}
	if !strings.Contains(tr, "hi") {
		t.Fatalf("transcript should show the command output:\n%s", tr)
	}
}

func TestInlineCommandFoldsIntoConversation(t *testing.T) {
	m := newInlineModel().(model)
	got := submitInline(t, m, "!echo folded")

	msgs := got.agent.Snapshot()
	found := false
	for _, msg := range msgs {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "echo folded") && strings.Contains(msg.Content, "folded") {
			found = true
		}
	}
	if !found {
		t.Fatal("inline command + output should be folded into the conversation as context")
	}
}

func TestLoneBangIsHint(t *testing.T) {
	got := submitInline(t, newInlineModel(), "!")

	if got.notice == "" {
		t.Fatal("a lone ! should surface a hint")
	}
	if len(got.lines) != 0 {
		t.Fatalf("a lone ! should push nothing to the transcript, got %d blocks", len(got.lines))
	}
	if len(got.agent.Snapshot()) != 1 { // system prompt only
		t.Fatal("a lone ! must not touch the conversation")
	}
}

func TestInlineWhileBusyQueuesSteering(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "sys")
	m := model{agent: ag, md: newRenderer(80), input: newInput(80), busy: true}
	got := submitInline(t, m, "!echo busy")

	if !got.busy {
		t.Fatal("running an inline command must not clear the agent's busy state")
	}
	pending := ag.TakeSteering()
	if len(pending) != 1 || !strings.Contains(pending[0], "echo busy") {
		t.Fatalf("inline result should be queued as steering while busy, got %v", pending)
	}
}

func TestInlineFullOutputRetained(t *testing.T) {
	got := submitInline(t, newInlineModel(), "!seq 1 50")

	if len(got.outputs) != 1 {
		t.Fatalf("long inline output should be retained for a Show modal, got %d", len(got.outputs))
	}
	// The retained copy holds every line, uncapped, even though the inline
	// preview only shows the first few.
	if lines := strings.Count(got.outputs[0].full, "\n") + 1; lines < 50 {
		t.Fatalf("retained output = %d lines, want the full 50 (untruncated)", lines)
	}
	if !strings.Contains(got.transcript(), "[ctrl+o]") {
		t.Fatalf("a long inline result should carry a view affordance:\n%s", got.transcript())
	}
}

func TestEscCancelsInlineCommand(t *testing.T) {
	m := newInlineModel().(model)
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.inlineEpoch = 1
	canceled := false
	m.inlineCancel = func() { canceled = true }

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := updated.(model)

	if !canceled {
		t.Fatal("esc should cancel a running inline command")
	}
	if got.inlineCancel != nil {
		t.Fatal("esc should clear the inline cancel func")
	}
	if got.inlineEpoch == 1 {
		t.Fatal("esc should bump the epoch so the canceled run's result is dropped")
	}
	if !strings.Contains(got.notice, "canceled") {
		t.Fatalf("notice should report cancellation, got %q", got.notice)
	}
}

func updateModel(m model, msg tea.Msg) (model, tea.Cmd) {
	tm, cmd := m.Update(msg)
	return tm.(model), cmd
}
