package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/tool"
)

// TestHelperProcess is not a real test: when MCP_HELPER=1 the test binary
// re-executes itself as a minimal stdio MCP server (see the manager success
// test), then exits before the test framework can print to the protocol stdout.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("MCP_HELPER") != "1" {
		return
	}
	fakeStdioServer(os.Stdin, os.Stdout, []ToolInfo{
		{Name: "alpha", Description: "first tool"},
		{Name: "beta", Description: "second tool"},
	})
	os.Exit(0)
}

// writeConfig writes an mcp.json under a temp HOME and points the loader at it.
func writeConfig(t *testing.T, servers map[string]ServerConfig) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PLUTO_MCP_CONFIG", "")
	dir := filepath.Join(home, ".pluto")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(Config{Servers: servers})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestManagerLoadNoConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PLUTO_MCP_CONFIG", "")

	reg := tool.NewRegistry()
	mgr := New("test")
	defer mgr.Close()
	s := mgr.Load(context.Background(), reg)
	if s.Servers != 0 || s.Tools != 0 || len(s.Failed) != 0 || s.ConfigPath != "" {
		t.Fatalf("expected empty summary, got %+v", s)
	}
}

func TestManagerLoadRegistersStdioServer(t *testing.T) {
	writeConfig(t, map[string]ServerConfig{
		"helper": {
			Command: os.Args[0],
			Args:    []string{"-test.run=TestHelperProcess", "--"},
			Env:     map[string]string{"MCP_HELPER": "1"},
		},
	})
	reg := tool.NewRegistry()
	mgr := New("test")
	defer mgr.Close()

	s := mgr.Load(context.Background(), reg)
	if s.Servers != 1 {
		t.Fatalf("want 1 server, got %d (failed: %v)", s.Servers, s.Failed)
	}
	if s.Tools != 2 {
		t.Fatalf("want 2 tools, got %d", s.Tools)
	}
	if _, ok := reg.Lookup("mcp__helper__alpha"); !ok {
		t.Errorf("alpha tool not registered")
	}
	if _, ok := reg.Lookup("mcp__helper__beta"); !ok {
		t.Errorf("beta tool not registered")
	}
	if len(s.Statuses) != 1 {
		t.Fatalf("want 1 server status, got %d", len(s.Statuses))
	}
	st := s.Statuses[0]
	if st.Name != "helper" || st.Transport != "stdio" || st.Err != "" || st.Disabled {
		t.Fatalf("unexpected status %+v", st)
	}
	if len(st.Tools) != 2 || st.Tools[0] != "alpha" || st.Tools[1] != "beta" {
		t.Fatalf("want [alpha beta] tool names, got %v", st.Tools)
	}
}

func TestManagerProgressAnnouncesLoad(t *testing.T) {
	writeConfig(t, map[string]ServerConfig{
		"off":    {Command: "does-not-matter", Disabled: true},
		"broken": {Command: "pluto-nonexistent-mcp-binary-xyz"},
	})
	var buf strings.Builder
	mgr := New("test").WithProgress(&buf)
	defer mgr.Close()

	mgr.Load(context.Background(), tool.NewRegistry())
	// The notice counts only the one enabled server, not the disabled one.
	if got := buf.String(); !strings.Contains(got, "loading 1 MCP server(s)") {
		t.Fatalf("progress notice = %q, want it to announce 1 server", got)
	}
}

func TestManagerProgressQuietWithoutConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PLUTO_MCP_CONFIG", "")

	var buf strings.Builder
	mgr := New("test").WithProgress(&buf)
	defer mgr.Close()
	mgr.Load(context.Background(), tool.NewRegistry())
	if buf.Len() != 0 {
		t.Fatalf("no config should stay quiet, got %q", buf.String())
	}
}

func TestManagerSkipsDisabledAndReportsFailures(t *testing.T) {
	writeConfig(t, map[string]ServerConfig{
		"off":    {Command: "does-not-matter", Disabled: true},
		"broken": {Command: "pluto-nonexistent-mcp-binary-xyz"},
	})
	reg := tool.NewRegistry()
	mgr := New("test")
	defer mgr.Close()

	s := mgr.Load(context.Background(), reg)
	if s.Servers != 0 {
		t.Fatalf("no server should connect, got %d", s.Servers)
	}
	if len(s.Failed) != 1 || s.Failed[0] != "broken" {
		t.Fatalf("expected only 'broken' to fail, got %v", s.Failed)
	}
	if len(reg.Tools()) != 0 {
		t.Fatalf("no tools should be registered, got %d", len(reg.Tools()))
	}
	byName := map[string]ServerStatus{}
	for _, st := range s.Statuses {
		byName[st.Name] = st
	}
	if off := byName["off"]; !off.Disabled || off.Err != "" {
		t.Fatalf("disabled server status = %+v, want Disabled with no error", off)
	}
	if broken := byName["broken"]; broken.Disabled || broken.Err == "" {
		t.Fatalf("failed server status = %+v, want a non-empty error and not disabled", broken)
	}
}

func TestManagerCloseNoClients(t *testing.T) {
	New("test").Close() // must not panic
}
