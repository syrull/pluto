package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestToolNameNamespacingAndSanitize(t *testing.T) {
	cases := []struct {
		server, remote, want string
	}{
		{"github", "create_issue", "mcp__github__create_issue"},
		{"my server", "do it!", "mcp__my_server__do_it_"},
		{"weird/name", "a.b", "mcp__weird_name__a_b"},
	}
	for _, c := range cases {
		if got := toolName(c.server, c.remote); got != c.want {
			t.Errorf("toolName(%q,%q) = %q, want %q", c.server, c.remote, got, c.want)
		}
	}
}

func TestToolNameTruncatedToLimit(t *testing.T) {
	long := strings.Repeat("x", 100)
	got := toolName("srv", long)
	if len(got) != maxToolNameLen {
		t.Fatalf("name length = %d, want %d", len(got), maxToolNameLen)
	}
}

func TestIsToolName(t *testing.T) {
	if !IsToolName("mcp__github__create_issue") {
		t.Error("namespaced MCP tool should be recognized")
	}
	if !IsToolName(toolName("srv", "tool")) {
		t.Error("toolName output must satisfy IsToolName")
	}
	for _, n := range []string{"bash", "read", "mcp", "mcp_x", "xmcp__y"} {
		if IsToolName(n) {
			t.Errorf("IsToolName(%q) = true, want false", n)
		}
	}
}

func TestSanitizeEmpty(t *testing.T) {
	if got := sanitize("///"); got != "___" {
		t.Fatalf("sanitize(///) = %q", got)
	}
	if got := sanitize(""); got != "server" {
		t.Fatalf("sanitize(empty) = %q, want fallback", got)
	}
}

func TestDescribeTagsServer(t *testing.T) {
	d := describe("gh", ToolInfo{Name: "x", Description: "make a PR"})
	if !strings.HasPrefix(d, "[MCP:gh]") || !strings.Contains(d, "make a PR") {
		t.Fatalf("describe = %q", d)
	}
	fallback := describe("gh", ToolInfo{Name: "x"})
	if !strings.Contains(fallback, "gh server") {
		t.Fatalf("fallback describe = %q", fallback)
	}
}

func TestToolExecuteRoundTrip(t *testing.T) {
	c := newTestClient(t, []ToolInfo{{Name: "search"}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.initialize(ctx, "1"); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	mt := newTool(c, "test", ToolInfo{Name: "search", Description: "find", InputSchema: json.RawMessage(`{"type":"object"}`)})

	if mt.Name() != "mcp__test__search" {
		t.Errorf("Name() = %q", mt.Name())
	}
	if string(mt.Schema()) != `{"type":"object"}` {
		t.Errorf("Schema() = %q", mt.Schema())
	}
	out, err := mt.Execute(ctx, json.RawMessage(`{"q":"x"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "echo:search" {
		t.Fatalf("Execute result = %q", out)
	}
}

func TestFlattenContent(t *testing.T) {
	res := callResult{}
	res.Content = append(res.Content, struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: "text", Text: "hello"})
	res.Content = append(res.Content, struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: "image", Text: ""})
	got := flattenContent(res.Content)
	if !strings.Contains(got, "hello") || !strings.Contains(got, "[image content omitted]") {
		t.Fatalf("flattenContent = %q", got)
	}
}
