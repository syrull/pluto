package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

func newDashModel() model {
	return model{
		agent:    agent.New(llm.Stub{}, tool.NewRegistry(), ""),
		md:       newRenderer(80),
		input:    newInput(80),
		width:    80,
		showHome: true,
	}
}

func TestParseStatusCounts(t *testing.T) {
	var g gitInfo
	g.parseStatus("M  staged.go\n M unstaged.go\nMM both.go\n?? new.go\n")

	if g.staged != 2 { // "M " and "MM"
		t.Fatalf("staged = %d, want 2", g.staged)
	}
	if g.unstaged != 2 { // " M" and "MM"
		t.Fatalf("unstaged = %d, want 2", g.unstaged)
	}
	if g.untracked != 1 {
		t.Fatalf("untracked = %d, want 1", g.untracked)
	}
	if len(g.files) != 4 {
		t.Fatalf("files = %d, want 4: %v", len(g.files), g.files)
	}
}

func TestParseStatusCapsFileList(t *testing.T) {
	var g gitInfo
	var b strings.Builder
	for i := 0; i < maxDashboardFiles+5; i++ {
		b.WriteString(" M file\n")
	}
	g.parseStatus(b.String())
	if len(g.files) != maxDashboardFiles {
		t.Fatalf("files = %d, want capped at %d", len(g.files), maxDashboardFiles)
	}
}

func TestChangeSummary(t *testing.T) {
	if got := (gitInfo{}).changeSummary(); got != "clean" {
		t.Fatalf("empty summary = %q, want clean", got)
	}
	g := gitInfo{staged: 1, unstaged: 2, untracked: 3}
	got := g.changeSummary()
	for _, want := range []string{"1 staged", "2 modified", "3 untracked"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q missing %q", got, want)
		}
	}
}

func TestBranchLineAheadBehind(t *testing.T) {
	g := gitInfo{branch: "main", hasUpstream: true, ahead: 2, behind: 1}
	if got := g.branchLine(); !strings.Contains(got, "main") || !strings.Contains(got, "↑2") || !strings.Contains(got, "↓1") {
		t.Fatalf("branchLine = %q, want main ↑2 ↓1", got)
	}
	if got := (gitInfo{}).branchLine(); got != "(detached)" {
		t.Fatalf("empty branch = %q, want (detached)", got)
	}
}

func TestDashboardViewContent(t *testing.T) {
	m := newDashModel()
	m.gitReady = true
	m.git = gitInfo{isRepo: true, branch: "main", staged: 1, lastCommit: "abc123 do a thing (2 hours ago)"}

	got := m.dashboardView(80)
	for _, want := range []string{"directory", "branch", "provider", "main", "/dash", "ctrl+o", m.agent.ProviderName()} {
		if !strings.Contains(got, want) {
			t.Fatalf("dashboardView missing %q:\n%s", want, got)
		}
	}
}

func TestDashboardViewGitStates(t *testing.T) {
	m := newDashModel()
	if got := m.dashboardView(80); !strings.Contains(got, "loading") {
		t.Fatalf("before git gather, expected loading state:\n%s", got)
	}
	m.gitReady = true // not a repo
	if got := m.dashboardView(80); !strings.Contains(got, "not a git repository") {
		t.Fatalf("gitReady non-repo should report it:\n%s", got)
	}
}

func TestInitGathersGit(t *testing.T) {
	if newDashModel().Init() == nil {
		t.Fatal("Init should return a command to gather git state")
	}
	if _, ok := gatherGitCmd().(gitInfoMsg); !ok {
		t.Fatalf("gatherGitCmd should return a gitInfoMsg, got %T", gatherGitCmd())
	}
}

func TestGitInfoMsgStored(t *testing.T) {
	var tm tea.Model = newDashModel()
	tm, _ = tm.Update(gitInfoMsg{info: gitInfo{isRepo: true, branch: "topic"}})
	got := tm.(model)
	if !got.gitReady || !got.git.isRepo || got.git.branch != "topic" {
		t.Fatalf("gitInfoMsg not stored: gitReady=%v git=%+v", got.gitReady, got.git)
	}
}

func TestContentShowsDashboardWhenHome(t *testing.T) {
	var tm tea.Model = newDashModel()
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	got := tm.(model)
	if !strings.Contains(got.content(), "directory") {
		t.Fatalf("home screen should render the dashboard:\n%s", got.content())
	}
}

func TestTypingDismissesDashboard(t *testing.T) {
	var tm tea.Model = newDashModel()
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	tm, _ = tm.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	got := tm.(model)
	if got.showHome {
		t.Fatal("typing should dismiss the dashboard")
	}
	if strings.Contains(got.content(), "directory") {
		t.Fatalf("dashboard should be gone from the conversation pane after typing:\n%s", got.content())
	}
}

func TestDashCommandReopensDashboard(t *testing.T) {
	m := newDashModel()
	m.showHome = false
	status, cmd := m.handleCommand("/dash")
	if status != "" {
		t.Fatalf("/dash should reopen silently, got status %q", status)
	}
	if cmd == nil {
		t.Fatal("/dash should restart the planet animation tick")
	}
	if !m.showHome {
		t.Fatal("/dash should set showHome")
	}
}

func TestMainAreaClipsToHeight(t *testing.T) {
	m := newDashModel()
	m.height = 10 // main row = 10 - footerHeight
	lines := strings.Count(m.mainArea(), "\n") + 1
	if want := m.height - footerHeight; lines > want {
		t.Fatalf("mainArea = %d lines, want <= %d", lines, want)
	}
}

func TestPickTipInSet(t *testing.T) {
	tip := pickTip()
	if !slices.Contains(dashboardTips, tip) {
		t.Fatalf("pickTip returned %q, not in the tip set", tip)
	}
}
