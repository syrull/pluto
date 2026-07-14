package tui

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/syrull/pluto/internal/tui/widgets"
)

// maxDashboardFiles caps the changed-file list shown on the dashboard.
const maxDashboardFiles = 6

// dashboardContextFiles are the instruction files the dashboard reports as
// injected into the system prompt when present (mirrors main.contextFiles).
var dashboardContextFiles = []string{"CLAUDE.md", "AGENTS.md"}

// orbitInterval is how often the home-screen planet animation advances a frame.
const orbitInterval = 90 * time.Millisecond

// orbitTickMsg advances the planet animation. epoch fences stale tick loops so
// only the most recently started loop keeps running.
type orbitTickMsg struct{ epoch int }

func orbitTick(epoch int) tea.Cmd {
	return tea.Tick(orbitInterval, func(time.Time) tea.Msg { return orbitTickMsg{epoch: epoch} })
}

var dashboardTips = []string{
	"steer the agent mid-turn — type while it's working to nudge it.",
	"ctrl+t toggles mouse capture so you can select text natively.",
	"/think high spends more reasoning budget on hard problems.",
	"ctrl+o reopens the last tool output; ctrl+y copies the last code block.",
	"/resume brings back a past conversation — sessions autosave by default.",
	"/model switches models without losing the conversation.",
}

// gitInfo is the git-derived project state shown on the dashboard.
type gitInfo struct {
	isRepo      bool
	root        string
	branch      string
	hasUpstream bool
	ahead       int
	behind      int
	staged      int
	unstaged    int
	untracked   int
	files       []string
	lastCommit  string
	status      map[string]string // abs path → porcelain XY code, for tree markers
}

// gitInfoMsg delivers gathered git state to the model.
type gitInfoMsg gitInfo

// gatherGitCmd collects git state off the UI goroutine so startup isn't blocked.
func gatherGitCmd() tea.Msg { return gitInfoMsg(gatherGit()) }

func gitRun(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	return string(out), err
}

func gatherGit() gitInfo {
	var g gitInfo
	if out, err := gitRun("rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return g
	}
	g.isRepo = true
	if out, err := gitRun("rev-parse", "--show-toplevel"); err == nil {
		g.root = strings.TrimSpace(out)
	}
	if out, err := gitRun("branch", "--show-current"); err == nil {
		g.branch = strings.TrimSpace(out)
	}
	if out, err := gitRun("rev-list", "--left-right", "--count", "@{upstream}...HEAD"); err == nil {
		if parts := strings.Fields(out); len(parts) == 2 {
			g.behind, _ = strconv.Atoi(parts[0])
			g.ahead, _ = strconv.Atoi(parts[1])
			g.hasUpstream = true
		}
	}
	if out, err := gitRun("status", "--porcelain"); err == nil {
		g.parseStatus(out)
		g.status = statusMap(out, g.root)
	}
	if out, err := gitRun("log", "-1", "--format=%h %s (%cr)"); err == nil {
		g.lastCommit = strings.TrimSpace(out)
	}
	return g
}

// parseStatus tallies staged/unstaged/untracked counts and keeps a short list of
// changed files from `git status --porcelain` output.
func (g *gitInfo) parseStatus(out string) {
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(line) < 3 {
			continue
		}
		x, y := line[0], line[1]
		switch {
		case x == '?' && y == '?':
			g.untracked++
		default:
			if x != ' ' {
				g.staged++
			}
			if y != ' ' {
				g.unstaged++
			}
		}
		if len(g.files) < maxDashboardFiles {
			g.files = append(g.files, strings.TrimSpace(line))
		}
	}
}

func (g gitInfo) branchLine() string {
	s := g.branch
	if s == "" {
		s = "(detached)"
	}
	if g.hasUpstream && (g.ahead > 0 || g.behind > 0) {
		s += fmt.Sprintf(" ↑%d ↓%d", g.ahead, g.behind)
	}
	return s
}

func (g gitInfo) changeSummary() string {
	var parts []string
	if g.staged > 0 {
		parts = append(parts, fmt.Sprintf("%d staged", g.staged))
	}
	if g.unstaged > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", g.unstaged))
	}
	if g.untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", g.untracked))
	}
	if len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, " · ")
}

// dashboardView renders the dashboard panels, wrapped to width.
func (m model) dashboardView(width int) string {
	d := widgets.Dashboard{
		Banner: widgets.Planet(m.orbitFrame, stylePlanet, stylePlanetMoon),
		Rows:   append(m.projectRows(), m.sessionRows()...),
		Lines:  helpLines(),
		Footer: m.tipLine(),
		Width:  width,
		Center: true,
		Style: widgets.DashboardStyle{
			Banner: lipgloss.NewStyle(), // the planet art is already colored
			Label:  styleHint,
			Value:  styleModel,
			Border: styleHint,
			Muted:  styleHint,
		},
	}
	return d.View()
}

func (m model) projectRows() []widgets.DashRow {
	var rows []widgets.DashRow
	if cwd := shortCwd(); cwd != "" {
		rows = append(rows, widgets.DashRow{Label: "directory", Value: cwd})
	}
	switch {
	case m.git.isRepo:
		rows = append(rows, widgets.DashRow{Label: "branch", Value: m.git.branchLine()})
		if m.git.lastCommit != "" {
			rows = append(rows, widgets.DashRow{Label: "last commit", Value: m.git.lastCommit})
		}
		rows = append(rows, widgets.DashRow{Label: "changes", Value: m.git.changeSummary()})
	case m.gitReady:
		rows = append(rows, widgets.DashRow{Label: "git", Value: "not a git repository"})
	default:
		rows = append(rows, widgets.DashRow{Label: "git", Value: "loading…"})
	}
	return rows
}

func (m model) sessionRows() []widgets.DashRow {
	name := "no provider"
	if m.agent != nil {
		name = m.agent.ProviderName()
	}
	rows := []widgets.DashRow{{Label: "provider", Value: name}}
	if m.agent != nil {
		if th, ok := m.agent.Thinker(); ok {
			v := "off"
			if lvl := th.ThinkLevel(); lvl.Thinking() {
				v = string(lvl)
			}
			rows = append(rows, widgets.DashRow{Label: "thinking", Value: v})
		}
		if ctrl, ok := m.agent.Auto(); ok {
			v := "off"
			if ctrl.AutoEnabled() {
				v = "on · judge " + ctrl.JudgeName()
			}
			rows = append(rows, widgets.DashRow{Label: "auto mode", Value: v})
		}
		if used, window, ok := m.agent.ContextUsage(); ok && window > 0 {
			rows = append(rows, widgets.DashRow{Label: "context", Value: fmt.Sprintf("%d%% / %s", used*100/window, formatTokens(window))})
		}
	}
	rows = append(rows, widgets.DashRow{Label: "context files", Value: contextFilesLine()})
	return rows
}

func helpLines() []string {
	return []string{
		"/new  /dash  /save  /resume  /model  /think  /auto  /gh  /login",
		"ctrl+o output · ctrl+y copy · ctrl+t mouse · ctrl+c quit",
	}
}

func (m model) tipLine() string {
	if m.tip == "" {
		return ""
	}
	return "tip: " + m.tip
}

// contextFilesLine reports which project instruction files are present and
// non-empty in the working directory, or "none".
func contextFilesLine() string {
	var found []string
	for _, name := range dashboardContextFiles {
		if info, err := os.Stat(name); err == nil && !info.IsDir() && info.Size() > 0 {
			found = append(found, name)
		}
	}
	if len(found) == 0 {
		return "none"
	}
	return strings.Join(found, ", ")
}

func pickTip() string {
	if len(dashboardTips) == 0 {
		return ""
	}
	return dashboardTips[rand.IntN(len(dashboardTips))]
}
