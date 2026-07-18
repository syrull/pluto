package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalRequest(t *testing.T) {
	frame, err := marshalRequest(7, "tools/list", map[string]any{"cursor": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	var out rpcOutgoing
	if err := json.Unmarshal(frame, &out); err != nil {
		t.Fatal(err)
	}
	if out.JSONRPC != "2.0" || out.Method != "tools/list" || out.ID == nil || *out.ID != 7 {
		t.Fatalf("unexpected request frame: %s", frame)
	}
	if !strings.Contains(string(out.Params), "abc") {
		t.Fatalf("params not carried: %s", out.Params)
	}
}

func TestMarshalNotificationHasNoID(t *testing.T) {
	frame, err := marshalNotification("notifications/initialized", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(frame), `"id"`) {
		t.Fatalf("notification must not carry an id: %s", frame)
	}
}

func TestIsResponse(t *testing.T) {
	id := int64(1)
	if !(rpcIncoming{ID: &id}).isResponse() {
		t.Error("message with id and no method should be a response")
	}
	if (rpcIncoming{ID: &id, Method: "roots/list"}).isResponse() {
		t.Error("server-initiated request should not be a response")
	}
	if (rpcIncoming{Method: "notifications/message"}).isResponse() {
		t.Error("notification should not be a response")
	}
}

func TestRPCErrorString(t *testing.T) {
	e := &rpcError{Code: -32601, Message: "Method not found"}
	if !strings.Contains(e.Error(), "-32601") || !strings.Contains(e.Error(), "Method not found") {
		t.Fatalf("Error() = %q", e.Error())
	}
	e.Data = json.RawMessage(`{"detail":"x"}`)
	if !strings.Contains(e.Error(), "detail") {
		t.Fatalf("Error() should include data: %q", e.Error())
	}
}
