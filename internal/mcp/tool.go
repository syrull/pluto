package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/tool"
)

// namePrefix marks a registry tool as MCP-provided and namespaces it by server
// so tools from different servers (or with built-in names) never collide.
const namePrefix = "mcp"

// maxToolNameLen matches the Anthropic tool-name limit; longer names are
// truncated so the model-facing name stays valid.
const maxToolNameLen = 64

// toolCallTimeout bounds a single tools/call when the caller's context carries
// no deadline, so a hung server can't wedge a turn indefinitely.
const toolCallTimeout = 2 * time.Minute

// mcpTool adapts one server-side MCP tool to the agent's tool.Tool contract. It
// keeps the server-side name separate from the namespaced registry name.
type mcpTool struct {
	client     *Client
	server     string
	remoteName string // the tool's name on the server, used for tools/call
	name       string // the namespaced, sanitized registry/model-facing name
	desc       string
	schema     json.RawMessage
}

var _ tool.Tool = (*mcpTool)(nil)

// newTool builds the registry adapter for a discovered server tool.
func newTool(client *Client, server string, info ToolInfo) *mcpTool {
	return &mcpTool{
		client:     client,
		server:     server,
		remoteName: info.Name,
		name:       toolName(server, info.Name),
		desc:       describe(server, info),
		schema:     info.InputSchema,
	}
}

func (t *mcpTool) Name() string            { return t.name }
func (t *mcpTool) Description() string     { return t.desc }
func (t *mcpTool) Schema() json.RawMessage { return t.schema }

func (t *mcpTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, toolCallTimeout)
		defer cancel()
	}
	timer := debug.NewTimer("mcp", "tool call")
	debug.Debug("mcp", "tool call", "server", t.server, "tool", t.remoteName)
	out, err := t.client.CallTool(ctx, t.remoteName, args)
	if err != nil {
		timer.Stop("server", t.server, "tool", t.remoteName, "outcome", "error")
		debug.Warn("mcp", "tool call failed", "server", t.server, "tool", t.remoteName, "err", err)
		return "", err
	}
	timer.Stop("server", t.server, "tool", t.remoteName, "outcome", "ok", "chars", len(out))
	return out, nil
}

// describe builds the model-facing description, tagging the source server and
// falling back to a generic line when the server supplies none.
func describe(server string, info ToolInfo) string {
	d := strings.TrimSpace(info.Description)
	if d == "" {
		d = "MCP tool provided by the " + server + " server."
	}
	return fmt.Sprintf("[MCP:%s] %s", server, d)
}

// toolName builds the namespaced registry name mcp__<server>__<tool>, sanitized
// to the model-facing name charset and truncated to the length limit.
func toolName(server, remote string) string {
	name := fmt.Sprintf("%s__%s__%s", namePrefix, sanitize(server), sanitize(remote))
	if len(name) > maxToolNameLen {
		name = name[:maxToolNameLen]
	}
	return name
}

// sanitize replaces every character outside [A-Za-z0-9_-] with '_' so the
// composed tool name matches the provider's tool-name pattern.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "server"
	}
	return b.String()
}
