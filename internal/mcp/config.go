// Package mcp connects pluto to Model Context Protocol servers — local
// subprocesses (stdio transport) and remote HTTP endpoints (Streamable HTTP /
// SSE transport) — and exposes each server's tools to the agent through the
// shared tool registry. Servers are declared in a JSON config file (mcp.json)
// resolved from the user's config directory; see Load and Manager.
package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/syrull/pluto/internal/debug"
)

// FileName is the config file every candidate directory is probed for.
const FileName = "mcp.json"

// Transport identifies how pluto talks to a server.
type Transport string

const (
	// TransportStdio spawns a local subprocess and speaks newline-delimited
	// JSON-RPC over its stdin/stdout.
	TransportStdio Transport = "stdio"
	// TransportHTTP POSTs JSON-RPC to a remote endpoint (Streamable HTTP).
	TransportHTTP Transport = "http"
	// TransportSSE is the legacy HTTP+SSE transport; treated like TransportHTTP
	// by the client, which negotiates JSON or an event stream per response.
	TransportSSE Transport = "sse"
)

// ServerConfig declares one MCP server. It mirrors the widely used mcp.json /
// Claude Desktop shape: local servers set command/args/env, remote servers set
// url/headers. type is optional and inferred from those fields when omitted.
type ServerConfig struct {
	Type     string            `json:"type,omitempty"`
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	URL      string            `json:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Disabled bool              `json:"disabled,omitempty"`
}

// Config is the parsed mcp.json document.
type Config struct {
	Servers map[string]ServerConfig `json:"mcpServers"`
}

// Transport infers the transport for a server: the explicit type when set,
// otherwise stdio when a command is present and http when only a url is.
func (s ServerConfig) Transport() Transport {
	switch strings.ToLower(strings.TrimSpace(s.Type)) {
	case "stdio":
		return TransportStdio
	case "http", "streamable-http", "streamable_http":
		return TransportHTTP
	case "sse":
		return TransportSSE
	}
	if strings.TrimSpace(s.Command) != "" {
		return TransportStdio
	}
	return TransportHTTP
}

// Validate reports why a server declaration is unusable, or nil when it is well
// formed. name is used only for the error message.
func (s ServerConfig) Validate(name string) error {
	switch s.Transport() {
	case TransportStdio:
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("mcp: server %q: stdio transport needs a command", name)
		}
		if strings.TrimSpace(s.URL) != "" {
			return fmt.Errorf("mcp: server %q: stdio transport must not set url", name)
		}
	case TransportHTTP, TransportSSE:
		if strings.TrimSpace(s.URL) == "" {
			return fmt.Errorf("mcp: server %q: %s transport needs a url", name, s.Transport())
		}
		if !strings.HasPrefix(s.URL, "http://") && !strings.HasPrefix(s.URL, "https://") {
			return fmt.Errorf("mcp: server %q: url must be http(s)", name)
		}
	}
	return nil
}

// homeDir returns the user's home directory, matching internal/auth's fallback
// so both resolve to the same place when the home dir is unknown.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// ConfigPaths lists candidate mcp.json locations in priority order:
// PLUTO_MCP_CONFIG (explicit override), then ~/.pluto/mcp.json (alongside the
// credential store), then the XDG config path (~/.config/pluto/mcp.json or
// $XDG_CONFIG_HOME/pluto/mcp.json). Load reads the first that exists.
func ConfigPaths() []string {
	var paths []string
	if override := strings.TrimSpace(os.Getenv("PLUTO_MCP_CONFIG")); override != "" {
		paths = append(paths, override)
	}
	home := homeDir()
	paths = append(paths, filepath.Join(home, ".pluto", FileName))
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "pluto", FileName))
	} else {
		paths = append(paths, filepath.Join(home, ".config", "pluto", FileName))
	}
	return paths
}

// DefaultConfigPath is where /install-mcp writes when no config exists yet: the
// first existing candidate, or ~/.pluto/mcp.json otherwise.
func DefaultConfigPath() string {
	paths := ConfigPaths()
	for _, p := range paths {
		if fileExists(p) {
			return p
		}
	}
	// Skip any PLUTO_MCP_CONFIG override for the fallback and prefer ~/.pluto.
	home := homeDir()
	return filepath.Join(home, ".pluto", FileName)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Load reads and parses the first mcp.json found among ConfigPaths. It returns
// an empty config (and path "") when no file exists — an absent config is normal
// and never an error. A present-but-malformed file is an error so the user is
// told rather than silently running without their servers.
func Load() (cfg Config, path string, err error) {
	for _, p := range ConfigPaths() {
		if !fileExists(p) {
			continue
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			debug.Warn("mcp", "config unreadable", "path", p, "err", rerr)
			return Config{}, p, fmt.Errorf("mcp: read %q: %w", p, rerr)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			debug.Warn("mcp", "config invalid", "path", p, "err", err)
			return Config{}, p, fmt.Errorf("mcp: parse %q: %w", p, err)
		}
		debug.Info("mcp", "config loaded", "path", p, "servers", len(cfg.Servers))
		return cfg, p, nil
	}
	debug.Debug("mcp", "no config found", "candidates", strings.Join(ConfigPaths(), ", "))
	return Config{}, "", nil
}

// Names returns the configured server names sorted for stable ordering.
func (c Config) Names() []string {
	names := make([]string, 0, len(c.Servers))
	for name := range c.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
