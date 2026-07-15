package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/session"
)

// worktreeOption and currentDirOption are the choices offered when spawning a
// new agent.
const (
	worktreeOption   = "create a git worktree (isolated)"
	currentDirOption = "use the current directory"
)

// Choices offered by the close-agent confirmation picker.
const (
	closeConfirmOption        = "close the agent"
	closeRemoveWorktreeOption = "close and remove the worktree"
	closeKeepWorktreeOption   = "close and keep the worktree"
	closeCancelOption         = "cancel"
)

// workspaceAt returns the workspace at index i, or nil when out of range.
func (m *model) workspaceAt(i int) *workspace {
	if i >= 0 && i < len(m.workspaces) {
		return m.workspaces[i]
	}
	return nil
}

// workspaceIndex returns the position of the workspace with id, or -1.
func (m *model) workspaceIndex(id int) int {
	for i, w := range m.workspaces {
		if w.id == id {
			return i
		}
	}
	return -1
}

// activeID returns the id of the active workspace, or 0 in the bare/test model.
func (m *model) activeID() int {
	if w := m.workspaceAt(m.active); w != nil {
		return w.id
	}
	return 0
}

// activeCwd returns the active agent's working directory, falling back to the
// process cwd when no workspace carries one (the bare/test model).
func (m *model) activeCwd() string {
	if w := m.workspaceAt(m.active); w != nil && w.cwd != "" {
		return w.cwd
	}
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return ""
}

// stash copies the model's live per-agent state into workspace i. Identity and
// pane markers (id/label/cwd/worktree/labeled/unread) live only on the workspace
// and are left untouched.
func (m *model) stash(i int) {
	w := m.workspaceAt(i)
	if w == nil {
		return
	}
	w.agent = m.agent
	w.busy = m.busy
	w.events = m.events
	w.cancel = m.cancel
	w.showHome = m.showHome
	w.git = m.git
	w.gitReady = m.gitReady
	w.tree = m.tree
	w.changes = m.changes
	w.finder = m.finder
	w.finderBase = m.finderBase
	w.lines = m.lines
	w.outputs = m.outputs
	w.codeBlocks = m.codeBlocks
	w.streamText = m.streamText
	w.streamThink = m.streamThink
	w.pendingTool = m.pendingTool
	w.pendingArgs = m.pendingArgs
}

// unstash loads workspace i's per-agent state into the model's live fields.
func (m *model) unstash(i int) {
	w := m.workspaceAt(i)
	if w == nil {
		return
	}
	m.agent = w.agent
	m.busy = w.busy
	m.events = w.events
	m.cancel = w.cancel
	m.showHome = w.showHome
	m.git = w.git
	m.gitReady = w.gitReady
	m.tree = w.tree
	m.changes = w.changes
	m.finder = w.finder
	m.finderBase = w.finderBase
	m.lines = w.lines
	m.outputs = w.outputs
	m.codeBlocks = w.codeBlocks
	m.streamText = w.streamText
	m.streamThink = w.streamThink
	m.pendingTool = w.pendingTool
	m.pendingArgs = w.pendingArgs
}

// onWorkspace runs fn with the model's per-agent fields temporarily loaded from
// workspace i, restoring the active workspace afterward. When i is already active
// fn runs directly. This lets the existing single-workspace handlers mutate a
// background workspace without a second code path. While swapped in, syncViewport
// is suppressed so a background handler can't repaint the visible transcript with
// the background agent's conversation.
func (m *model) onWorkspace(i int, fn func()) {
	if i == m.active || m.workspaceAt(i) == nil {
		fn()
		return
	}
	saved := m.active
	prevSwapped := m.swapped
	m.swapped = true
	m.stash(saved)
	m.active = i
	m.unstash(i)
	fn() // sees m.active == i, so activeID/activeCwd resolve to workspace i
	m.stash(i)
	m.active = saved
	m.unstash(saved)
	m.swapped = prevSwapped
}

// switchTo makes workspace i active: the visible transcript, sidebar, and
// dashboard all swap to it while the others keep running. It returns a command
// to refresh git state for the new agent's directory.
func (m *model) switchTo(i int) tea.Cmd {
	if i < 0 || i >= len(m.workspaces) || i == m.active {
		return nil
	}
	debug.Info(dbgTUI, "switch agent", "from", m.active, "to", i, "cwd", m.workspaces[i].cwd)
	m.stash(m.active)
	m.active = i
	m.unstash(i)
	m.workspaces[i].unread = false
	m.agentsCursor = i
	m.finder = nil
	if m.tree == nil {
		m.tree = newFileTree(m.activeCwd())
	}
	if m.tree != nil && m.gitReady {
		m.tree.SetStatus(m.buildStatusStyles())
	}
	if m.changes == nil && m.focus == paneChanges {
		m.focus = paneTree
	}
	m.notice = fmt.Sprintf("✓ switched to %s", m.workspaceLabel(i))
	m.syncViewport()
	cmds := []tea.Cmd{m.gatherGit()}
	// Restart the planet loop under a fresh epoch when landing on a dashboard.
	if m.showHome {
		m.orbitEpoch++
		cmds = append(cmds, orbitTick(m.orbitEpoch))
	}
	return tea.Batch(cmds...)
}

// spawn creates a new workspace rooted at cwd (worktree marks a git worktree),
// switches to its cleared dashboard, and leaves the previous agent running.
func (m *model) spawn(cwd string, worktree bool) tea.Cmd {
	if m.newAgent == nil {
		m.notice = "✗ cannot create agents in this build"
		return nil
	}
	debug.Info(dbgTUI, "spawn agent", "id", m.nextID, "cwd", cwd, "worktree", worktree)
	m.stash(m.active)
	id := m.nextID
	m.nextID++
	ws := &workspace{id: id, cwd: cwd, worktree: worktree, agent: m.newAgent(), showHome: true}
	m.workspaces = append(m.workspaces, ws)
	m.active = len(m.workspaces) - 1
	m.unstash(m.active)
	m.showHome = true
	m.finder = nil
	m.tree = newFileTree(m.activeCwd())
	m.changes = nil
	m.git = gitInfo{}
	m.gitReady = false
	m.agentsCursor = m.active
	if m.focus == paneChanges {
		m.focus = paneTree
	}
	m.notice = fmt.Sprintf("✓ created agent %d", m.active+1)
	m.orbitEpoch++
	m.syncViewport()
	return tea.Batch(orbitTick(m.orbitEpoch), m.gatherGit())
}

// promptNewAgent starts the new-agent flow: outside a repo it spawns in the
// current directory; inside one it offers a worktree via a picker.
func (m *model) promptNewAgent() tea.Cmd {
	if m.newAgent == nil {
		m.notice = "✗ cannot create agents in this build"
		return nil
	}
	if !m.git.isRepo {
		return m.spawn(m.activeCwd(), false)
	}
	m.picker = newNewAgentPicker()
	m.picker.SetSize(m.width, m.height)
	m.pickerKind = pickerNewAgent
	return nil
}

// applyNewAgentPick acts on the new-agent picker choice, creating a worktree when
// asked (falling back to the current directory if git fails).
func (m *model) applyNewAgentPick(target string) tea.Cmd {
	if target != worktreeOption {
		return m.spawn(m.activeCwd(), false)
	}
	path, err := m.createWorktree(m.nextID)
	if err != nil {
		m.notice = "✗ worktree failed: " + err.Error()
		return m.spawn(m.activeCwd(), false)
	}
	return m.spawn(path, true)
}

// createWorktree adds a git worktree on a fresh branch at a sibling of the repo
// root and returns its path. A short time-based suffix keeps the branch/path
// unique so a leftover branch from a prior session (agent ids reset on restart)
// doesn't collide.
func (m *model) createWorktree(id int) (string, error) {
	root := m.git.root
	if root == "" {
		return "", fmt.Errorf("not a git repository")
	}
	suffix := strconv.FormatInt(time.Now().Unix(), 36)
	branch := fmt.Sprintf("pluto-agent-%d-%s", id, suffix)
	path := filepath.Join(filepath.Dir(root), fmt.Sprintf("%s-agent-%d-%s", filepath.Base(root), id, suffix))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "worktree", "add", "-b", branch, path).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return path, nil
}

// promptClose starts the close flow for the active agent. It closes straight
// away when nothing is at risk; otherwise (busy, dirty tree, or an owned
// worktree) it opens a confirmation picker.
func (m *model) promptClose() tea.Cmd {
	if len(m.workspaces) == 0 {
		debug.Debug(dbgTUI, "close prompt", "outcome", "no-agent")
		m.notice = "✗ no agent to close"
		return nil
	}
	w := m.workspaces[m.active]
	if !m.busy && !w.worktree && len(m.git.status) == 0 {
		debug.Debug(dbgTUI, "close prompt", "outcome", "direct", "id", w.id)
		return m.closeActiveAgent(false)
	}
	debug.Debug(dbgTUI, "close prompt", "outcome", "confirm", "id", w.id,
		"busy", m.busy, "worktree", w.worktree, "dirty", len(m.git.status))
	m.picker = newCloseAgentPicker(w.worktree)
	m.picker.SetSize(m.width, m.height)
	m.pickerKind = pickerCloseAgent
	return nil
}

// applyClosePick acts on the close confirmation choice.
func (m *model) applyClosePick(target string) tea.Cmd {
	debug.Debug(dbgTUI, "close pick", "choice", target)
	switch target {
	case closeCancelOption:
		m.notice = "✗ close canceled"
		return nil
	case closeRemoveWorktreeOption:
		return m.closeActiveAgent(true)
	default:
		return m.closeActiveAgent(false)
	}
}

// closeActiveAgent tears down the active agent: it cancels any in-flight run,
// optionally removes its git worktree, drops the workspace, and switches to a
// neighbor. Closing the only remaining agent resets it to a fresh default rather
// than leaving the app with zero agents.
func (m *model) closeActiveAgent(removeWorktree bool) tea.Cmd {
	if len(m.workspaces) == 0 {
		return nil
	}
	idx := m.active
	w := m.workspaces[idx]
	debug.Info(dbgTUI, "close agent", "id", w.id, "idx", idx, "worktree", w.worktree, "remove", removeWorktree)

	if m.busy {
		m.interrupt()
		m.busy = false
	}

	removeErr := ""
	if removeWorktree && w.worktree && w.cwd != "" {
		path := w.cwd
		t := debug.NewTimer(dbgTUI, "worktree remove")
		if err := removeWorktreeAt(path); err != nil {
			removeErr = err.Error()
			debug.Warn(dbgTUI, "worktree remove failed", "path", path, "err", err)
			t.Stop("path", path, "outcome", "error")
		} else {
			w.worktree = false
			w.cwd = ""
			t.Stop("path", path, "outcome", "ok")
		}
	}

	label := m.workspaceLabel(idx)

	if len(m.workspaces) == 1 {
		debug.Info(dbgTUI, "close agent done", "id", w.id, "mode", "reset-last",
			"worktree_removed", removeWorktree && removeErr == "")
		return m.resetLastAgent(label, removeErr)
	}

	m.workspaces = slices.Delete(m.workspaces, idx, idx+1)
	next := idx - 1
	if next < 0 {
		next = 0 // closed the first agent: land on the new first
	}
	m.active = next
	m.unstash(next)
	m.workspaces[next].unread = false
	m.agentsCursor = next
	m.finder = nil
	m.tree = newFileTree(m.activeCwd())
	m.changes = nil
	m.git = gitInfo{}
	m.gitReady = false
	if m.focus == paneChanges {
		m.focus = paneTree
	}
	m.notice = closeNotice(label, removeErr)
	m.orbitEpoch++
	m.syncViewport()
	debug.Info(dbgTUI, "close agent done", "id", w.id, "mode", "switch",
		"active", next, "remaining", len(m.workspaces), "worktree_removed", removeWorktree && removeErr == "")
	// Drop the closed agent from the persisted session so it doesn't resume.
	m.persistClosed()
	cmds := []tea.Cmd{m.gatherGit()}
	if m.showHome {
		cmds = append(cmds, orbitTick(m.orbitEpoch))
	}
	return tea.Batch(cmds...)
}

// resetLastAgent clears the sole remaining agent back to a fresh conversation and
// dashboard, mirroring /new, so closing it never leaves zero agents. A removed
// worktree drops the agent back to the process directory.
func (m *model) resetLastAgent(label, removeErr string) tea.Cmd {
	if w := m.workspaceAt(m.active); w != nil {
		w.label = ""
		w.labeled = false
		if w.cwd == "" {
			if d, err := os.Getwd(); err == nil {
				w.cwd = d
			}
		}
	}
	m.agent.Reset()
	m.lines = nil
	m.outputs = nil
	m.codeBlocks = nil
	m.pendingTool = ""
	m.pendingArgs = ""
	m.streamText = ""
	m.streamThink = ""
	m.sessionName = ""
	m.showHome = true
	m.finder = nil
	m.tree = newFileTree(m.activeCwd())
	m.changes = nil
	m.git = gitInfo{}
	m.gitReady = false
	if m.focus == paneChanges {
		m.focus = paneTree
	}
	m.orbitEpoch++
	if removeErr != "" {
		m.notice = fmt.Sprintf("✗ closed %s but worktree removal failed: %s — reset to a fresh agent", label, removeErr)
	} else {
		m.notice = fmt.Sprintf("✓ closed %s — reset to a fresh agent", label)
	}
	m.syncViewport()
	return tea.Batch(orbitTick(m.orbitEpoch), m.gatherGit())
}

// closeNotice builds the transient notice after closing an agent, folding in a
// worktree-removal failure when one occurred.
func closeNotice(label, removeErr string) string {
	if removeErr != "" {
		return fmt.Sprintf("✗ closed %s but worktree removal failed: %s", label, removeErr)
	}
	return fmt.Sprintf("✓ closed %s", label)
}

// removeWorktreeAt removes the git worktree rooted at path (force-removing it so
// a dirty tree is still cleaned up once the user has confirmed).
func removeWorktreeAt(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", path, "worktree", "remove", "--force", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// gatherGit refreshes git state for the active agent's directory.
func (m *model) gatherGit() tea.Cmd { return gatherGitCmdIn(m.activeCwd()) }

// applyGitInfo stores a gathered git snapshot. A tagged gather whose directory
// isn't the active agent's cwd (a background agent's, or a stale result from a
// since-switched agent) updates only that workspace's own git state, so it never
// clobbers the foreground Files/Changes view. Otherwise it updates the active
// view and rebuilds the sidebar.
func (m *model) applyGitInfo(msg gitInfoMsg) {
	if msg.dir != "" && len(m.workspaces) > 0 && msg.dir != m.activeCwd() {
		for _, w := range m.workspaces {
			if w.cwd == msg.dir {
				w.git = msg.info
				w.gitReady = true
				debug.Debug(dbgTUI, "git info (background)", "dir", msg.dir, "changes", len(msg.info.status))
			}
		}
		return
	}
	m.git = msg.info
	m.gitReady = true
	if m.tree != nil {
		m.tree.SetStatus(m.buildStatusStyles())
	}
	m.changes = m.buildChangesList()
	debug.Info(dbgTUI, "changes pane refreshed", "dir", msg.dir, "repo", msg.info.isRepo,
		"changed", len(msg.info.status), "present", m.changes != nil,
		"staged", msg.info.staged, "unstaged", msg.info.unstaged, "untracked", msg.info.untracked)
	if m.changes == nil && m.focus == paneChanges {
		m.focus = paneTree
		debug.Debug(dbgTUI, "focus fell back to files (changes gone)")
	}
}

// workspaceLabel returns the display label for workspace i: its auto-label, or
// "Agent N" until one is assigned.
func (m *model) workspaceLabel(i int) string {
	w := m.workspaceAt(i)
	if w == nil {
		return fmt.Sprintf("Agent %d", i+1)
	}
	if strings.TrimSpace(w.label) != "" {
		return w.label
	}
	return fmt.Sprintf("Agent %d", i+1)
}

// maybeLabel requests an auto-label for workspace i after its first completed
// turn, deriving one from the first message when no summarizer is wired.
func (m *model) maybeLabel(i int) tea.Cmd {
	w := m.workspaceAt(i)
	if w == nil || w.labeled {
		return nil
	}
	msgs := w.agent.Snapshot()
	first := session.TitleFromMessages(msgs)
	if first == "" || first == "untitled" {
		return nil
	}
	w.labeled = true
	id := w.id
	summarize := m.summarize
	return func() tea.Msg {
		if summarize != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if s, err := summarize(ctx, labelPrompt(first)); err == nil {
				if s = shortLabel(s); s != "" {
					return labelMsg{id: id, label: s}
				}
			}
		}
		return labelMsg{id: id, label: shortLabel(first)}
	}
}

// labelPrompt asks a summarizer for a terse tab-style label for a conversation.
func labelPrompt(firstMessage string) string {
	return "In 2-4 words, give a short tab title for a coding task that starts with " +
		"this request. Reply with only the title, no punctuation:\n\n" + firstMessage
}

// shortLabel trims a candidate label to a compact single line.
func shortLabel(s string) string {
	s = strings.TrimSpace(oneLine(s))
	s = strings.Trim(s, "\"'.")
	const max = 28
	r := []rune(s)
	if len(r) > max {
		s = strings.TrimSpace(string(r[:max])) + "…"
	}
	return s
}
