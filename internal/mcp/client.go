package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/syrull/pluto/internal/debug"
)

// protocolVersion is the MCP revision pluto advertises on initialize. Servers
// negotiate down to a version they support.
const protocolVersion = "2025-06-18"

// conn is a JSON-RPC transport: a request/response channel plus fire-and-forget
// notifications. Both the stdio and HTTP transports satisfy it.
type conn interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	notify(ctx context.Context, method string, params any) error
	close() error
}

// Client is a live connection to one MCP server: it owns the transport and
// tracks whether the initialize handshake has completed.
type Client struct {
	name      string
	conn      conn
	serverAPI string // server name+version reported by initialize, for logs
}

// ToolInfo is a tool a server advertises: its name, description, and JSON-Schema
// for arguments (passed through to the model unchanged).
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Dial connects to a server per its config and completes the MCP handshake,
// returning a ready client. clientVersion is reported to the server as
// clientInfo.version.
func Dial(ctx context.Context, name string, cfg ServerConfig, clientVersion string) (*Client, error) {
	if err := cfg.Validate(name); err != nil {
		return nil, err
	}
	var (
		cn  conn
		err error
	)
	switch cfg.Transport() {
	case TransportStdio:
		cn, err = startStdioProcess(ctx, name, cfg)
	default:
		cn = newHTTPConn(name, cfg, &http.Client{Timeout: 0})
	}
	if err != nil {
		return nil, err
	}
	c := &Client{name: name, conn: cn}
	if err := c.initialize(ctx, clientVersion); err != nil {
		_ = cn.close()
		return nil, err
	}
	return c, nil
}

// Name returns the configured server name.
func (c *Client) Name() string { return c.name }

// initialize performs the MCP handshake: an initialize request declaring no
// client capabilities, followed by the initialized notification.
func (c *Client) initialize(ctx context.Context, clientVersion string) error {
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = "dev"
	}
	timer := debug.NewTimer("mcp", "initialize")
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "pluto", "version": clientVersion},
	}
	raw, err := c.conn.call(ctx, "initialize", params)
	if err != nil {
		timer.Stop("server", c.name, "outcome", "error")
		return fmt.Errorf("mcp: %s: initialize: %w", c.name, err)
	}
	var res struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	_ = json.Unmarshal(raw, &res)
	if res.ProtocolVersion != "" {
		if h, ok := c.conn.(*httpConn); ok {
			h.setProtocol(res.ProtocolVersion)
		}
	}
	c.serverAPI = strings.TrimSpace(res.ServerInfo.Name + " " + res.ServerInfo.Version)
	if err := c.conn.notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		timer.Stop("server", c.name, "outcome", "error")
		return fmt.Errorf("mcp: %s: initialized notification: %w", c.name, err)
	}
	timer.Stop("server", c.name, "outcome", "ok", "protocol", res.ProtocolVersion, "server_info", c.serverAPI)
	debug.Info("mcp", "handshake complete", "server", c.name, "protocol", res.ProtocolVersion, "server_info", c.serverAPI)
	return nil
}

// ListTools returns every tool the server exposes, following pagination until
// the server stops handing back a cursor.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	timer := debug.NewTimer("mcp", "tools/list")
	var (
		all    []ToolInfo
		cursor string
	)
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.conn.call(ctx, "tools/list", params)
		if err != nil {
			timer.Stop("server", c.name, "outcome", "error")
			return nil, fmt.Errorf("mcp: %s: tools/list: %w", c.name, err)
		}
		var res struct {
			Tools      []ToolInfo `json:"tools"`
			NextCursor string     `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			timer.Stop("server", c.name, "outcome", "error")
			return nil, fmt.Errorf("mcp: %s: decode tools/list: %w", c.name, err)
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" || len(res.Tools) == 0 {
			break
		}
		cursor = res.NextCursor
	}
	timer.Stop("server", c.name, "outcome", "ok", "count", len(all))
	return all, nil
}

// callResult is the tools/call response: content blocks plus an error flag.
type callResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// CallTool invokes a tool by its server-side name with raw JSON arguments and
// returns the flattened textual result. A protocol-level error is returned as an
// error; a tool-reported failure (isError) is returned as text prefixed with
// "error:" so the model sees it and can react.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	params := map[string]any{"name": name, "arguments": args}
	raw, err := c.conn.call(ctx, "tools/call", params)
	if err != nil {
		return "", fmt.Errorf("mcp: %s: tools/call %q: %w", c.name, name, err)
	}
	var res callResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("mcp: %s: decode tools/call %q: %w", c.name, name, err)
	}
	text := flattenContent(res.Content)
	if res.IsError {
		if text == "" {
			text = "tool reported an error"
		}
		return "error: " + text, nil
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}

// Close tears down the transport.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.close()
}

// flattenContent joins text content blocks; non-text blocks are noted by type
// since the agent's tool results are plain text.
func flattenContent(content []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	var parts []string
	for _, b := range content {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		default:
			parts = append(parts, fmt.Sprintf("[%s content omitted]", b.Type))
		}
	}
	return strings.Join(parts, "\n")
}

// dialTimeout bounds a single server's connect+handshake so one unreachable
// server can't stall startup.
const dialTimeout = 30 * time.Second
