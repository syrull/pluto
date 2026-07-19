package mcp

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/tool"
)

// Manager owns the live connections to every configured MCP server and keeps
// them open for the process lifetime. Load connects and registers tools; Close
// tears every connection down.
type Manager struct {
	clientVersion string
	clients       []*Client
	progress      io.Writer // user-facing startup progress; nil ⇒ quiet (tests)
}

// New builds a Manager. clientVersion is reported to servers as clientInfo and
// defaults to "dev" when empty.
func New(clientVersion string) *Manager {
	return &Manager{clientVersion: clientVersion}
}

// WithProgress sets a writer for the one-line "loading N MCP server(s)" notice
// Load prints before the (possibly slow) dial phase, so a slow server doesn't
// look like a hang at startup. nil (the default) stays quiet, which tests rely
// on. It returns the manager for chaining.
func (m *Manager) WithProgress(w io.Writer) *Manager {
	m.progress = w
	return m
}

// ServerStatus is one configured server's load outcome, surfaced by the TUI's
// /mcp command. Exactly one of Tools (connected), Err (failed), or Disabled is
// meaningful.
type ServerStatus struct {
	Name      string   // the mcp.json key
	Transport string   // stdio | http | sse
	Tools     []string // server-side tool names registered (connected servers)
	Disabled  bool     // declared disabled in mcp.json; never dialed
	Err       string   // failure reason when the server did not connect
}

// Summary is the outcome of Load, for a one-line startup log and status display.
type Summary struct {
	Servers    int            // servers that connected successfully
	Tools      int            // tools registered across all servers
	Failed     []string       // names of servers that failed to connect
	ConfigPath string         // the mcp.json that was loaded ("" ⇒ none found)
	Statuses   []ServerStatus // per-server outcome in config order, for /mcp
}

// Load reads mcp.json, connects to each enabled server, and registers its tools
// into reg. It is best-effort and never fatal: a missing config is a no-op, and
// a server that fails to connect (or exposes no tools) is logged and skipped so
// the rest of pluto still starts. Every server gets its own bounded dial so one
// unreachable endpoint can't stall startup.
func (m *Manager) Load(ctx context.Context, reg *tool.Registry) Summary {
	timer := debug.NewTimer("mcp", "load")
	cfg, path, err := Load()
	summary := Summary{ConfigPath: path}
	if err != nil {
		timer.Stop("outcome", "config-error")
		return summary
	}
	if len(cfg.Servers) == 0 {
		debug.Debug("mcp", "no servers configured")
		timer.Stop("outcome", "empty", "config", path)
		return summary
	}

	m.announce(cfg)
	for _, name := range cfg.Names() {
		sc := cfg.Servers[name]
		st := ServerStatus{Name: name, Transport: string(sc.Transport())}
		if sc.Disabled {
			st.Disabled = true
			summary.Statuses = append(summary.Statuses, st)
			debug.Info("mcp", "server disabled; skipped", "server", name)
			continue
		}
		names, err := m.loadServer(ctx, reg, name, sc)
		if err != nil {
			st.Err = err.Error()
			summary.Failed = append(summary.Failed, name)
			summary.Statuses = append(summary.Statuses, st)
			continue
		}
		st.Tools = names
		summary.Statuses = append(summary.Statuses, st)
		summary.Servers++
		summary.Tools += len(names)
	}
	timer.Stop("outcome", "ok", "servers", summary.Servers, "tools", summary.Tools, "failed", len(summary.Failed))
	debug.Info("mcp", "load complete", "servers", summary.Servers, "tools", summary.Tools, "failed", len(summary.Failed))
	return summary
}

// announce prints the one-line "loading N MCP server(s)" notice (when a progress
// writer is set) before the dial phase, counting only enabled servers.
func (m *Manager) announce(cfg Config) {
	if m.progress == nil {
		return
	}
	enabled := 0
	for _, sc := range cfg.Servers {
		if !sc.Disabled {
			enabled++
		}
	}
	if enabled == 0 {
		return
	}
	debug.Debug("mcp", "startup notice", "servers", enabled)
	fmt.Fprintf(m.progress, "pluto: loading %d MCP server(s)…\n", enabled)
}

// loadServer dials one server, lists its tools, and registers them, returning
// the registered server-side tool names and any error. A dial/list failure is
// logged and returned; the connection is closed on any error so a half-open
// subprocess doesn't leak.
func (m *Manager) loadServer(ctx context.Context, reg *tool.Registry, name string, sc ServerConfig) ([]string, error) {
	if err := sc.Validate(name); err != nil {
		debug.Warn("mcp", "invalid server config; skipped", "server", name, "err", err)
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	debug.Info("mcp", "connecting", "server", name, "transport", sc.Transport())
	client, err := Dial(dialCtx, name, sc, m.clientVersion)
	if err != nil {
		debug.Warn("mcp", "connect failed; skipped", "server", name, "err", err)
		return nil, err
	}

	listCtx, listCancel := context.WithTimeout(ctx, dialTimeout)
	defer listCancel()
	tools, err := client.ListTools(listCtx)
	if err != nil {
		debug.Warn("mcp", "tools/list failed; closing", "server", name, "err", err)
		_ = client.Close()
		return nil, err
	}

	m.clients = append(m.clients, client)
	var names []string
	for _, info := range tools {
		mt := newTool(client, name, info)
		if err := reg.Register(mt); err != nil {
			debug.Warn("mcp", "tool registration skipped", "server", name, "tool", info.Name, "name", mt.Name(), "err", err)
			continue
		}
		names = append(names, info.Name)
		debug.Debug("mcp", "tool registered", "server", name, "tool", info.Name, "name", mt.Name())
	}
	debug.Info("mcp", "server ready", "server", name, "tools", len(names))
	return names, nil
}

// Close tears down every open connection. Safe to call once at shutdown.
func (m *Manager) Close() {
	if len(m.clients) == 0 {
		return
	}
	timer := debug.NewTimer("mcp", "close")
	for _, c := range m.clients {
		if err := c.Close(); err != nil {
			debug.Debug("mcp", "close error", "server", c.Name(), "err", err)
		}
	}
	timer.Stop("clients", len(m.clients))
	m.clients = nil
}

// startupDeadline bounds the whole Load phase so a batch of slow servers can't
// hang pluto's launch; individual dials are bounded by dialTimeout too.
const startupDeadline = 90 * time.Second

// LoadWithDeadline runs Load under startupDeadline, the entry point main wires
// in so a pathological config can't block the TUI from opening.
func (m *Manager) LoadWithDeadline(reg *tool.Registry) Summary {
	ctx, cancel := context.WithTimeout(context.Background(), startupDeadline)
	defer cancel()
	return m.Load(ctx, reg)
}
