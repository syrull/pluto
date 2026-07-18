package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// httpMethod decodes the JSON-RPC method and id from a request body.
func decodeReq(t *testing.T, r *http.Request) (string, *int64, json.RawMessage) {
	t.Helper()
	data, _ := io.ReadAll(r.Body)
	var msg rpcIncoming
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return msg.Method, msg.ID, msg.Params
}

func writeJSONResult(w http.ResponseWriter, id *int64, result any) {
	res, _ := json.Marshal(result)
	frame, _ := json.Marshal(rpcIncoming{JSONRPC: "2.0", ID: id, Result: res})
	w.Header().Set("Content-Type", "application/json")
	w.Write(frame)
}

func TestHTTPClientJSONAndSSE(t *testing.T) {
	var sawSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, id, params := decodeReq(t, r)
		switch method {
		case "initialize":
			w.Header().Set(sessionHeader, "sess-123")
			writeJSONResult(w, id, map[string]any{
				"protocolVersion": protocolVersion,
				"serverInfo":      map[string]any{"name": "remote", "version": "0.1"},
			})
		case "notifications/initialized":
			sawSession = r.Header.Get(sessionHeader)
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeJSONResult(w, id, map[string]any{
				"tools": []ToolInfo{{Name: "ping", Description: "ping"}},
			})
		case "tools/call":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(params, &p)
			// Reply over SSE to exercise the event-stream path.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			res, _ := json.Marshal(map[string]any{
				"content": []map[string]any{{"type": "text", "text": "pong:" + p.Name}},
			})
			frame, _ := json.Marshal(rpcIncoming{JSONRPC: "2.0", ID: id, Result: res})
			fmt.Fprintf(w, ": keepalive\n\ndata: %s\n\n", frame)
		default:
			writeJSONResult(w, id, map[string]any{})
		}
	}))
	defer srv.Close()

	c := &Client{name: "remote", conn: newHTTPConn("remote", ServerConfig{URL: srv.URL}, srv.Client())}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.initialize(ctx, "1.0"); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if sawSession != "sess-123" {
		t.Fatalf("session id not echoed on later request, got %q", sawSession)
	}

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	out, err := c.CallTool(ctx, "ping", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if out != "pong:ping" {
		t.Fatalf("CallTool over SSE = %q, want %q", out, "pong:ping")
	}
}

func TestHTTPClientHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &Client{name: "remote", conn: newHTTPConn("remote", ServerConfig{URL: srv.URL}, srv.Client())}
	if err := c.initialize(context.Background(), "1"); err == nil {
		t.Fatal("expected an error on HTTP 500")
	}
}
