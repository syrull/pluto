package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

func typeInto(m tea.Model, s string) tea.Model {
	for _, r := range s {
		m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func newTestModel(t *testing.T) tea.Model {
	t.Helper()
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var m tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80)}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

func TestSlashOpensCommandMenu(t *testing.T) {
	m := newTestModel(t)
	m = typeInto(m, "/")
	got := m.(model)

	if got.cmdMenu == nil {
		t.Fatal("'/' in the chat input should open the command menu")
	}
	if got.cmdMenu.Len() != len(slashCommands) {
		t.Fatalf("menu shows %d commands, want %d (all)", got.cmdMenu.Len(), len(slashCommands))
	}
	if got.input.Value() != "/" {
		t.Fatalf("input = %q, want %q", got.input.Value(), "/")
	}
}

func TestCommandMenuFiltersLive(t *testing.T) {
	m := newTestModel(t)
	m = typeInto(m, "/mo")
	got := m.(model)

	if got.cmdMenu == nil || got.cmdMenu.Len() != 1 {
		t.Fatalf("'/mo' should narrow to a single match, got %v", got.cmdMenu)
	}
	if c, ok := got.cmdMenu.Selected(); !ok || c.Name != "/model" {
		t.Fatalf("selected = %q,%v want /model,true", c.Name, ok)
	}
}

func TestCommandMenuClosesWhenNoMatch(t *testing.T) {
	m := newTestModel(t)
	m = typeInto(m, "/zzz")
	if got := m.(model); got.cmdMenu != nil {
		t.Fatal("a non-matching slash word should close the menu")
	}
}

func TestCommandMenuClosesOnSpace(t *testing.T) {
	m := newTestModel(t)
	m = typeInto(m, "/new ")
	got := m.(model)
	if got.cmdMenu != nil {
		t.Fatal("typing a space (argument entry) should close the menu")
	}
	if got.input.Value() != "/new " {
		t.Fatalf("input = %q, want %q", got.input.Value(), "/new ")
	}
}

func TestCommandMenuClosesWhenNotSlash(t *testing.T) {
	m := newTestModel(t)
	m = typeInto(m, "/")
	if m.(model).cmdMenu == nil {
		t.Fatal("'/' should open the menu")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := m.(model); got.cmdMenu != nil {
		t.Fatal("backspacing away the '/' should close the menu")
	}
}

func TestCommandMenuTabCompletesSingleMatch(t *testing.T) {
	m := newTestModel(t)
	m = typeInto(m, "/mo")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := m.(model)

	if got.cmdMenu != nil {
		t.Fatal("Tab on a single match should complete and close the menu")
	}
	if got.input.Value() != "/model " {
		t.Fatalf("input = %q, want %q", got.input.Value(), "/model ")
	}
}

func TestCommandMenuTabNavigatesMultipleMatches(t *testing.T) {
	m := newTestModel(t)
	m = typeInto(m, "/")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := m.(model)

	if got.cmdMenu == nil {
		t.Fatal("Tab with several matches should navigate, not close the menu")
	}
	if c, _ := got.cmdMenu.Selected(); c.Name != slashCommands[1].Name {
		t.Fatalf("Tab should advance the highlight to %q, got %q", slashCommands[1].Name, c.Name)
	}
	if got.input.Value() != "/" {
		t.Fatalf("Tab-navigate should not change the input, got %q", got.input.Value())
	}
}

func TestCommandMenuEnterCompletesWithoutSubmitting(t *testing.T) {
	m := newTestModel(t)
	m = typeInto(m, "/")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := m.(model)

	if got.cmdMenu != nil {
		t.Fatal("Enter should complete and close the menu")
	}
	if got.input.Value() != slashCommands[0].Name+" " {
		t.Fatalf("input = %q, want %q", got.input.Value(), slashCommands[0].Name+" ")
	}
	if len(got.lines) != 0 || got.busy {
		t.Fatal("Enter should complete into the input, not submit the command")
	}
}

func TestCommandMenuArrowNavigation(t *testing.T) {
	m := newTestModel(t)
	m = typeInto(m, "/")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	got := m.(model)
	if c, _ := got.cmdMenu.Selected(); c.Name != slashCommands[1].Name {
		t.Fatalf("Down should highlight %q, got %q", slashCommands[1].Name, c.Name)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	got = m.(model)
	if c, _ := got.cmdMenu.Selected(); c.Name != slashCommands[0].Name {
		t.Fatalf("Up should highlight %q, got %q", slashCommands[0].Name, c.Name)
	}
}

func TestCommandMenuEscDismisses(t *testing.T) {
	m := newTestModel(t)
	m = typeInto(m, "/")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := m.(model)

	if got.cmdMenu != nil {
		t.Fatal("Esc should dismiss the menu")
	}
	if got.input.Value() != "/" {
		t.Fatalf("Esc should leave the input untouched, got %q", got.input.Value())
	}
}

func TestCommandMenuRendersAboveFooter(t *testing.T) {
	m := newTestModel(t)
	m = typeInto(m, "/mo")
	view := m.(model).content()

	menu := strings.Index(view, "switch the active model")
	status := strings.Index(view, m.(model).agent.ProviderName())
	if menu < 0 {
		t.Fatalf("menu description should render, view:\n%s", view)
	}
	if status < 0 || menu > status {
		t.Fatalf("menu should render above the footer status line (menu=%d status=%d):\n%s", menu, status, view)
	}
}

func TestSlashInFilesPaneDoesNotOpenCommandMenu(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var m tea.Model = model{md: newRenderer(80)}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	got := m.(model)
	got.focus = paneTree
	m = got

	m, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	got = m.(model)
	if got.cmdMenu != nil {
		t.Fatal("'/' in the Files pane must not open the command menu")
	}
	if got.finder == nil {
		t.Fatal("'/' in the Files pane should still open the fuzzy finder")
	}
}

// TestSlashCommandNotEchoedIntoTranscript guards issue #55: submitting a TUI
// action command (e.g. /model — /image is the documented exception) dispatches
// it without pushing the command text into the conversation, while still
// surfacing any command status/output.
func TestSlashCommandNotEchoedIntoTranscript(t *testing.T) {
	m := newTestModel(t)
	// Seed a prior line so we can assert the command adds nothing to it.
	got := m.(model)
	got.lines = []entry{{text: "prior message"}}
	m = got

	// The trailing space closes the completion menu so Enter submits.
	m = typeInto(m, "/model ")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = m.(model)

	if got.input.Value() != "" {
		t.Fatalf("input should reset after submitting a command, got %q", got.input.Value())
	}
	for _, e := range got.lines {
		if strings.Contains(e.text, "/model") {
			t.Fatalf("slash command should not be echoed into the transcript, got line %q", e.text)
		}
	}
	// The command's status output is still shown (stub provider can't switch models).
	if joined := got.transcript(); !strings.Contains(joined, "does not support model switching") {
		t.Fatalf("command status should still be shown, got:\n%s", joined)
	}
}

// TestRegularMessageEchoedAsUserLine confirms non-command input still renders as
// a user line, so the #55 fix doesn't swallow real messages.
func TestRegularMessageEchoedAsUserLine(t *testing.T) {
	m := newTestModel(t)
	m = typeInto(m, "hello there")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := m.(model)

	if !strings.Contains(got.transcript(), "hello there") {
		t.Fatalf("a regular message should appear as a user line, got:\n%s", got.transcript())
	}
}

// TestSlashCommandsMatchDispatch guards the registry against drift: every case
// in handleCommand must have a registry entry and vice versa.
func TestSlashCommandsMatchDispatch(t *testing.T) {
	src, err := os.ReadFile("update.go")
	if err != nil {
		t.Fatalf("read update.go: %v", err)
	}
	re := regexp.MustCompile(`case "(/\w+)":`)
	var dispatch []string
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		dispatch = append(dispatch, m[1])
	}
	sort.Strings(dispatch)

	var registry []string
	for _, c := range slashCommands {
		registry = append(registry, c.Name)
	}
	sort.Strings(registry)

	if strings.Join(dispatch, " ") != strings.Join(registry, " ") {
		t.Fatalf("slash command registry and handleCommand dispatch drifted:\n dispatch: %v\n registry: %v", dispatch, registry)
	}
}
