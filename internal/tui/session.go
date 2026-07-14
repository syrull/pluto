package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/syrull/pluto/internal/agent"
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

// save writes the current conversation to the sessions dir, reusing name when
// given, else the active session id, else a fresh timestamped id.
func (m *model) save(name string) string {
	store, err := m.sessionStore()
	if err != nil {
		return styleErr.Render("✗ sessions unavailable: " + err.Error())
	}
	msgs := m.agent.Snapshot()
	if !hasConversation(msgs) {
		return styleErr.Render("✗ nothing to save yet")
	}
	if name == "" {
		name = m.sessionName
	}
	title := session.TitleFromMessages(msgs)
	if name == "" {
		name = session.NewID(title)
	}
	sess := &session.Session{ID: name, Title: title, Model: m.agent.ProviderName(), Messages: msgs}
	if prev, err := store.Load(name); err == nil {
		sess.CreatedAt = prev.CreatedAt // resave keeps the original creation time
	}
	if err := store.Save(sess); err != nil {
		return styleErr.Render("✗ save failed: " + err.Error())
	}
	m.sessionName = sess.ID
	m.notice = "✓ saved conversation as " + sess.ID
	return ""
}

// resume loads a saved conversation, hands it to the agent, and rebuilds the
// visible transcript from its messages.
func (m *model) resume(id string) {
	store, err := m.sessionStore()
	if err != nil {
		m.pushText(styleErr.Render("✗ sessions unavailable: " + err.Error()))
		m.syncViewport()
		return
	}
	sess, err := store.Load(id)
	if err != nil {
		msg := "✗ resume failed: " + err.Error()
		if errors.Is(err, session.ErrNotFound) {
			msg = fmt.Sprintf("✗ no saved conversation %q — run /resume to list", id)
		}
		m.pushText(styleErr.Render(msg))
		m.syncViewport()
		return
	}
	m.agent.Load(sess.Messages)
	m.rebuildFromMessages(sess.Messages)
	m.sessionName = sess.ID
	m.notice = "✓ resumed " + sess.ID
	m.syncViewport()
}

// autosave persists the active conversation after a completed turn so an
// unexpected exit doesn't lose work. It runs by default (see autosaveEnabled).
func (m *model) autosave() {
	if !autosaveEnabled() {
		return
	}
	msgs := m.agent.Snapshot()
	if !hasConversation(msgs) {
		return
	}
	store, err := m.sessionStore()
	if err != nil {
		return
	}
	title := session.TitleFromMessages(msgs)
	if m.sessionName == "" {
		m.sessionName = session.NewID(title)
	}
	sess := &session.Session{ID: m.sessionName, Title: title, Model: m.agent.ProviderName(), Messages: msgs}
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
			m.pushText(m.renderUserLine(msg.Content, msg.Attachments...))
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
