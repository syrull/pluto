package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

func TestEscWhileBusyCancelsRequest(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	ag.Steer("leftover")
	ctx, cancel := context.WithCancel(context.Background())
	var tm tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80), busy: true, cancel: cancel}

	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := tm.(model)

	if ctx.Err() == nil {
		t.Fatal("esc while busy should cancel the in-flight request context")
	}
	if got.cancel != nil {
		t.Fatal("cancel func should be cleared after interrupt")
	}
	if !strings.Contains(got.notice, "canceled") {
		t.Fatalf("notice should report cancellation, got %q", got.notice)
	}
	if pending := ag.TakeSteering(); len(pending) != 0 {
		t.Fatalf("interrupt should drain queued steering so the turn doesn't restart, got %v", pending)
	}
}

func TestEscWhileIdleEditsInput(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var tm tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80)}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := tm.(model)

	if strings.Contains(got.notice, "canceled") {
		t.Fatalf("esc while idle should not report a cancellation, got %q", got.notice)
	}
}

func TestDoneMsgClearsCancel(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	_, cancel := context.WithCancel(context.Background())
	m := model{agent: ag, md: newRenderer(80), input: newInput(80), busy: true, cancel: cancel}

	updated, _ := m.Update(doneMsg{})
	got := updated.(model)

	if got.busy {
		t.Fatal("doneMsg should clear busy")
	}
	if got.cancel != nil {
		t.Fatal("doneMsg should clear the cancel func")
	}
}

func TestBusyStatusAdvertisesCancel(t *testing.T) {
	m := &model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), ""), busy: true}
	if status := m.modelStatus(); !strings.Contains(status, "esc to cancel") {
		t.Fatalf("busy status should advertise esc to cancel, got:\n%s", status)
	}
}
