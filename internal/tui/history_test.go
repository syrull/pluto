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

// pressCtrl sends a ctrl-modified key (e.g. ctrl+p recall, ctrl+n forward).
func pressCtrl(m tea.Model, r rune) tea.Model {
	m, _ = m.Update(tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl})
	return m
}

func TestHistoryCtrlPRecallsLastMessage(t *testing.T) {
	m := busyModel(t)
	m = submit(m, "hello world")
	if got := m.(model); got.input.Value() != "" {
		t.Fatalf("input should reset after submit, got %q", got.input.Value())
	}
	m = pressCtrl(m, 'p')
	if got := m.(model).input.Value(); got != "hello world" {
		t.Fatalf("ctrl+p should recall last message, got %q", got)
	}
}

func TestHistoryCyclesOlderAndNewer(t *testing.T) {
	m := busyModel(t)
	m = submit(m, "first")
	m = submit(m, "second")
	m = submit(m, "third")

	m = pressCtrl(m, 'p')
	if got := m.(model).input.Value(); got != "third" {
		t.Fatalf("first ctrl+p = %q, want %q", got, "third")
	}
	m = pressCtrl(m, 'p')
	if got := m.(model).input.Value(); got != "second" {
		t.Fatalf("second ctrl+p = %q, want %q", got, "second")
	}
	m = pressCtrl(m, 'p')
	if got := m.(model).input.Value(); got != "first" {
		t.Fatalf("third ctrl+p = %q, want %q", got, "first")
	}
	// At the oldest entry, further ctrl+p stays put (consumed, not walked past).
	m = pressCtrl(m, 'p')
	if got := m.(model).input.Value(); got != "first" {
		t.Fatalf("ctrl+p past oldest = %q, want %q", got, "first")
	}
	m = pressCtrl(m, 'n')
	if got := m.(model).input.Value(); got != "second" {
		t.Fatalf("ctrl+n = %q, want %q", got, "second")
	}
}

func TestHistoryCtrlNClearsInputPastNewest(t *testing.T) {
	m := busyModel(t)
	m = submit(m, "only")
	m = pressCtrl(m, 'p')
	if got := m.(model).input.Value(); got != "only" {
		t.Fatalf("ctrl+p = %q, want %q", got, "only")
	}
	// ctrl+n steps past the newest entry, clearing the buffer.
	m = pressCtrl(m, 'n')
	if got := m.(model).input.Value(); got != "" {
		t.Fatalf("ctrl+n past newest should clear input, got %q", got)
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

func TestHistoryCtrlPWithDirtyBufferKeepsDraft(t *testing.T) {
	m := busyModel(t)
	m = submit(m, "past message")
	// Type a fresh, unsent line; ctrl+p must not clobber it with history.
	m = typeInto(m, "draft")
	m = pressCtrl(m, 'p')
	dirty := m.(model)
	if dirty.input.Value() != "draft" {
		t.Fatalf("ctrl+p with a dirty buffer should not recall, got %q", dirty.input.Value())
	}
	if dirty.navigatingHistory() {
		t.Fatalf("ctrl+p on a dirty buffer must not enter history navigation")
	}
}

func TestHistoryEmptyCtrlPLeavesInput(t *testing.T) {
	m := busyModel(t)
	m = pressCtrl(m, 'p')
	m = pressCtrl(m, 'n')
	if got := m.(model).input.Value(); got != "" {
		t.Fatalf("ctrl+p/ctrl+n with no history should leave input empty, got %q", got)
	}
}

func TestHistoryEditExitsNavigation(t *testing.T) {
	m := busyModel(t)
	m = submit(m, "alpha")
	m = submit(m, "beta")
	m = pressCtrl(m, 'p') // recalls "beta"
	// Edit the recalled line; a later ctrl+p must not clobber the edit.
	m = typeInto(m, "X")
	if got := m.(model).input.Value(); got != "betaX" {
		t.Fatalf("edit after recall = %q, want %q", got, "betaX")
	}
	m = pressCtrl(m, 'p')
	if got := m.(model).input.Value(); got != "betaX" {
		t.Fatalf("ctrl+p after editing a recalled line should not recall, got %q", got)
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
	m = pressCtrl(m, 'p') // recall
	m = pressCtrl(m, 'n') // clear past newest
	m = pressCtrl(m, 'p') // recall again
	out := read()

	for _, want := range []string{
		"history record",
		`history recall dir=prev`,
		`history recall dir=next`,
		`outcome=cleared`,
		// The chat-key fork records how ctrl+p resolved, distinguishing recall
		// from a plain scroll or cursor move.
		`chat key key=ctrl+p action=recall`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("debug log missing %q:\n%s", want, out)
		}
	}
}

// TestScrollUpWithHistoryScrolls is the core regression for #88: with recorded
// history but an empty buffer, ↑/↓ scroll the transcript and never recall.
func TestScrollUpWithHistoryScrolls(t *testing.T) {
	read := enableTUILog(t, "trace")
	var m tea.Model = model{md: newRenderer(80)}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	for range 30 {
		m, _ = m.Update(eventMsg{Kind: "text", Text: "line of output"})
	}
	// Non-empty history, empty buffer: the state where the user wants to read back.
	got := m.(model)
	got.history = []string{"remember me"}
	got.histPos = len(got.history)
	m = got
	inputBefore := got.input.Value()
	offsetBefore := got.vp.YOffset()

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	got = m.(model)
	if got.vp.YOffset() == offsetBefore {
		t.Fatalf("↑ should scroll with non-empty history, YOffset stayed %d", offsetBefore)
	}
	if got.input.Value() != inputBefore {
		t.Fatalf("↑ altered the input buffer: %q -> %q", inputBefore, got.input.Value())
	}
	if got.navigatingHistory() {
		t.Fatalf("↑ must not enter history navigation")
	}
	if out := read(); !strings.Contains(out, "chat key key=up action=scroll") {
		t.Errorf("debug log should record ↑ as a scroll:\n%s", out)
	}
}

// TestScrollDownWithHistoryScrolls is the ↓ counterpart of the #88 regression.
func TestScrollDownWithHistoryScrolls(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	for range 30 {
		m, _ = m.Update(eventMsg{Kind: "text", Text: "line of output"})
	}
	got := m.(model)
	got.history = []string{"remember me"}
	got.histPos = len(got.history)
	m = got
	// Scroll up first so ↓ has somewhere to go.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	got = m.(model)
	inputBefore := got.input.Value()
	offsetBefore := got.vp.YOffset()

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	got = m.(model)
	if got.vp.YOffset() == offsetBefore {
		t.Fatalf("↓ should scroll with non-empty history, YOffset stayed %d", offsetBefore)
	}
	if got.input.Value() != inputBefore {
		t.Fatalf("↓ altered the input buffer: %q -> %q", inputBefore, got.input.Value())
	}
}

// TestCtrlPMovesCursorInMultiLineDraft: with a dirty multi-line draft, ctrl+p/n
// don't recall — they fall through to the textarea so cursor line movement keeps
// working, and a subsequent insertion lands on the moved-to line.
func TestCtrlPMovesCursorInMultiLineDraft(t *testing.T) {
	m := busyModel(t)
	m = submit(m, "history entry")
	m = typeInto(m, "one")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}) // newline
	m = typeInto(m, "two")
	// ctrl+p moves the cursor up to the first line; inserting there proves it.
	m = pressCtrl(m, 'p')
	draft := m.(model)
	if draft.navigatingHistory() {
		t.Fatalf("ctrl+p in a multi-line draft must not recall history")
	}
	m = typeInto(m, "X")
	if got := m.(model).input.Value(); got != "oneX\ntwo" {
		t.Fatalf("ctrl+p should move the cursor up a line, got %q", got)
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
