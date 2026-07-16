package tui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/session"
	"github.com/syrull/pluto/internal/tool"
)

func seededAgent(msgs []llm.Message) *agent.Agent {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	ag.Load(msgs)
	return ag
}

func conversation() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Content: "read the file please"},
		{
			Role:      llm.RoleModel,
			Content:   "sure thing",
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Args: json.RawMessage(`{"path":"main.go"}`)}},
		},
		{Role: llm.RoleTool, ToolName: "read", ToolCallID: "c1", Content: "package main\nfunc main() {}"},
		{Role: llm.RoleModel, Content: "done reading"},
	}
}

func TestSaveThenResumeReconstructsTranscript(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())

	m := &model{agent: seededAgent(conversation()), md: newRenderer(80), width: 80}
	if status, cmd := m.handleCommand("/save mywork"); status != "" || cmd != nil {
		t.Fatalf("/save should succeed quietly, got status %q cmd %v", status, cmd)
	}
	if !strings.Contains(m.notice, "mywork") {
		t.Fatalf("save notice = %q, want it to mention the name", m.notice)
	}

	// A fresh model + agent resumes into a reconstructed transcript.
	m2 := &model{agent: seededAgent(nil), md: newRenderer(80), width: 80}
	m2.resume("mywork")

	joined := m2.transcript()
	for _, want := range []string{"read the file please", "read", "main.go", "done reading"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("resumed transcript missing %q:\n%s", want, joined)
		}
	}
	// The read's full output is retained behind a [Show] modal, not shown inline.
	if len(m2.outputs) != 1 || !strings.Contains(m2.outputs[0].full, "package main") {
		t.Fatalf("resumed read output not retained: %+v", m2.outputs)
	}
	// The agent transcript is restored so the next turn continues the conversation.
	if got := len(m2.agent.Snapshot()); got != 4 {
		t.Fatalf("resumed agent transcript = %d msgs, want 4", got)
	}
	if m2.sessionName != "mywork" {
		t.Fatalf("sessionName = %q, want mywork", m2.sessionName)
	}
}

func TestHistoryFromMessages(t *testing.T) {
	got := historyFromMessages([]llm.Message{
		{Role: llm.RoleSystem, Content: "system prompt"},
		{Role: llm.RoleUser, Content: "first ask"},
		{Role: llm.RoleModel, Content: "an answer"},
		{Role: llm.RoleTool, ToolName: "read", Content: "file body"},
		{Role: llm.RoleUser, Content: "  second ask  "}, // trimmed
		{Role: llm.RoleUser, Content: "second ask"},     // consecutive dup collapses
		{Role: llm.RoleUser, Content: "   "},            // blank skipped
		{Role: llm.RoleUser, Content: "third ask"},
	})
	want := []string{"first ask", "second ask", "third ask"}
	if !slices.Equal(got, want) {
		t.Fatalf("historyFromMessages = %v, want %v", got, want)
	}
}

func TestResumeRestoresRecallHistory(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())

	convo := []llm.Message{
		{Role: llm.RoleUser, Content: "list the files"},
		{Role: llm.RoleModel, Content: "here they are"},
		{Role: llm.RoleUser, Content: "now open main.go"},
		{Role: llm.RoleModel, Content: "opened"},
	}
	m := &model{agent: seededAgent(convo), md: newRenderer(80), width: 80}
	if status, _ := m.handleCommand("/save recallwork"); status != "" {
		t.Fatalf("/save failed: %q", status)
	}

	m2 := &model{agent: seededAgent(nil), md: newRenderer(80), input: newInput(80), width: 80}
	m2.resume("recallwork")

	want := []string{"list the files", "now open main.go"}
	if !slices.Equal(m2.history, want) {
		t.Fatalf("resumed recall history = %v, want %v", m2.history, want)
	}
	if m2.histPos != len(m2.history) {
		t.Fatalf("resumed histPos = %d, want %d (not navigating)", m2.histPos, len(m2.history))
	}

	// ctrl+p/ctrl+n now walk the resumed conversation's user turns.
	var tm tea.Model = *m2
	tm = pressCtrl(tm, 'p')
	if got := tm.(model).input.Value(); got != "now open main.go" {
		t.Fatalf("ctrl+p after resume = %q, want the last user turn", got)
	}
	tm = pressCtrl(tm, 'p')
	if got := tm.(model).input.Value(); got != "list the files" {
		t.Fatalf("second ctrl+p after resume = %q, want the first user turn", got)
	}
}

func TestResumeMultiAgentRestoresPerAgentHistory(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())

	m := multiModel(2)
	m.workspaces[0].agent.Load([]llm.Message{{Role: llm.RoleUser, Content: "agent zero ask"}})
	m.workspaces[1].agent.Load([]llm.Message{{Role: llm.RoleUser, Content: "agent one ask"}})
	if s := m.save(""); s != "" {
		t.Fatalf("save failed: %s", s)
	}

	m2 := multiModel(1)
	m2.resume(m.sessionName)

	if len(m2.workspaces) != 2 {
		t.Fatalf("resume should restore 2 workspaces, got %d", len(m2.workspaces))
	}
	if got := m2.workspaces[0].history; !slices.Equal(got, []string{"agent zero ask"}) {
		t.Fatalf("agent 0 recall history = %v, want [agent zero ask]", got)
	}
	if got := m2.workspaces[1].history; !slices.Equal(got, []string{"agent one ask"}) {
		t.Fatalf("agent 1 recall history = %v, want [agent one ask]", got)
	}
	// The active workspace's history is live on the model for ctrl+p.
	if got := m2.history; !slices.Equal(got, m2.workspaces[m2.active].history) {
		t.Fatalf("active model history = %v, want the active workspace's %v", got, m2.workspaces[m2.active].history)
	}
}

func TestResumeRecallHistoryDebugLog(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	read := enableTUILog(t, "debug")

	m := &model{agent: seededAgent([]llm.Message{{Role: llm.RoleUser, Content: "remember this"}}), md: newRenderer(80), width: 80}
	m.handleCommand("/save logged")

	m2 := &model{agent: seededAgent(nil), md: newRenderer(80), input: newInput(80), width: 80}
	m2.resume("logged")

	if out := read(); !strings.Contains(out, "resume recall history") {
		t.Errorf("debug log should record recall-history restoration:\n%s", out)
	}
}

func TestRebuildFromMessagesReconstructsVisibleTranscript(t *testing.T) {
	m := &model{md: newRenderer(80), width: 80}
	m.rebuildFromMessages([]llm.Message{
		{Role: llm.RoleSystem, Content: "system prompt should not show"},
		{Role: llm.RoleUser, Content: "hello agent"},
		{
			Role:      llm.RoleModel,
			Thinking:  "let me think about it",
			Content:   "here is the plan",
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)}},
		},
		{Role: llm.RoleTool, ToolName: "bash", ToolCallID: "c1", Content: "file1\nfile2"},
	})

	joined := m.transcript()
	if strings.Contains(joined, "system prompt should not show") {
		t.Fatalf("system prompt must not appear in the visible transcript:\n%s", joined)
	}
	for _, want := range []string{"hello agent", "Thinking", "let me think about it", "here is the plan", "bash", "ls", "file1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rebuilt transcript missing %q:\n%s", want, joined)
		}
	}
}

func TestSaveNothingToSave(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	m := &model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), "sys"), md: newRenderer(80)}

	status, _ := m.handleCommand("/save x")
	if !strings.Contains(status, "nothing to save") {
		t.Fatalf("/save with no conversation = %q, want a 'nothing to save' error", status)
	}
}

func TestResumeListsSavedInPicker(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	m := &model{agent: seededAgent([]llm.Message{{Role: llm.RoleUser, Content: "hello there"}}), md: newRenderer(80), width: 80, height: 24}
	m.handleCommand("/save first")

	status, _ := m.handleCommand("/resume")
	if status != "" {
		t.Fatalf("/resume should open a picker silently, got %q", status)
	}
	if m.picker == nil || m.pickerKind != pickerResume {
		t.Fatal("/resume should open the resume picker")
	}
	if got := m.picker.Selected(); got != "first" {
		t.Fatalf("resume picker should list the saved session, got %q", got)
	}
	if !strings.Contains(m.picker.View(), "first") {
		t.Fatalf("resume picker view should show the session:\n%s", m.picker.View())
	}
}

func TestResumeScopedToCurrentFolder(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())

	ag := seededAgent([]llm.Message{{Role: llm.RoleUser, Content: "in folder a"}})
	m := &model{
		agent: ag, md: newRenderer(80), width: 80, height: 24,
		workspaces: []*workspace{{id: 0, cwd: "/folder/a", agent: ag}}, active: 0,
	}
	if status, _ := m.handleCommand("/save mine"); status != "" {
		t.Fatalf("/save failed: %q", status)
	}

	// A conversation recorded in another folder must not show up by default.
	store, _ := session.Open()
	if err := store.Save(&session.Session{ID: "elsewhere", Cwd: "/folder/b", Messages: conversation()}); err != nil {
		t.Fatal(err)
	}

	m.handleCommand("/resume")
	if m.picker == nil {
		t.Fatal("/resume should open a picker")
	}
	if view := m.picker.View(); !strings.Contains(view, "mine") || strings.Contains(view, "elsewhere") {
		t.Fatalf("/resume should list only this folder's conversation, got:\n%s", view)
	}

	m.handleCommand("/resume --all")
	if view := m.picker.View(); !strings.Contains(view, "mine") || !strings.Contains(view, "elsewhere") {
		t.Fatalf("/resume --all should list every folder's conversation, got:\n%s", view)
	}
}

func TestResumeMissingReportsError(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	m := &model{agent: seededAgent(nil), md: newRenderer(80), width: 80}

	m.resume("ghost")
	if joined := m.transcript(); !strings.Contains(joined, "no saved conversation") {
		t.Fatalf("resume(missing) should report a clear error, got:\n%s", joined)
	}
}

func TestResumeBareOpensPicker(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	m := &model{agent: seededAgent([]llm.Message{{Role: llm.RoleUser, Content: "hi"}}), md: newRenderer(80), width: 80}
	m.handleCommand("/save one")

	status, _ := m.handleCommand("/resume")
	if status != "" || m.picker == nil || m.pickerKind != pickerResume {
		t.Fatalf("bare /resume should open the resume picker, got status %q picker %v kind %v", status, m.picker, m.pickerKind)
	}
	if m.picker.Selected() != "one" {
		t.Fatalf("resume picker selected = %q, want 'one'", m.picker.Selected())
	}
}

func TestResumeBareNoSessions(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	m := &model{agent: seededAgent(nil), md: newRenderer(80)}

	status, _ := m.handleCommand("/resume")
	if !strings.Contains(status, "no saved conversations") {
		t.Fatalf("/resume with no sessions = %q", status)
	}
	if m.picker != nil {
		t.Fatal("no picker should open when there are no sessions")
	}
}

func TestNewClearsSessionName(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	m := &model{agent: seededAgent([]llm.Message{{Role: llm.RoleUser, Content: "hi"}}), md: newRenderer(80)}
	m.handleCommand("/save keep")
	if m.sessionName == "" {
		t.Fatal("expected sessionName set after /save")
	}
	m.handleCommand("/new")
	if m.sessionName != "" {
		t.Fatalf("/new should clear sessionName, got %q", m.sessionName)
	}
}

func TestAutosaveOnDoneByDefault(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	t.Setenv("PLUTO_AUTOSAVE", "") // unset: autosave is on by default

	ag := seededAgent([]llm.Message{{Role: llm.RoleUser, Content: "remember me"}})
	var tm tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80), busy: true}

	tm, _ = tm.Update(doneMsg{})
	got := tm.(model)

	if got.sessionName == "" {
		t.Fatal("autosave should assign a session name on the first completed turn")
	}
	store, err := session.Open()
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	if _, err := store.Load(got.sessionName); err != nil {
		t.Fatalf("autosave did not persist a loadable session: %v", err)
	}
}

func TestNoAutosaveWhenOptedOut(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	t.Setenv("PLUTO_AUTOSAVE", "off")

	ag := seededAgent([]llm.Message{{Role: llm.RoleUser, Content: "do not persist"}})
	var tm tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80), busy: true}

	tm, _ = tm.Update(doneMsg{})
	got := tm.(model)

	if got.sessionName != "" {
		t.Fatalf("PLUTO_AUTOSAVE=off should not assign a session name, got %q", got.sessionName)
	}
	store, _ := session.Open()
	if metas, _ := store.List(); len(metas) != 0 {
		t.Fatalf("PLUTO_AUTOSAVE=off should write nothing, found %d sessions", len(metas))
	}
}
