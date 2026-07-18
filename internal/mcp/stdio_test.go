package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeStdioServer speaks minimal MCP over the given pipes: it answers
// initialize, tools/list, and tools/call, and echoes the called tool name back
// in the result so a round-trip is observable.
func fakeStdioServer(r io.Reader, w io.WriteCloser, tools []ToolInfo) {
	defer w.Close()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	write := func(id *int64, result any) {
		res, _ := json.Marshal(result)
		frame, _ := json.Marshal(rpcIncoming{JSONRPC: "2.0", ID: id, Result: res})
		w.Write(append(frame, '\n'))
	}
	for sc.Scan() {
		var msg rpcIncoming
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			write(msg.ID, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.2.3"},
			})
		case "notifications/initialized":
			// notification: no reply
		case "tools/list":
			write(msg.ID, map[string]any{"tools": tools})
		case "tools/call":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			write(msg.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "echo:" + p.Name}},
			})
		default:
			if msg.ID != nil {
				write(msg.ID, map[string]any{})
			}
		}
	}
}

// newTestClient wires a Client to a fake stdio server over in-memory pipes.
func newTestClient(t *testing.T, tools []ToolInfo) *Client {
	t.Helper()
	cr, cw := io.Pipe() // client -> server
	sr, sw := io.Pipe() // server -> client
	go fakeStdioServer(cr, sw, tools)
	stop := func() error { cw.Close(); return nil }
	c := &Client{name: "test", conn: newStdioConn("test", sr, cw, stop)}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestStdioClientHandshakeAndTools(t *testing.T) {
	tools := []ToolInfo{
		{Name: "search", Description: "search the web", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "fetch", Description: "fetch a url"},
	}
	c := newTestClient(t, tools)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.initialize(ctx, "9.9.9"); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if c.serverAPI != "fake 1.2.3" {
		t.Fatalf("serverAPI = %q, want %q", c.serverAPI, "fake 1.2.3")
	}

	got, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(got) != 2 || got[0].Name != "search" || got[1].Name != "fetch" {
		t.Fatalf("unexpected tools: %+v", got)
	}

	out, err := c.CallTool(ctx, "search", json.RawMessage(`{"q":"pluto"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if out != "echo:search" {
		t.Fatalf("CallTool result = %q, want %q", out, "echo:search")
	}
}

func TestMergeEnvCuratesSecrets(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-secret")
	// A non-allowlisted variable in the parent env must not reach the child.
	t.Setenv("MCP_TEST_SECRET", "leak-me")

	joined := strings.Join(mergeEnv(map[string]string{"SOME_API_KEY": "declared"}), "\n")
	if !strings.Contains(joined, "PATH=/usr/bin") {
		t.Error("PATH should be inherited from the curated allowlist")
	}
	if !strings.Contains(joined, "SOME_API_KEY=declared") {
		t.Error("the server's declared env should be passed through")
	}
	for _, leak := range []string{"sk-secret", "oauth-secret", "ANTHROPIC_API_KEY", "MCP_TEST_SECRET", "leak-me"} {
		if strings.Contains(joined, leak) {
			t.Errorf("parent secret %q leaked into the child environment", leak)
		}
	}
}

func TestStderrLoggerBuffersLines(t *testing.T) {
	l := &stderrLogger{server: "s"}
	if n, err := l.Write([]byte("partial")); err != nil || n != 7 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if len(l.buf) != len("partial") {
		t.Fatalf("a line without a newline should stay buffered, have %q", l.buf)
	}
	if _, err := l.Write([]byte(" line\r\nsecond\n")); err != nil {
		t.Fatal(err)
	}
	if len(l.buf) != 0 {
		t.Fatalf("buffer should drain after a trailing newline, have %q", l.buf)
	}
}

func TestStdioClosedConnFailsCalls(t *testing.T) {
	c := newTestClient(t, nil)
	ctx := context.Background()
	if err := c.initialize(ctx, "1"); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Give the read loop a moment to observe the closed pipe.
	time.Sleep(20 * time.Millisecond)
	if _, err := c.ListTools(ctx); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected a closed-connection error, got %v", err)
	}
}
