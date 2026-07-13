package tui

import (
	"encoding/json"
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

func TestSessionsListsSaved(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	m := &model{agent: seededAgent([]llm.Message{{Role: llm.RoleUser, Content: "hello there"}}), md: newRenderer(80), width: 80}
	m.handleCommand("/save first")

	status, _ := m.handleCommand("/sessions")
	if !strings.Contains(status, "1 saved") || !strings.Contains(status, "first") || !strings.Contains(status, "hello there") {
		t.Fatalf("/sessions output missing expected fields:\n%s", status)
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

func TestAutosaveOnDoneWhenEnabled(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	t.Setenv("PLUTO_AUTOSAVE", "on")

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

func TestNoAutosaveWhenDisabled(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	t.Setenv("PLUTO_AUTOSAVE", "")

	ag := seededAgent([]llm.Message{{Role: llm.RoleUser, Content: "do not persist"}})
	var tm tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80), busy: true}

	tm, _ = tm.Update(doneMsg{})
	got := tm.(model)

	if got.sessionName != "" {
		t.Fatalf("autosave disabled should not assign a session name, got %q", got.sessionName)
	}
	store, _ := session.Open()
	if metas, _ := store.List(); len(metas) != 0 {
		t.Fatalf("autosave disabled should write nothing, found %d sessions", len(metas))
	}
}
