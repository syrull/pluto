package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

func TestToggleGHContextAddsAndRemoves(t *testing.T) {
	m := &model{}
	is := ghIssue{Number: 24, Title: "first"}

	m.toggleGHContext(is)
	if len(m.ghContext) != 1 || m.ghContext[0].Number != 24 {
		t.Fatalf("issue #24 should be staged, got %+v", m.ghContext)
	}
	if !strings.Contains(m.notice, "added issue #24") {
		t.Fatalf("add should surface a notice, got %q", m.notice)
	}

	m.toggleGHContext(ghIssue{Number: 25, Title: "second"})
	if len(m.ghContext) != 2 {
		t.Fatalf("a second issue should stack, got %+v", m.ghContext)
	}

	m.toggleGHContext(is) // toggle #24 back off
	if len(m.ghContext) != 1 || m.ghContext[0].Number != 25 {
		t.Fatalf("re-adding #24 should remove it, got %+v", m.ghContext)
	}
	if !strings.Contains(m.notice, "removed issue #24") {
		t.Fatalf("remove should surface a notice, got %q", m.notice)
	}
}

func TestTakeGHContextClears(t *testing.T) {
	m := &model{ghContext: []ghIssue{{Number: 24}, {Number: 25}}}
	got := m.takeGHContext()
	if len(got) != 2 {
		t.Fatalf("take should return both issues, got %+v", got)
	}
	if len(m.ghContext) != 0 {
		t.Fatalf("take should clear staged context, got %+v", m.ghContext)
	}
}

func TestGHContextNumbers(t *testing.T) {
	m := &model{ghContext: []ghIssue{{Number: 7}, {Number: 42}}}
	nums := m.ghContextNumbers()
	if len(nums) != 2 || nums[0] != 7 || nums[1] != 42 {
		t.Fatalf("numbers = %v, want [7 42]", nums)
	}
}

func TestComposeWithGHContext(t *testing.T) {
	if got := composeWithGHContext(nil, "hello"); got != "hello" {
		t.Fatalf("no context should pass input through, got %q", got)
	}

	issues := []ghIssue{
		{Number: 24, Title: "bug in parser", State: "OPEN", Author: "me", Body: "steps to repro"},
		{Number: 25, Title: "second"},
	}
	got := composeWithGHContext(issues, "please fix these")
	for _, want := range []string{"Issue #24: bug in parser", "steps to repro", "Issue #25: second", "please fix these"} {
		if !strings.Contains(got, want) {
			t.Fatalf("composed prompt missing %q:\n%s", want, got)
		}
	}
	// The user's message comes after the attached context block.
	if strings.Index(got, "Issue #24") > strings.Index(got, "please fix these") {
		t.Fatalf("context should precede the user message:\n%s", got)
	}
}

func TestComposeWithGHContextEmptyInput(t *testing.T) {
	got := composeWithGHContext([]ghIssue{{Number: 24, Title: "t"}}, "")
	if !strings.Contains(got, "Issue #24") {
		t.Fatalf("context-only turn should still carry the issue:\n%s", got)
	}
	if strings.HasSuffix(got, "\n") {
		t.Fatalf("context-only prompt should be trimmed, got %q", got)
	}
}

func TestGHContextChip(t *testing.T) {
	if ghContextChip(nil) != "" {
		t.Fatal("no issues should render no chip")
	}
	chip := ghContextChip([]ghIssue{{Number: 24}, {Number: 25}})
	if !strings.Contains(chip, "#24") || !strings.Contains(chip, "#25") {
		t.Fatalf("chip should list both issue numbers, got %q", chip)
	}
}

func TestRenderUserLineShowsContextChip(t *testing.T) {
	m := &model{md: newRenderer(80), width: 80}
	line := m.renderUserLine("fix these", nil, []ghIssue{{Number: 24}})
	if !strings.Contains(line, "#24") {
		t.Fatalf("user line should include the context chip, got:\n%s", line)
	}
}

func TestApplyGHOutcomeAddContextKeepsBrowserOpen(t *testing.T) {
	m := &model{ghm: newGHModal()}
	m.applyGHOutcome(ghOutcome{kind: ghOutcomeAddContext, issue: ghIssue{Number: 24, Title: "bug"}})
	if len(m.ghContext) != 1 || m.ghContext[0].Number != 24 {
		t.Fatalf("add-context outcome should stage the issue, got %+v", m.ghContext)
	}
	if m.ghm == nil {
		t.Fatal("adding context should keep the browser open for more issues")
	}
}

func TestSubmitSendsAndClearsGHContext(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var tm tea.Model = model{
		agent: ag, md: newRenderer(80), input: newInput(80),
		ghContext: []ghIssue{{Number: 24, Title: "parser bug", Body: "repro steps"}},
	}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	for _, r := range "please fix" {
		tm, _ = tm.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := tm.(model)

	if len(got.ghContext) != 0 {
		t.Fatalf("context should be cleared after send, got %+v", got.ghContext)
	}
	if !got.busy {
		t.Fatal("submitting a message should start a run")
	}
	if joined := got.transcript(); !strings.Contains(joined, "#24") {
		t.Fatalf("transcript should show the context chip, got:\n%s", joined)
	}
}
