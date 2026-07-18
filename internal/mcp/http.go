package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/syrull/pluto/internal/debug"
)

// sessionHeader carries the server-assigned session id across a Streamable HTTP
// connection; protocolHeader pins the negotiated protocol version.
const (
	sessionHeader  = "Mcp-Session-Id"
	protocolHeader = "MCP-Protocol-Version"
)

// httpConn speaks JSON-RPC over the Streamable HTTP transport: each message is
// POSTed to a single endpoint, and the server replies with either a JSON body
// or a text/event-stream it closes once the response is delivered.
type httpConn struct {
	name     string
	endpoint string
	headers  map[string]string
	client   *http.Client

	mu        sync.Mutex
	sessionID string
	protocol  string
}

func newHTTPConn(name string, cfg ServerConfig, client *http.Client) *httpConn {
	if client == nil {
		client = http.DefaultClient
	}
	return &httpConn{name: name, endpoint: cfg.URL, headers: cfg.Headers, client: client}
}

func (c *httpConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := int64(1)
	frame, err := marshalRequest(id, method, params)
	if err != nil {
		return nil, err
	}
	resp, err := c.post(ctx, frame)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp: %s: %s -> HTTP %d: %s", c.name, method, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	c.captureSession(resp)

	msg, err := c.readResponse(resp, id)
	if err != nil {
		return nil, err
	}
	if msg.Error != nil {
		return nil, msg.Error
	}
	return msg.Result, nil
}

func (c *httpConn) notify(ctx context.Context, method string, params any) error {
	frame, err := marshalNotification(method, params)
	if err != nil {
		return err
	}
	resp, err := c.post(ctx, frame)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("mcp: %s: notify %s -> HTTP %d", c.name, method, resp.StatusCode)
	}
	c.captureSession(resp)
	return nil
}

// post sends one framed message with the transport's headers and session state.
func (c *httpConn) post(ctx context.Context, frame []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(frame))
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: build request: %w", c.name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	c.mu.Lock()
	if c.sessionID != "" {
		req.Header.Set(sessionHeader, c.sessionID)
	}
	if c.protocol != "" {
		req.Header.Set(protocolHeader, c.protocol)
	}
	c.mu.Unlock()

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: request: %w", c.name, err)
	}
	return resp, nil
}

// captureSession records a session id the server assigns so later requests on
// this connection are correlated to the same session.
func (c *httpConn) captureSession(resp *http.Response) {
	if id := resp.Header.Get(sessionHeader); id != "" {
		c.mu.Lock()
		if id != c.sessionID {
			c.sessionID = id
			debug.Debug("mcp", "http session established", "server", c.name)
		}
		c.mu.Unlock()
	}
}

// setProtocol pins the negotiated protocol version for subsequent requests.
func (c *httpConn) setProtocol(version string) {
	c.mu.Lock()
	c.protocol = version
	c.mu.Unlock()
}

// readResponse decodes the response for id from either a JSON body or an SSE
// stream, depending on the server's Content-Type.
func (c *httpConn) readResponse(resp *http.Response, id int64) (rpcIncoming, error) {
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return c.readSSE(resp.Body, id)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return rpcIncoming{}, fmt.Errorf("mcp: %s: read body: %w", c.name, err)
	}
	var msg rpcIncoming
	if err := json.Unmarshal(bytes.TrimSpace(data), &msg); err != nil {
		return rpcIncoming{}, fmt.Errorf("mcp: %s: decode response: %w", c.name, err)
	}
	return msg, nil
}

// readSSE consumes an event stream, returning the first JSON-RPC response whose
// id matches; server-initiated requests/notifications on the stream are skipped.
func (c *httpConn) readSSE(body io.Reader, id int64) (rpcIncoming, error) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	var data strings.Builder
	flush := func() (rpcIncoming, bool, error) {
		if data.Len() == 0 {
			return rpcIncoming{}, false, nil
		}
		payload := data.String()
		data.Reset()
		var msg rpcIncoming
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			debug.Warn("mcp", "undecodable sse event", "server", c.name, "err", err)
			return rpcIncoming{}, false, nil
		}
		if msg.isResponse() && msg.ID != nil && *msg.ID == id {
			return msg, true, nil
		}
		debug.Trace("mcp", "sse event skipped", "server", c.name, "method", msg.Method)
		return rpcIncoming{}, false, nil
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if msg, ok, err := flush(); err != nil || ok {
				return msg, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // comment/keepalive
		}
		if field, value, ok := strings.Cut(line, ":"); ok && field == "data" {
			data.WriteString(strings.TrimPrefix(value, " "))
		}
	}
	if msg, ok, err := flush(); err != nil || ok {
		return msg, err
	}
	if err := sc.Err(); err != nil {
		return rpcIncoming{}, fmt.Errorf("mcp: %s: read stream: %w", c.name, err)
	}
	return rpcIncoming{}, fmt.Errorf("mcp: %s: event stream closed without a reply", c.name)
}

func (c *httpConn) close() error {
	c.mu.Lock()
	session := c.sessionID
	c.sessionID = ""
	c.mu.Unlock()
	if session == "" {
		return nil
	}
	// Best-effort session teardown; ignore the outcome.
	req, err := http.NewRequest(http.MethodDelete, c.endpoint, nil)
	if err != nil {
		return nil
	}
	req.Header.Set(sessionHeader, session)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if resp, err := c.client.Do(req); err == nil {
		resp.Body.Close()
	}
	return nil
}
