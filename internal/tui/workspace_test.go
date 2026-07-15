package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/session"
	"github.com/syrull/pluto/internal/tool"
)

// multiModel builds a model with n workspaces backed by stub agents and a factory
// for spawning more, with workspace 0 active.
func multiModel(n int) *model {
	mk := func() *agent.Agent { return agent.New(llm.Stub{}, tool.NewRegistry(), "") }
	m := &model{md: newRenderer(80), width: 80, height: 24, input: newInput(80), newAgent: mk}
	for i := 0; i < n; i++ {
		m.workspaces = append(m.workspaces, &workspace{id: i, agent: mk(), showHome: true})
	}
	m.nextID = n
	m.active = 0
	m.unstash(0)
	return m
}

func TestSpawnCreatesClearedWorkspace(t *testing.T) {
	m := multiModel(1)
	m.lines = []entry{{text: "agent 0 work"}}

	m.spawn(t.TempDir(), false)

	if len(m.workspaces) != 2 {
		t.Fatalf("spawn should add a workspace, got %d", len(m.workspaces))
	}
	if m.active != 1 {
		t.Fatalf("spawn should switch to the new agent, active = %d", m.active)
	}
	if !m.showHome {
		t.Fatal("a new agent should open on a cleared dashboard")
	}
	if len(m.lines) != 0 {
		t.Fatalf("new agent should start with an empty transcript, got %d lines", len(m.lines))
	}
	if got := m.workspaces[0].lines; len(got) != 1 || got[0].text != "agent 0 work" {
		t.Fatalf("previous agent's transcript should be retained, got %+v", got)
	}
	if m.workspaces[0].agent == m.workspaces[1].agent {
		t.Fatal("each workspace must own a distinct agent instance")
	}
}

func TestSwitchRetainsEachTranscript(t *testing.T) {
	m := multiModel(2)
	m.lines = []entry{{text: "A"}}

	m.switchTo(1)
	if len(m.lines) != 0 {
		t.Fatalf("switching to a fresh agent should show its own transcript, got %d lines", len(m.lines))
	}
	m.lines = []entry{{text: "B"}}

	m.switchTo(0)
	if len(m.lines) != 1 || m.lines[0].text != "A" {
		t.Fatalf("agent 0 transcript should be restored, got %+v", m.lines)
	}
	m.switchTo(1)
	if len(m.lines) != 1 || m.lines[0].text != "B" {
		t.Fatalf("agent 1 transcript should be restored, got %+v", m.lines)
	}
}

func TestBackgroundEventDoesNotDisturbActive(t *testing.T) {
	m := multiModel(2)
	m.lines = []entry{{text: "active line"}}

	updated, _ := (*m).Update(eventMsg{id: 1, Kind: "text", Text: "background reply"})
	got := updated.(model)

	if len(got.lines) != 1 || got.lines[0].text != "active line" {
		t.Fatalf("a background event must not change the active transcript, got %+v", got.lines)
	}
	w := got.workspaces[1]
	if len(w.lines) == 0 {
		t.Fatal("the background workspace should record its own event")
	}
	if !w.unread {
		t.Fatal("a background event should flag the workspace unread")
	}
}

func TestNewClearsOnlyActiveAgent(t *testing.T) {
	m := multiModel(2)
	m.lines = []entry{{text: "active"}}
	m.workspaces[1].lines = []entry{{text: "other agent's work"}}

	m.handleCommand("/new")

	if len(m.lines) != 0 {
		t.Fatalf("/new should clear the active transcript, got %+v", m.lines)
	}
	if got := m.workspaces[1].lines; len(got) != 1 || got[0].text != "other agent's work" {
		t.Fatalf("/new must not touch other agents, got %+v", got)
	}
}

func TestPromptNewAgentOutsideRepoSpawnsDirectly(t *testing.T) {
	m := multiModel(1)
	m.git = gitInfo{isRepo: false}

	cmd := m.promptNewAgent()

	if m.picker != nil {
		t.Fatal("outside a repo, new agent should spawn without offering a worktree")
	}
	if len(m.workspaces) != 2 {
		t.Fatalf("promptNewAgent should spawn a workspace, got %d", len(m.workspaces))
	}
	_ = cmd
}

func TestPromptNewAgentInRepoOffersWorktree(t *testing.T) {
	m := multiModel(1)
	m.git = gitInfo{isRepo: true, root: "/repo"}

	m.promptNewAgent()

	if m.picker == nil || m.pickerKind != pickerNewAgent {
		t.Fatal("inside a repo, new agent should offer a worktree picker")
	}
	if len(m.workspaces) != 1 {
		t.Fatal("offering the picker should not spawn yet")
	}
}

func TestToggleCollapse(t *testing.T) {
	m := multiModel(1)
	m.focus = paneAgents
	if _, _ = m.paneKey("-"); m.collapsedAgents != true {
		t.Fatal("- should collapse the focused Agents pane")
	}
	if _, _ = m.paneKey("-"); m.collapsedAgents != false {
		t.Fatal("- should expand a collapsed pane")
	}
}

func TestFocusOrderReachesAgents(t *testing.T) {
	m := multiModel(1)
	order := m.focusOrder()
	// Tab follows the sidebar's visual top-to-bottom order (Agents, Files, ...),
	// so a single Tab out of the chat lands on Agents.
	if order[0] != paneChat || order[1] != paneAgents {
		t.Fatalf("tab order should start chat→agents, got %v", order)
	}
	if order[2] != paneTree {
		t.Fatalf("tab order should reach Files after Agents, got %v", order)
	}
}

func TestBackgroundGitInfoDoesNotClobberActive(t *testing.T) {
	m := multiModel(2)
	m.workspaces[0].cwd = "/repoA"
	m.workspaces[1].cwd = "/repoB"

	bg := gitInfo{isRepo: true, root: "/repoB", status: map[string]string{"/repoB/new.txt": "??"}}
	updated, _ := (*m).Update(gitInfoMsg{info: bg, dir: "/repoB"})
	got := updated.(model)

	if len(got.git.status) != 0 {
		t.Fatalf("a background agent's git gather must not touch the active view, got %+v", got.git.status)
	}
	if len(got.workspaces[1].git.status) != 1 {
		t.Fatalf("the background workspace should receive its own git state, got %+v", got.workspaces[1].git)
	}
}

func TestActiveGitInfoAppliesToActive(t *testing.T) {
	m := multiModel(1)
	m.workspaces[0].cwd = "/repoA"

	updated, _ := (*m).Update(gitInfoMsg{info: gitInfo{isRepo: true, root: "/repoA", branch: "main"}, dir: "/repoA"})
	got := updated.(model)

	if !got.gitReady || got.git.branch != "main" {
		t.Fatalf("a gather for the active cwd should update the active view, got %+v", got.git)
	}
}

func TestActiveAgentRowShowsLiveBusy(t *testing.T) {
	m := multiModel(1)
	m.busy = true // active agent is generating; its workspace copy isn't stashed yet
	m.focus = paneAgents

	if body := m.agentsBody(32, 4); !strings.Contains(body, "working") {
		t.Fatalf("active row should reflect live busy state, got:\n%s", body)
	}
}

func TestBackgroundCompletionAutosavesRealActive(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	m := multiModel(2)
	m.workspaces[0].agent.Load([]llm.Message{{Role: llm.RoleUser, Content: "agent zero task"}})
	m.workspaces[1].agent.Load([]llm.Message{{Role: llm.RoleUser, Content: "agent one task"}})
	m.workspaces[1].busy = true

	updated, _ := (*m).Update(doneMsg{id: 1})
	got := updated.(model)

	if got.active != 0 {
		t.Fatalf("a background completion should leave the active agent unchanged, got %d", got.active)
	}
	store, err := session.Open()
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	sess, err := store.Load(got.sessionName)
	if err != nil {
		t.Fatalf("autosave should persist a loadable session: %v", err)
	}
	if sess.Active != 0 {
		t.Fatalf("autosaved Active = %d, want 0 (the user-facing active)", sess.Active)
	}
	if len(sess.Agents) != 2 {
		t.Fatalf("autosave should record both agents, got %d", len(sess.Agents))
	}
}

func TestResumeReturnsGitRefresh(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	m := multiModel(1)
	m.workspaces[0].agent.Load([]llm.Message{{Role: llm.RoleUser, Content: "save me"}})
	if s := m.save(""); s != "" {
		t.Fatalf("save failed: %s", s)
	}

	if cmd := m.resume(m.sessionName); cmd == nil {
		t.Fatal("resume should return a command to refresh git state")
	}
}

func TestCreateWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "t@example.com")
	runGit(t, repo, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "init")
	root := runGit(t, repo, "rev-parse", "--show-toplevel")

	m := &model{git: gitInfo{isRepo: true, root: root}, nextID: 2}
	path, err := m.createWorktree(2)
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("worktree dir not created at %q: %v", path, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return trimNL(string(out))
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

var _ tea.Model = model{}
