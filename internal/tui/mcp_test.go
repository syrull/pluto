package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/mcp"
	"github.com/syrull/pluto/internal/tool"
)

// mcpModel builds a bare model backed by the offline stub provider.
func mcpModel() model {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	return model{agent: ag, md: newRenderer(80), input: newInput(80)}
}

func TestInstallMCPStartsTurn(t *testing.T) {
	m := mcpModel()
	status, cmd := m.handleCommand("/install-mcp https://github.com/owner/repo")
	if cmd == nil {
		t.Fatal("/install-mcp should return a run command")
	}
	if !m.busy {
		t.Fatal("/install-mcp should start a turn (busy)")
	}
	if m.showHome {
		t.Fatal("/install-mcp should dismiss the home dashboard")
	}
	if !strings.Contains(status, "install-mcp") {
		t.Fatalf("status should confirm the install, got %q", status)
	}
}

func TestInstallMCPAcceptsShorthand(t *testing.T) {
	m := mcpModel()
	_, cmd := m.handleCommand("/install-mcp owner/repo")
	if cmd == nil || !m.busy {
		t.Fatal("owner/repo shorthand should start a turn")
	}
}

func TestInstallMCPRequiresRepo(t *testing.T) {
	m := mcpModel()
	status, cmd := m.handleCommand("/install-mcp")
	if cmd != nil || m.busy {
		t.Fatal("/install-mcp with no repo must not start a turn")
	}
	if !strings.Contains(status, "usage") {
		t.Fatalf("expected a usage hint, got %q", status)
	}
}

func TestInstallMCPRejectsGarbage(t *testing.T) {
	m := mcpModel()
	status, cmd := m.handleCommand("/install-mcp not-a-repo")
	if cmd != nil || m.busy {
		t.Fatal("a bad repo reference must not start a turn")
	}
	if !strings.Contains(status, "GitHub repository") {
		t.Fatalf("expected a repo-format hint, got %q", status)
	}
}

func TestMCPStatusEmpty(t *testing.T) {
	m := mcpModel()
	status, cmd := m.handleCommand("/mcp")
	if cmd != nil {
		t.Fatal("/mcp should not start a turn")
	}
	if !strings.Contains(status, "no MCP servers configured") {
		t.Fatalf("empty /mcp should say none configured, got %q", status)
	}
}

func TestMCPStatusListsServers(t *testing.T) {
	m := mcpModel()
	m.mcpInfo = mcp.Summary{
		ConfigPath: "/home/u/.pluto/mcp.json",
		Servers:    1,
		Tools:      2,
		Failed:     []string{"broken"},
		Statuses: []mcp.ServerStatus{
			{Name: "github", Transport: "stdio", Tools: []string{"create_issue", "list_prs"}},
			{Name: "broken", Transport: "http", Err: "connect failed"},
			{Name: "old", Transport: "stdio", Disabled: true},
		},
	}
	status, cmd := m.handleCommand("/mcp")
	if cmd != nil {
		t.Fatal("/mcp should not start a turn")
	}
	for _, want := range []string{
		"/home/u/.pluto/mcp.json", "github", "create_issue", "list_prs",
		"broken", "connect failed", "old", "disabled", "1 connected", "2 tool(s)", "1 failed",
	} {
		if !strings.Contains(status, want) {
			t.Fatalf("/mcp output missing %q:\n%s", want, status)
		}
	}
}

func TestSkillsStatusEmpty(t *testing.T) {
	m := mcpModel()
	m.workspaces = []*workspace{{id: 0, cwd: t.TempDir()}}
	status, cmd := m.handleCommand("/skills")
	if cmd != nil {
		t.Fatal("/skills should not start a turn")
	}
	if !strings.Contains(status, "no skills found") {
		t.Fatalf("empty /skills should say none found, got %q", status)
	}
}

func TestSkillsStatusListsSkills(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "tidy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: tidy\ndescription: clean up imports and formatting\n---\nRun gofmt.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m := mcpModel()
	m.workspaces = []*workspace{{id: 0, cwd: dir}}
	status, cmd := m.handleCommand("/skills")
	if cmd != nil {
		t.Fatal("/skills should not start a turn")
	}
	for _, want := range []string{"skills (1)", "tidy", "clean up imports"} {
		if !strings.Contains(status, want) {
			t.Fatalf("/skills output missing %q:\n%s", want, status)
		}
	}
}

func TestApprovalPromptRendersMCPCall(t *testing.T) {
	req := &approvalRequest{
		call: llm.ToolCall{Name: "mcp__github__create_issue", Args: json.RawMessage(`{"title":"x"}`)},
		rr:   agent.ReviewResult{Source: "mcp", Pattern: "mcp__github__create_issue"},
	}
	out := renderApprovalPrompt(80, req)
	if !strings.Contains(out, "MCP tool") {
		t.Fatalf("MCP approval prompt missing its header: %q", out)
	}
	if !strings.Contains(out, "mcp__github__create_issue") {
		t.Fatal("MCP approval prompt should name the tool and allow pattern")
	}
}

func TestLooksLikeRepo(t *testing.T) {
	ok := []string{
		"https://github.com/owner/repo",
		"http://github.com/owner/repo",
		"git@github.com:owner/repo.git",
		"owner/repo",
	}
	for _, s := range ok {
		if !looksLikeRepo(s) {
			t.Errorf("looksLikeRepo(%q) = false, want true", s)
		}
	}
	bad := []string{"", "just-a-word", "a/b/c", "has space", "/leadingslash", "trailing/"}
	for _, s := range bad {
		if looksLikeRepo(s) {
			t.Errorf("looksLikeRepo(%q) = true, want false", s)
		}
	}
}
