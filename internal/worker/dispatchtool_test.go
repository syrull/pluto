package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

func newFinishingPool() *Pool {
	prov := &scriptProvider{gen: func(int, []llm.Message, []llm.ToolSpec, context.Context) (llm.Response, error) {
		return finalText("done"), nil
	}}
	return NewPool(context.Background(), Config{Provider: prov, Registry: tool.NewRegistry()})
}

func decodeJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, s)
	}
	return m
}

func TestDispatchToolSchemaIsValidJSON(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(NewDispatchTool(nil).Schema(), &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if m["type"] != "object" {
		t.Fatalf("schema type = %v, want object", m["type"])
	}
}

func TestDispatchToolNilPoolReports(t *testing.T) {
	_, err := NewDispatchTool(nil).Execute(context.Background(), json.RawMessage(`{"action":"poll"}`))
	if err == nil {
		t.Fatal("expected an error from a nil-pool workers tool")
	}
}

func TestDispatchToolDispatchReturnsIDs(t *testing.T) {
	d := NewDispatchTool(newFinishingPool())
	args := `{"action":"dispatch","workers":[{"task":"recon"},{"task":"exploit"}]}`
	out, err := d.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	m := decodeJSON(t, out)
	ids, ok := m["ids"].([]any)
	if !ok || len(ids) != 2 {
		t.Fatalf("dispatch ids = %v, want 2", m["ids"])
	}
}

func TestDispatchToolRejectsEmptyDispatch(t *testing.T) {
	d := NewDispatchTool(newFinishingPool())
	if _, err := d.Execute(context.Background(), json.RawMessage(`{"action":"dispatch","workers":[]}`)); err == nil {
		t.Fatal("expected an error dispatching zero workers")
	}
}

func TestDispatchToolPollReturnsResults(t *testing.T) {
	pool := newFinishingPool()
	d := NewDispatchTool(pool)
	if _, err := d.Execute(context.Background(), json.RawMessage(`{"action":"dispatch","workers":[{"task":"recon"}]}`)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out, err := d.Execute(context.Background(), json.RawMessage(`{"action":"poll"}`))
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		workers := decodeJSON(t, out)["workers"].([]any)
		if len(workers) == 1 && workers[0].(map[string]any)["state"] == "done" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("worker never reported done via poll")
}

func TestDispatchToolWaitReturnsResults(t *testing.T) {
	d := NewDispatchTool(newFinishingPool())
	if _, err := d.Execute(context.Background(), json.RawMessage(`{"action":"dispatch","workers":[{"task":"recon"}]}`)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	out, err := d.Execute(context.Background(), json.RawMessage(`{"action":"wait","timeout_s":2}`))
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	m := decodeJSON(t, out)
	if m["wait"] != "completed" {
		t.Fatalf("wait reason = %v, want completed", m["wait"])
	}
	workers, ok := m["workers"].([]any)
	if !ok || len(workers) != 1 {
		t.Fatalf("wait workers = %v, want 1", m["workers"])
	}
	if state := workers[0].(map[string]any)["state"]; state != "done" {
		t.Fatalf("worker state = %v, want done after wait", state)
	}
}

func TestDispatchToolCancelRequiresIDs(t *testing.T) {
	d := NewDispatchTool(newFinishingPool())
	if _, err := d.Execute(context.Background(), json.RawMessage(`{"action":"cancel"}`)); err == nil {
		t.Fatal("expected an error cancelling with no ids")
	}
}

func TestDispatchToolUnknownAction(t *testing.T) {
	d := NewDispatchTool(newFinishingPool())
	if _, err := d.Execute(context.Background(), json.RawMessage(`{"action":"frobnicate"}`)); err == nil {
		t.Fatal("expected an error for an unknown action")
	}
}
