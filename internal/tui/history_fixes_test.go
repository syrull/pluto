package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Fix 1: word-backward must no-op (not spin the textarea's wordLeft loop) whenever
// only whitespace lies to the left of the cursor — not just at the very start of
// an empty buffer. Each case is timeout-guarded and asserted via the debug log.
func TestWordBackwardNothingLeftNoOpAndLogs(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T) tea.Model
	}{
		{
			name: "leading-space",
			build: func(t *testing.T) tea.Model {
				m := busyModel(t)
				m = typeInto(m, " abc")
				m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyHome})  // col 0
				m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight}) // col 1, just past the space
				return m
			},
		},
		{
			name: "empty-first-line",
			build: func(t *testing.T) tea.Model {
				m := busyModel(t)
				m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}) // blank first line
				m = typeInto(m, "abc")
				m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyHome}) // start of line 2
				return m
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			read := enableTUILog(t, "trace")
			done := make(chan struct{})
			go func() {
				defer close(done)
				m := tc.build(t)
				want := m.(model).input.Value()
				// Both the Command (super) and alt bindings must no-op here.
				m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModSuper})
				m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt})
				if got := m.(model).input.Value(); got != want {
					t.Errorf("word-backward changed buffer %q -> %q", want, got)
				}
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("word-backward hung for %s", tc.name)
			}
			out := read()
			if !strings.Contains(out, "word jump dir=back outcome=nothing-left") {
				t.Errorf("expected no-op logged for %s:\n%s", tc.name, out)
			}
		})
	}
}

// Fix 2: a transient background swap (onWorkspace, fired on every background-agent
// event) must not reset the active workspace's recall position. Proven via the
// debug log: recall keeps walking (two prev recalls) instead of falling into the
// buffer-dirty / not-navigating no-op paths.
func TestHistoryRecallSurvivesBackgroundWorkspace(t *testing.T) {
	read := enableTUILog(t, "trace")
	m := multiModel(2) // workspace 0 active, workspace 1 in the background

	m.history = []string{"first", "second", "third"}
	m.histPos = len(m.history)

	if !m.historyPrev() { // ↑ recalls "third"
		t.Fatal("historyPrev should recall the newest entry")
	}
	if !m.navigatingHistory() {
		t.Fatalf("should be navigating after recall, histPos=%d len=%d", m.histPos, len(m.history))
	}
	pos := m.histPos

	// A background agent (workspace 1) produces output while we are mid-recall.
	m.onWorkspace(1, func() {})

	if !m.navigatingHistory() || m.histPos != pos {
		t.Fatalf("background activity dropped recall: histPos=%d (want %d), navigating=%v",
			m.histPos, pos, m.navigatingHistory())
	}
	if !m.historyPrev() { // ↑ keeps walking to "second"
		t.Fatal("historyPrev after background activity should keep recalling")
	}
	if got := m.input.Value(); got != "second" {
		t.Fatalf("recall after background activity = %q, want %q", got, "second")
	}

	out := read()
	if strings.Contains(out, "outcome=buffer-dirty") || strings.Contains(out, "outcome=not-navigating") {
		t.Errorf("recall should not have been interrupted by background activity:\n%s", out)
	}
	if n := strings.Count(out, "history recall dir=prev"); n < 2 {
		t.Errorf("expected at least two prev recalls logged, got %d:\n%s", n, out)
	}
}

// A user-facing switch (switchTo), unlike a transient background swap, still starts
// the newly active workspace fresh (not navigating).
func TestHistorySwitchWorkspaceStartsFresh(t *testing.T) {
	m := multiModel(2)
	m.history = []string{"a", "b"}
	m.histPos = len(m.history)
	if !m.historyPrev() { // navigating in workspace 0
		t.Fatal("historyPrev should recall")
	}
	if !m.navigatingHistory() {
		t.Fatal("precondition: should be navigating")
	}
	m.switchTo(1)
	if m.navigatingHistory() {
		t.Fatalf("switching workspaces should not carry recall navigation, histPos=%d len=%d",
			m.histPos, len(m.history))
	}
}
