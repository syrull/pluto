package mcp

import (
	"context"
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
}

// New builds a Manager. clientVersion is reported to servers as clientInfo and
// defaults to "dev" when empty.
func New(clientVersion string) *Manager {
	return &Manager{clientVersion: clientVersion}
}

// Summary is the outcome of Load, for a one-line startup log and status display.
type Summary struct {
	Servers    int      // servers that connected successfully
	Tools      int      // tools registered across all servers
	Failed     []string // names of servers that failed to connect
	ConfigPath string   // the mcp.json that was loaded ("" ⇒ none found)
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

	for _, name := range cfg.Names() {
		sc := cfg.Servers[name]
		if sc.Disabled {
			debug.Info("mcp", "server disabled; skipped", "server", name)
			continue
		}
		n, ok := m.loadServer(ctx, reg, name, sc)
		if !ok {
			summary.Failed = append(summary.Failed, name)
			continue
		}
		summary.Servers++
		summary.Tools += n
	}
	timer.Stop("outcome", "ok", "servers", summary.Servers, "tools", summary.Tools, "failed", len(summary.Failed))
	debug.Info("mcp", "load complete", "servers", summary.Servers, "tools", summary.Tools, "failed", len(summary.Failed))
	return summary
}

// loadServer dials one server, lists its tools, and registers them, returning
// the number registered and whether the server connected. A dial/list failure
// is logged and reported as not-ok; the connection is closed on any error so a
// half-open subprocess doesn't leak.
func (m *Manager) loadServer(ctx context.Context, reg *tool.Registry, name string, sc ServerConfig) (int, bool) {
	if err := sc.Validate(name); err != nil {
		debug.Warn("mcp", "invalid server config; skipped", "server", name, "err", err)
		return 0, false
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	debug.Info("mcp", "connecting", "server", name, "transport", sc.Transport())
	client, err := Dial(dialCtx, name, sc, m.clientVersion)
	if err != nil {
		debug.Warn("mcp", "connect failed; skipped", "server", name, "err", err)
		return 0, false
	}

	listCtx, listCancel := context.WithTimeout(ctx, dialTimeout)
	defer listCancel()
	tools, err := client.ListTools(listCtx)
	if err != nil {
		debug.Warn("mcp", "tools/list failed; closing", "server", name, "err", err)
		_ = client.Close()
		return 0, false
	}

	m.clients = append(m.clients, client)
	registered := 0
	for _, info := range tools {
		mt := newTool(client, name, info)
		if err := reg.Register(mt); err != nil {
			debug.Warn("mcp", "tool registration skipped", "server", name, "tool", info.Name, "name", mt.Name(), "err", err)
			continue
		}
		registered++
		debug.Debug("mcp", "tool registered", "server", name, "tool", info.Name, "name", mt.Name())
	}
	debug.Info("mcp", "server ready", "server", name, "tools", registered)
	return registered, true
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
