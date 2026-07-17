package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/session"
)

// sessionStore opens the on-disk session store on first use and caches it.
func (m *model) sessionStore() (*session.Store, error) {
	if m.store != nil {
		return m.store, nil
	}
	s, err := session.Open()
	if err != nil {
		return nil, err
	}
	m.store = s
	return s, nil
}

// sessionAgents snapshots every workspace (or the single agent in the bare/test
// model) into the persisted agent shape.
func (m *model) sessionAgents() []session.Agent {
	if len(m.workspaces) == 0 {
		return []session.Agent{{Messages: m.agent.Snapshot()}}
	}
	agents := make([]session.Agent, 0, len(m.workspaces))
	for _, w := range m.workspaces {
		agents = append(agents, session.Agent{
			Label:    w.label,
			Cwd:      w.cwd,
			Worktree: w.worktree,
			Messages: w.agent.Snapshot(),
		})
	}
	return agents
}

// anyConversation reports whether any agent holds a real conversation.
func anyConversation(agents []session.Agent) bool {
	for _, a := range agents {
		if hasConversation(a.Messages) {
			return true
		}
	}
	return false
}

// buildSession assembles a Session recording every agent, with the active
// agent's transcript mirrored into Messages so listings and v1 readers still work.
func (m *model) buildSession(name string) *session.Session {
	agents := m.sessionAgents()
	active := m.active
	if active < 0 || active >= len(agents) {
		active = 0
	}
	title := session.TitleFromMessages(agents[active].Messages)
	if name == "" {
		name = session.NewID(title)
	}
	return &session.Session{
		ID:       name,
		Title:    title,
		Model:    m.agent.ProviderName(),
		Cwd:      m.sessionCwd(),
		Messages: agents[active].Messages,
		Agents:   agents,
		Active:   active,
	}
}

// sessionCwd is the folder a conversation is scoped to for /resume: the launch
// (primary) workspace's directory, falling back to the process cwd for the
// bare/test model.
func (m *model) sessionCwd() string {
	if len(m.workspaces) > 0 && m.workspaces[0].cwd != "" {
		return m.workspaces[0].cwd
	}
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return ""
}

// save writes the current agents to the sessions dir, reusing name when given,
// else the active session id, else a fresh timestamped id.
func (m *model) save(name string) string {
	store, err := m.sessionStore()
	if err != nil {
		return styleErr.Render("✗ sessions unavailable: " + err.Error())
	}
	if !anyConversation(m.sessionAgents()) {
		return styleErr.Render("✗ nothing to save yet")
	}
	if name == "" {
		name = m.sessionName
	}
	sess := m.buildSession(name)
	if prev, err := store.Load(sess.ID); err == nil {
		sess.CreatedAt = prev.CreatedAt // resave keeps the original creation time
	}
	if err := store.Save(sess); err != nil {
		return styleErr.Render("✗ save failed: " + err.Error())
	}
	m.sessionName = sess.ID
	m.notice = "✓ saved conversation as " + sess.ID
	return ""
}

// resume loads a saved session and rebuilds its agents, returning a command to
// refresh the restored active agent's git state. A multi-agent session is fully
// reconstructed (needs the agent factory); a single-agent or v1 session is
// loaded into the existing active agent.
func (m *model) resume(id string) tea.Cmd {
	store, err := m.sessionStore()
	if err != nil {
		m.pushText(styleErr.Render("✗ sessions unavailable: " + err.Error()))
		m.syncViewport()
		return nil
	}
	sess, err := store.Load(id)
	if err != nil {
		msg := "✗ resume failed: " + err.Error()
		if errors.Is(err, session.ErrNotFound) {
			msg = fmt.Sprintf("✗ no saved conversation %q — run /resume to list", id)
		}
		m.pushText(styleErr.Render(msg))
		m.syncViewport()
		return nil
	}
	agents := sess.AgentList()
	active := sess.Active
	if active < 0 || active >= len(agents) {
		active = 0
	}
	debug.Info(dbgTUI, "resume session", "id", sess.ID, "cwd", sess.Cwd, "agents", len(agents), "active", active)

	if m.newAgent == nil || len(agents) <= 1 {
		a := agents[active]
		m.agent.Load(a.Messages)
		m.rebuildFromMessages(a.Messages)
		m.history = historyFromMessages(a.Messages)
		m.histPos = len(m.history)
		debug.Debug(dbgTUI, "resume recall history", "mode", "single", "entries", len(m.history))
		if w := m.workspaceAt(m.active); w != nil {
			if a.Cwd != "" {
				w.cwd = a.Cwd
			}
			w.worktree = a.Worktree
			w.label = a.Label
			w.labeled = strings.TrimSpace(a.Label) != ""
		}
		m.sessionName = sess.ID
		m.notice = "✓ resumed " + sess.ID
		m.syncViewport()
		return m.gatherGit()
	}

	m.restoreAgents(agents, active)
	m.sessionName = sess.ID
	m.notice = "✓ resumed " + sess.ID
	m.syncViewport()
	return m.gatherGit()
}

// restoreAgents rebuilds every workspace from a saved multi-agent set and makes
// the recorded one active, reconstructing each visible transcript.
func (m *model) restoreAgents(agents []session.Agent, active int) {
	wss := make([]*workspace, 0, len(agents))
	for _, a := range agents {
		ag := m.newAgent()
		ag.Load(a.Messages)
		hist := historyFromMessages(a.Messages)
		wss = append(wss, &workspace{
			id:       m.nextID,
			cwd:      a.Cwd,
			worktree: a.Worktree,
			label:    a.Label,
			labeled:  strings.TrimSpace(a.Label) != "",
			agent:    ag,
			showHome: !hasConversation(a.Messages),
			history:  hist,
			histPos:  len(hist),
		})
		debug.Debug(dbgTUI, "resume recall history", "mode", "agent", "id", m.nextID, "entries", len(hist))
		m.nextID++
	}
	m.workspaces = wss
	for i, w := range wss {
		m.active = i
		m.unstash(i)
		m.showHome = w.showHome
		m.git = gitInfo{}
		m.gitReady = false
		m.tree = nil
		m.changes = nil
		m.rebuildFromMessages(w.agent.Snapshot())
		m.stash(i)
	}
	m.active = active
	m.unstash(active)
	m.tree = newFileTree(m.activeCwd())
	m.agentsCursor = active
}

// autosave persists all agents after a completed turn so an unexpected exit
// doesn't lose work. It runs by default (see autosaveEnabled).
func (m *model) autosave() {
	if !autosaveEnabled() {
		return
	}
	if !anyConversation(m.sessionAgents()) {
		return
	}
	store, err := m.sessionStore()
	if err != nil {
		return
	}
	sess := m.buildSession(m.sessionName)
	m.sessionName = sess.ID
	if prev, err := store.Load(m.sessionName); err == nil {
		sess.CreatedAt = prev.CreatedAt
	}
	_ = store.Save(sess)
}

// persistClosed rewrites the active saved session after an agent is closed so
// the removed agent doesn't reappear on resume. It only touches a session that
// was already saved (sessionName set) and, unlike autosave, doesn't require a
// remaining conversation — closing back down to empty agents still updates the file.
func (m *model) persistClosed() {
	if !autosaveEnabled() || m.sessionName == "" {
		return
	}
	store, err := m.sessionStore()
	if err != nil {
		return
	}
	sess := m.buildSession(m.sessionName)
	m.sessionName = sess.ID
	if prev, err := store.Load(m.sessionName); err == nil {
		sess.CreatedAt = prev.CreatedAt
	}
	_ = store.Save(sess)
}

// rebuildFromMessages replays a loaded transcript through the same render
// helpers the live stream uses, reconstructing the visible history — user
// turns, thinking, markdown (with code-copy affordances), tool calls, and tool
// results (with their [Show] output retained) — rather than swapping raw text.
func (m *model) rebuildFromMessages(msgs []llm.Message) {
	m.lines = nil
	m.outputs = nil
	m.codeBlocks = nil
	m.pendingTool = ""
	m.pendingArgs = ""
	m.streamText = ""
	m.streamThink = ""

	calls := make(map[string]llm.ToolCall) // tool-call id → call, to title results
	for _, msg := range msgs {
		switch msg.Role {
		case llm.RoleUser:
			m.pushText(m.renderUserLine(msg.Content, msg.Attachments, nil))
		case llm.RoleModel:
			if think := strings.TrimSpace(msg.Thinking); think != "" {
				m.pushText(m.renderThinkBox(think))
			}
			if text := strings.TrimSpace(msg.Content); text != "" {
				m.pushMarkdown(text)
			}
			for _, c := range msg.ToolCalls {
				calls[c.ID] = c
				m.pushText(renderToolCall(m.contentWidth(), c.Name, string(c.Args)))
			}
		case llm.RoleTool:
			if c, ok := calls[msg.ToolCallID]; ok {
				m.pendingTool, m.pendingArgs = c.Name, string(c.Args)
			} else {
				m.pendingTool, m.pendingArgs = msg.ToolName, ""
			}
			m.appendToolResult(agent.Event{Kind: "tool_result", Tool: msg.ToolName, Text: msg.Content})
		}
	}
}

// historyFromMessages rebuilds the input recall ring from a transcript's user
// turns so ctrl+p/ctrl+n walk back through them after a resume, matching
// recordHistory's rules: blanks are skipped and consecutive duplicates collapse.
func historyFromMessages(msgs []llm.Message) []string {
	var hist []string
	for _, msg := range msgs {
		if msg.Role != llm.RoleUser {
			continue
		}
		in := strings.TrimSpace(msg.Content)
		if in == "" {
			continue
		}
		if n := len(hist); n > 0 && hist[n-1] == in {
			continue
		}
		hist = append(hist, in)
	}
	return hist
}

// hasConversation reports whether msgs hold anything beyond the system prompt.
func hasConversation(msgs []llm.Message) bool {
	for _, msg := range msgs {
		if msg.Role != llm.RoleSystem {
			return true
		}
	}
	return false
}

// autosaveEnabled reports whether conversations are persisted automatically.
// Autosave is on by default; PLUTO_AUTOSAVE=off opts out.
func autosaveEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PLUTO_AUTOSAVE"))) {
	case "off", "0", "false", "no":
		return false
	}
	return true
}
