package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTransportInference(t *testing.T) {
	cases := []struct {
		name string
		cfg  ServerConfig
		want Transport
	}{
		{"explicit stdio", ServerConfig{Type: "stdio", Command: "x"}, TransportStdio},
		{"explicit http", ServerConfig{Type: "http", URL: "https://x"}, TransportHTTP},
		{"explicit sse", ServerConfig{Type: "sse", URL: "https://x"}, TransportSSE},
		{"streamable-http alias", ServerConfig{Type: "streamable-http", URL: "https://x"}, TransportHTTP},
		{"inferred stdio from command", ServerConfig{Command: "npx"}, TransportStdio},
		{"inferred http from url", ServerConfig{URL: "https://x"}, TransportHTTP},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.Transport(); got != c.want {
				t.Fatalf("Transport() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     ServerConfig
		wantErr bool
	}{
		{"stdio ok", ServerConfig{Command: "npx", Args: []string{"x"}}, false},
		{"stdio missing command", ServerConfig{Type: "stdio"}, true},
		{"stdio with url", ServerConfig{Command: "npx", URL: "https://x"}, true},
		{"http ok", ServerConfig{URL: "https://x/mcp"}, false},
		{"http missing url", ServerConfig{Type: "http"}, true},
		{"http bad scheme", ServerConfig{Type: "http", URL: "ftp://x"}, true},
		{"http with command", ServerConfig{Type: "http", URL: "https://x", Command: "npx"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.Validate("srv")
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestConfigPathsPriority(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PLUTO_MCP_CONFIG", "/tmp/override.json")

	paths := ConfigPaths()
	if len(paths) != 3 {
		t.Fatalf("want 3 candidate paths, got %d: %v", len(paths), paths)
	}
	if paths[0] != "/tmp/override.json" {
		t.Errorf("override should come first, got %q", paths[0])
	}
	if paths[1] != filepath.Join(home, ".pluto", "mcp.json") {
		t.Errorf("second path = %q", paths[1])
	}
	if paths[2] != filepath.Join(home, ".config", "pluto", "mcp.json") {
		t.Errorf("third path = %q", paths[2])
	}
}

func TestConfigPathsXDG(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("PLUTO_MCP_CONFIG", "")

	paths := ConfigPaths()
	if paths[len(paths)-1] != filepath.Join(xdg, "pluto", "mcp.json") {
		t.Errorf("XDG path not honored, got %q", paths[len(paths)-1])
	}
}

func TestLoadNoConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PLUTO_MCP_CONFIG", "")

	cfg, path, err := Load()
	if err != nil {
		t.Fatalf("Load with no config should not error, got %v", err)
	}
	if path != "" || len(cfg.Servers) != 0 {
		t.Fatalf("expected empty load, got path=%q servers=%d", path, len(cfg.Servers))
	}
}

func TestLoadParsesConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PLUTO_MCP_CONFIG", "")

	dir := filepath.Join(home, ".pluto")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
	  "mcpServers": {
	    "fs": {"command": "npx", "args": ["-y", "server-filesystem", "/tmp"]},
	    "remote": {"type": "http", "url": "https://example.com/mcp", "headers": {"Authorization": "Bearer x"}},
	    "off": {"command": "x", "disabled": true}
	  }
	}`
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, path, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if path != filepath.Join(dir, "mcp.json") {
		t.Fatalf("path = %q", path)
	}
	if len(cfg.Servers) != 3 {
		t.Fatalf("want 3 servers, got %d", len(cfg.Servers))
	}
	if got := cfg.Servers["fs"].Transport(); got != TransportStdio {
		t.Errorf("fs transport = %q", got)
	}
	if got := cfg.Servers["remote"].Transport(); got != TransportHTTP {
		t.Errorf("remote transport = %q", got)
	}
	if !cfg.Servers["off"].Disabled {
		t.Errorf("off should be disabled")
	}
	names := cfg.Names()
	if len(names) != 3 || names[0] != "fs" || names[1] != "off" || names[2] != "remote" {
		t.Errorf("Names() not sorted as expected: %v", names)
	}
}

func TestLoadInvalidConfigErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PLUTO_MCP_CONFIG", "")
	dir := filepath.Join(home, ".pluto")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(); err == nil {
		t.Fatal("expected an error for malformed config")
	}
}

func TestDefaultConfigPathFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PLUTO_MCP_CONFIG", "")
	if got := DefaultConfigPath(); got != filepath.Join(home, ".pluto", "mcp.json") {
		t.Fatalf("DefaultConfigPath fallback = %q", got)
	}
}
