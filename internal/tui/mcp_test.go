package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
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

func TestApprovalPromptRendersMCPCall(t *testing.T) {
	req := &approvalRequest{
		call: llm.ToolCall{Name: "mcp__github__create_issue", Args: json.RawMessage(`{"title":"x"}`)},
		rr:   agent.ReviewResult{Source: "mcp", Pattern: "mcp__github__create_issue"},
	}
	out := renderApprovalPrompt(80, 24, req)
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
