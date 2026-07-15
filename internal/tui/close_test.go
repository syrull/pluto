package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/session"
)

func TestCloseActiveAgentSwitchesToPrevious(t *testing.T) {
	m := multiModel(2)
	m.switchTo(1)
	closedID := m.workspaces[1].id

	m.closeActiveAgent(false)

	if len(m.workspaces) != 1 {
		t.Fatalf("closing should remove one agent, got %d", len(m.workspaces))
	}
	if m.active != 0 {
		t.Fatalf("closing should land on the previous agent, active = %d", m.active)
	}
	if m.workspaces[0].id == closedID {
		t.Fatal("the closed agent should be gone from the list")
	}
	if m.agentsCursor != 0 {
		t.Fatalf("cursor should follow the new active agent, got %d", m.agentsCursor)
	}
}

func TestCloseFirstAgentLandsOnNext(t *testing.T) {
	m := multiModel(2)
	next := m.workspaces[1].id // closing the first should land here

	m.closeActiveAgent(false)

	if len(m.workspaces) != 1 {
		t.Fatalf("closing should remove one agent, got %d", len(m.workspaces))
	}
	if m.active != 0 || m.workspaces[0].id != next {
		t.Fatalf("closing the first agent should land on the next, got id %d", m.workspaces[0].id)
	}
}

func TestCloseLastAgentResetsToFresh(t *testing.T) {
	m := multiModel(1)
	m.lines = []entry{{text: "old work"}}
	m.showHome = false
	m.workspaces[0].label = "some task"
	m.workspaces[0].labeled = true
	m.workspaces[0].agent.Load([]llm.Message{{Role: llm.RoleUser, Content: "old work"}})

	m.closeActiveAgent(false)

	if len(m.workspaces) != 1 {
		t.Fatalf("closing the last agent must not leave zero agents, got %d", len(m.workspaces))
	}
	if len(m.lines) != 0 {
		t.Fatalf("the reset agent should have an empty transcript, got %+v", m.lines)
	}
	if !m.showHome {
		t.Fatal("closing the last agent should return to the dashboard")
	}
	if m.workspaces[0].label != "" || m.workspaces[0].labeled {
		t.Fatal("the reset agent should drop its label")
	}
	if hasConversation(m.workspaces[0].agent.Snapshot()) {
		t.Fatal("the reset agent should start from a fresh conversation")
	}
}

func TestCloseCancelsInFlightRun(t *testing.T) {
	m := multiModel(2)
	canceled := false
	m.busy = true
	m.cancel = func() { canceled = true }

	m.closeActiveAgent(false)

	if !canceled {
		t.Fatal("closing a busy agent should cancel its in-flight run")
	}
	if m.busy {
		t.Fatal("closing should clear the busy flag")
	}
}

func TestPromptCloseSkipsConfirmationWhenClean(t *testing.T) {
	m := multiModel(2)

	if cmd := m.promptClose(); cmd == nil {
		t.Fatal("closing a clean agent should return a teardown command")
	}
	if m.picker != nil {
		t.Fatal("a clean agent should close without a confirmation prompt")
	}
	if len(m.workspaces) != 1 {
		t.Fatalf("promptClose should close directly, got %d agents", len(m.workspaces))
	}
}

func TestPromptCloseConfirmsWorktree(t *testing.T) {
	m := multiModel(2)
	m.workspaces[0].worktree = true

	m.promptClose()

	if m.picker == nil || m.pickerKind != pickerCloseAgent {
		t.Fatal("closing a worktree agent should open the close confirmation picker")
	}
	if len(m.workspaces) != 2 {
		t.Fatal("offering the confirmation should not close yet")
	}
}

func TestPromptCloseConfirmsDirtyTree(t *testing.T) {
	m := multiModel(2)
	m.git = gitInfo{isRepo: true, status: map[string]string{"f.txt": " M"}}

	m.promptClose()

	if m.picker == nil || m.pickerKind != pickerCloseAgent {
		t.Fatal("closing an agent with uncommitted changes should ask for confirmation")
	}
}

func TestPromptCloseConfirmsBusyAgent(t *testing.T) {
	m := multiModel(2)
	m.busy = true

	m.promptClose()

	if m.picker == nil || m.pickerKind != pickerCloseAgent {
		t.Fatal("closing a busy agent should ask for confirmation")
	}
}

func TestApplyClosePickCancelKeepsAgent(t *testing.T) {
	m := multiModel(2)

	if cmd := m.applyClosePick(closeCancelOption); cmd != nil {
		t.Fatal("cancelling the close should be a no-op")
	}
	if len(m.workspaces) != 2 {
		t.Fatalf("cancelling should keep every agent, got %d", len(m.workspaces))
	}
}

func TestCloseDropsAgentFromSession(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	m := multiModel(2)
	m.workspaces[0].agent.Load([]llm.Message{{Role: llm.RoleUser, Content: "keep me"}})
	m.workspaces[1].agent.Load([]llm.Message{{Role: llm.RoleUser, Content: "close me"}})
	if s := m.save(""); s != "" {
		t.Fatalf("save failed: %s", s)
	}

	m.switchTo(1)
	m.closeActiveAgent(false)

	store, err := session.Open()
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	sess, err := store.Load(m.sessionName)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if len(sess.Agents) != 1 {
		t.Fatalf("closed agent should be dropped from the session, got %d agents", len(sess.Agents))
	}
	if got := sess.Agents[0].Messages; len(got) == 0 || got[0].Content != "keep me" {
		t.Fatalf("the remaining agent's transcript should persist, got %+v", got)
	}
}

func TestRemoveWorktreeAt(t *testing.T) {
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
	// A dirty file forces --force, which removeWorktreeAt uses.
	if err := os.WriteFile(filepath.Join(path, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeWorktreeAt(path); err != nil {
		t.Fatalf("removeWorktreeAt: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone, stat err = %v", err)
	}
}

func TestAgentsPaneCloseKey(t *testing.T) {
	m := multiModel(2)
	m.focus = paneAgents
	m.agentsCursor = 0

	handled, _ := m.agentsKey("d")

	if !handled {
		t.Fatal("d in the Agents pane should be handled")
	}
	if len(m.workspaces) != 1 {
		t.Fatalf("d should close the agent under the cursor, got %d", len(m.workspaces))
	}
}

func TestAgentsPaneCloseKeyIgnoresNewRow(t *testing.T) {
	m := multiModel(2)
	m.focus = paneAgents
	m.agentsCursor = len(m.workspaces) // the "＋ new agent" row

	m.agentsKey("d")

	if len(m.workspaces) != 2 {
		t.Fatal("d on the new-agent row should not close anything")
	}
}
