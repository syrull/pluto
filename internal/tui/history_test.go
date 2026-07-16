package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

// busyModel builds a ready model held busy so plain submits take the steer path
// (no agent goroutine) while still recording input history.
func busyModel(t *testing.T) tea.Model {
	t.Helper()
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var m tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80), busy: true}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

// submit types s into the input and presses Enter.
func submit(m tea.Model, s string) tea.Model {
	m = typeInto(m, s)
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return m
}

func press(m tea.Model, code rune) tea.Model {
	m, _ = m.Update(tea.KeyPressMsg{Code: code})
	return m
}

func TestHistoryUpRecallsLastMessage(t *testing.T) {
	m := busyModel(t)
	m = submit(m, "hello world")
	if got := m.(model); got.input.Value() != "" {
		t.Fatalf("input should reset after submit, got %q", got.input.Value())
	}
	m = press(m, tea.KeyUp)
	if got := m.(model).input.Value(); got != "hello world" {
		t.Fatalf("↑ should recall last message, got %q", got)
	}
}

func TestHistoryCyclesOlderAndNewer(t *testing.T) {
	m := busyModel(t)
	m = submit(m, "first")
	m = submit(m, "second")
	m = submit(m, "third")

	m = press(m, tea.KeyUp)
	if got := m.(model).input.Value(); got != "third" {
		t.Fatalf("first ↑ = %q, want %q", got, "third")
	}
	m = press(m, tea.KeyUp)
	if got := m.(model).input.Value(); got != "second" {
		t.Fatalf("second ↑ = %q, want %q", got, "second")
	}
	m = press(m, tea.KeyUp)
	if got := m.(model).input.Value(); got != "first" {
		t.Fatalf("third ↑ = %q, want %q", got, "first")
	}
	// At the oldest entry, further ↑ stays put (consumed, not scrolled past).
	m = press(m, tea.KeyUp)
	if got := m.(model).input.Value(); got != "first" {
		t.Fatalf("↑ past oldest = %q, want %q", got, "first")
	}
	m = press(m, tea.KeyDown)
	if got := m.(model).input.Value(); got != "second" {
		t.Fatalf("↓ = %q, want %q", got, "second")
	}
}

func TestHistoryDownClearsInputPastNewest(t *testing.T) {
	m := busyModel(t)
	m = submit(m, "only")
	m = press(m, tea.KeyUp)
	if got := m.(model).input.Value(); got != "only" {
		t.Fatalf("↑ = %q, want %q", got, "only")
	}
	// ↓ steps past the newest entry, clearing the buffer.
	m = press(m, tea.KeyDown)
	if got := m.(model).input.Value(); got != "" {
		t.Fatalf("↓ past newest should clear input, got %q", got)
	}
}

func TestHistorySkipsConsecutiveDuplicates(t *testing.T) {
	m := busyModel(t)
	m = submit(m, "same")
	m = submit(m, "same")
	got := m.(model)
	if len(got.history) != 1 {
		t.Fatalf("consecutive duplicates should collapse, history=%v", got.history)
	}
}

func TestHistoryUpWithDirtyBufferScrolls(t *testing.T) {
	m := busyModel(t)
	m = submit(m, "past message")
	// Type a fresh, unsent line; ↑ must not clobber it with history.
	m = typeInto(m, "draft")
	m = press(m, tea.KeyUp)
	if got := m.(model).input.Value(); got != "draft" {
		t.Fatalf("↑ with a dirty buffer should not recall, got %q", got)
	}
}

func TestHistoryEmptyScrollsAndLeavesInput(t *testing.T) {
	m := busyModel(t)
	m = press(m, tea.KeyUp)
	m = press(m, tea.KeyDown)
	if got := m.(model).input.Value(); got != "" {
		t.Fatalf("↑/↓ with no history should leave input empty, got %q", got)
	}
}

func TestHistoryEditExitsNavigation(t *testing.T) {
	m := busyModel(t)
	m = submit(m, "alpha")
	m = submit(m, "beta")
	m = press(m, tea.KeyUp) // recalls "beta"
	// Edit the recalled line; a later ↑ must not clobber the edit.
	m = typeInto(m, "X")
	if got := m.(model).input.Value(); got != "betaX" {
		t.Fatalf("edit after recall = %q, want %q", got, "betaX")
	}
	m = press(m, tea.KeyUp)
	if got := m.(model).input.Value(); got != "betaX" {
		t.Fatalf("↑ after editing a recalled line should not recall, got %q", got)
	}
}

func TestRecordHistoryDedupAndOrder(t *testing.T) {
	var m model
	m.recordHistory("first")
	m.recordHistory("second")
	m.recordHistory("second") // consecutive duplicate collapses
	m.recordHistory("/model") // slash commands are recorded too
	m.recordHistory("")       // blanks are ignored

	want := []string{"first", "second", "/model"}
	if !slices.Equal(m.history, want) {
		t.Fatalf("history = %v, want %v", m.history, want)
	}
	if m.histPos != len(m.history) {
		t.Fatalf("histPos = %d, want %d (not navigating)", m.histPos, len(m.history))
	}
}

func TestWordJumpCommandBindings(t *testing.T) {
	ta := newInput(80)
	if !contains(ta.KeyMap.WordBackward.Keys(), "super+left") {
		t.Fatalf("WordBackward keys = %v, want super+left", ta.KeyMap.WordBackward.Keys())
	}
	if !contains(ta.KeyMap.WordForward.Keys(), "super+right") {
		t.Fatalf("WordForward keys = %v, want super+right", ta.KeyMap.WordForward.Keys())
	}

	// Command+← jumps back a word: cursor lands before "two", so an inserted
	// rune splits there.
	m := busyModel(t)
	m = typeInto(m, "one two")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModSuper})
	m = typeInto(m, "X")
	if got := m.(model).input.Value(); got != "one Xtwo" {
		t.Fatalf("Command+← word jump = %q, want %q", got, "one Xtwo")
	}

	// Command+→ from the start jumps forward a word: cursor lands after "one".
	m2 := busyModel(t)
	m2 = typeInto(m2, "one two")
	m2, _ = m2.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m2, _ = m2.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModSuper})
	m2 = typeInto(m2, "X")
	if got := m2.(model).input.Value(); got != "oneX two" {
		t.Fatalf("Command+→ word jump = %q, want %q", got, "oneX two")
	}
}

func TestWordJumpBackwardAtStartDoesNotHang(t *testing.T) {
	// Command+← / alt+← on an empty (or start-of-buffer) input must no-op rather
	// than spin the textarea's word-backward loop forever.
	done := make(chan struct{})
	go func() {
		defer close(done)
		m := busyModel(t)
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModSuper})
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt})
		if got := m.(model).input.Value(); got != "" {
			t.Errorf("word-backward at start changed empty buffer to %q", got)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("word-backward at start of empty input hung")
	}
}

func TestHistoryDebugInstrumentation(t *testing.T) {
	read := enableTUILog(t, "trace")
	m := busyModel(t)
	m = submit(m, "logged message")
	m = press(m, tea.KeyUp)   // recall
	m = press(m, tea.KeyDown) // clear past newest
	m = press(m, tea.KeyUp)   // recall again
	out := read()

	for _, want := range []string{
		"history record",
		`history recall dir=prev`,
		`history recall dir=next`,
		`outcome=cleared`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("debug log missing %q:\n%s", want, out)
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
