package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/judge"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/policy"
	"github.com/syrull/pluto/internal/tool"
)

func TestRunsInBackground(t *testing.T) {
	cases := map[string]bool{
		"/gh":         true,
		"/auto off":   true,
		"/auto on":    true,
		"/new":        false,
		"/model gpt5": false,
		"":            false,
		"hello there": false,
	}
	for in, want := range cases {
		if got := runsInBackground(in); got != want {
			t.Errorf("runsInBackground(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestBackgroundCommandsAreRegistered guards the two registries against drift:
// every background-safe command must be a real slash command.
func TestBackgroundCommandsAreRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, c := range slashCommands {
		registered[c.Name] = true
	}
	for name := range backgroundCommands {
		if !registered[name] {
			t.Errorf("background command %q is not in the slash-command registry", name)
		}
	}
}

// TestAutoOffWhileBusyTogglesGate confirms /auto off dispatches while the agent
// is working — flipping the review gate off without entering the conversation or
// disturbing the running turn.
func TestAutoOffWhileBusyTogglesGate(t *testing.T) {
	gate := policy.NewReviewGate(policy.Config{Mode: policy.ModeAuto, JudgeName: "judge-x"}, judge.Fake{})
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "", agent.WithGate(gate))
	var m tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80), busy: true}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if !gate.AutoEnabled() {
		t.Fatal("gate should start in auto mode")
	}
	m = typeInto(m, "/auto off")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := m.(model)

	if gate.AutoEnabled() {
		t.Fatal("/auto off while busy should disable the review gate")
	}
	if !got.busy {
		t.Fatal("dispatching a background command must not clear the busy flag")
	}
	if got.input.Value() != "" {
		t.Fatalf("input should reset after a background command, got %q", got.input.Value())
	}
	if pending := ag.TakeSteering(); len(pending) != 0 {
		t.Fatalf("a background command must not be queued as steering, got %v", pending)
	}
	if strings.Contains(got.transcript(), "/auto") {
		t.Fatalf("a background command should not enter the conversation, got:\n%s", got.transcript())
	}
}

// TestNonBackgroundCommandWhileBusyDeferred confirms an unsafe command (e.g.
// /new) is still rejected while busy rather than dispatched or steered.
func TestNonBackgroundCommandWhileBusyDeferred(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var m tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80), busy: true}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Trailing space closes the completion menu so Enter submits.
	m = typeInto(m, "/new ")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := m.(model)

	if !strings.Contains(got.transcript(), "commands are unavailable while the agent is working") {
		t.Fatalf("a non-background command should be rejected while busy, got:\n%s", got.transcript())
	}
	if pending := ag.TakeSteering(); len(pending) != 0 {
		t.Fatalf("a rejected command must not be queued as steering, got %v", pending)
	}
}
