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
	issue := issueContext(ghIssue{Number: 24, Title: "first"})

	m.toggleGHContext(issue)
	if len(m.ghContext) != 1 || m.ghContext[0].Number != 24 {
		t.Fatalf("issue #24 should be staged, got %+v", m.ghContext)
	}
	if !strings.Contains(m.notice, "added issue #24") {
		t.Fatalf("add should surface a notice, got %q", m.notice)
	}

	m.toggleGHContext(prContext(ghPR{Number: 12, Title: "the pr"}))
	if len(m.ghContext) != 2 {
		t.Fatalf("a PR should stack alongside the issue, got %+v", m.ghContext)
	}
	if !strings.Contains(m.notice, "added PR #12") {
		t.Fatalf("adding a PR should name it, got %q", m.notice)
	}

	m.toggleGHContext(issue) // toggle issue #24 back off
	if len(m.ghContext) != 1 || !m.ghContext[0].PR || m.ghContext[0].Number != 12 {
		t.Fatalf("re-adding issue #24 should remove only it, got %+v", m.ghContext)
	}
	if !strings.Contains(m.notice, "removed issue #24") {
		t.Fatalf("remove should surface a notice, got %q", m.notice)
	}
}

func TestToggleGHContextIssueAndPRSameNumberDistinct(t *testing.T) {
	m := &model{}
	m.toggleGHContext(issueContext(ghIssue{Number: 12, Title: "issue twelve"}))
	m.toggleGHContext(prContext(ghPR{Number: 12, Title: "pr twelve"}))
	if len(m.ghContext) != 2 {
		t.Fatalf("issue #12 and PR #12 should both stage, got %+v", m.ghContext)
	}
}

func TestTakeGHContextClears(t *testing.T) {
	m := &model{ghContext: []ghContextItem{{Number: 24}, {PR: true, Number: 12}}}
	got := m.takeGHContext()
	if len(got) != 2 {
		t.Fatalf("take should return both items, got %+v", got)
	}
	if len(m.ghContext) != 0 {
		t.Fatalf("take should clear staged context, got %+v", m.ghContext)
	}
}

func TestGHContextRefs(t *testing.T) {
	m := &model{ghContext: []ghContextItem{{Number: 7}, {PR: true, Number: 42}}}
	refs := m.ghContextRefs()
	want := []ghRef{{num: 7}, {pr: true, num: 42}}
	if len(refs) != 2 || refs[0] != want[0] || refs[1] != want[1] {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
}

func TestComposeWithGHContext(t *testing.T) {
	if got := composeWithGHContext(nil, "hello"); got != "hello" {
		t.Fatalf("no context should pass input through, got %q", got)
	}

	items := []ghContextItem{
		{Number: 24, Title: "bug in parser", State: "OPEN", Author: "me", Body: "steps to repro"},
		{PR: true, Number: 12, Title: "the fix", Branch: "fix-24", Body: "diff summary"},
	}
	got := composeWithGHContext(items, "please review")
	for _, want := range []string{
		"Issue #24: bug in parser", "steps to repro",
		"Pull Request #12: the fix", "branch fix-24", "diff summary", "please review",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("composed prompt missing %q:\n%s", want, got)
		}
	}
	// The user's message comes after the attached context block.
	if strings.Index(got, "Issue #24") > strings.Index(got, "please review") {
		t.Fatalf("context should precede the user message:\n%s", got)
	}
}

func TestComposeWithGHContextEmptyInput(t *testing.T) {
	got := composeWithGHContext([]ghContextItem{{PR: true, Number: 12, Title: "t"}}, "")
	if !strings.Contains(got, "Pull Request #12") {
		t.Fatalf("context-only turn should still carry the PR:\n%s", got)
	}
	if strings.HasSuffix(got, "\n") {
		t.Fatalf("context-only prompt should be trimmed, got %q", got)
	}
}

func TestGHContextChip(t *testing.T) {
	if ghContextChip(nil) != "" {
		t.Fatal("no items should render no chip")
	}
	chip := ghContextChip([]ghContextItem{{Number: 24}, {PR: true, Number: 12}})
	if !strings.Contains(chip, "#24") || !strings.Contains(chip, "PR #12") {
		t.Fatalf("chip should list the issue and PR, got %q", chip)
	}
}

func TestRenderUserLineShowsContextChip(t *testing.T) {
	m := &model{md: newRenderer(80), width: 80}
	line := m.renderUserLine("fix these", nil, []ghContextItem{{Number: 24}, {PR: true, Number: 12}})
	if !strings.Contains(line, "#24") || !strings.Contains(line, "PR #12") {
		t.Fatalf("user line should include the context chip, got:\n%s", line)
	}
}

func TestApplyGHOutcomeAddContextKeepsBrowserOpen(t *testing.T) {
	m := &model{ghm: newGHModal()}
	m.applyGHOutcome(ghOutcome{kind: ghOutcomeAddContext, ctx: prContext(ghPR{Number: 12, Title: "pr"})})
	if len(m.ghContext) != 1 || !m.ghContext[0].PR || m.ghContext[0].Number != 12 {
		t.Fatalf("add-context outcome should stage the PR, got %+v", m.ghContext)
	}
	if m.ghm == nil {
		t.Fatal("adding context should keep the browser open for more items")
	}
}

func TestSubmitSendsAndClearsGHContext(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var tm tea.Model = model{
		agent: ag, md: newRenderer(80), input: newInput(80),
		ghContext: []ghContextItem{{PR: true, Number: 12, Title: "the fix", Body: "diff"}},
	}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	for _, r := range "please review" {
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
	if joined := got.transcript(); !strings.Contains(joined, "PR #12") {
		t.Fatalf("transcript should show the context chip, got:\n%s", joined)
	}
}
