package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBashHappyPath(t *testing.T) {
	b := Bash{}
	args := json.RawMessage(`{"command":"echo hello"}`)
	result, err := b.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Bash.Execute() error = %v, want nil", err)
	}
	if result != "hello" {
		t.Fatalf("Bash.Execute() result = %q, want %q", result, "hello")
	}
}

func TestBashStderrCapture(t *testing.T) {
	b := Bash{}
	args := json.RawMessage(`{"command":"echo oops >&2"}`)
	result, err := b.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Bash.Execute() error = %v, want nil", err)
	}
	if result != "oops" {
		t.Fatalf("Bash.Execute() result = %q, want %q", result, "oops")
	}
}

func TestBashEmptyOutput(t *testing.T) {
	b := Bash{}
	args := json.RawMessage(`{"command":"true"}`)
	result, err := b.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Bash.Execute() error = %v, want nil", err)
	}
	if result != "(no output)" {
		t.Fatalf("Bash.Execute() result = %q, want %q", result, "(no output)")
	}
}

func TestBashNonzeroExit(t *testing.T) {
	b := Bash{}
	args := json.RawMessage(`{"command":"exit 3"}`)
	result, err := b.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Bash.Execute() error = %v, want nil", err)
	}
	if !strings.Contains(result, "error:") {
		t.Fatalf("Bash.Execute() result = %q, want to contain %q", result, "error:")
	}
}

func TestBashTimeout(t *testing.T) {
	b := Bash{}
	args := json.RawMessage(`{"command":"sleep 5","timeout":1}`)
	result, err := b.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Bash.Execute() error = %v, want nil", err)
	}
	if !strings.Contains(result, "timed out after") {
		t.Fatalf("Bash.Execute() result = %q, want to contain %q", result, "timed out after")
	}
}

func TestBashEmptyCommandError(t *testing.T) {
	b := Bash{}
	args := json.RawMessage(`{"command":""}`)
	result, err := b.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Bash.Execute() error = nil, want non-nil; result = %q", result)
	}
}

func TestBashInvalidJSONError(t *testing.T) {
	b := Bash{}
	args := json.RawMessage(`{invalid json}`)
	result, err := b.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Bash.Execute() error = nil, want non-nil; result = %q", result)
	}
}

func TestBashSchemaRequiresIntent(t *testing.T) {
	var s struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(Bash{}.Schema(), &s); err != nil {
		t.Fatalf("Schema unmarshal: %v", err)
	}
	for _, k := range []string{"command", "intent", "why"} {
		if _, ok := s.Properties[k]; !ok {
			t.Fatalf("Schema missing property %q", k)
		}
		if !strings.Contains(strings.Join(s.Required, ","), k) {
			t.Fatalf("Schema does not require %q", k)
		}
	}
}

func TestBashExecuteIgnoresMissingIntent(t *testing.T) {
	// Intent/why are for the gate; Execute must still run without them.
	result, err := Bash{}.Execute(context.Background(), json.RawMessage(`{"command":"echo ok"}`))
	if err != nil || result != "ok" {
		t.Fatalf("Execute without intent = %q, %v; want %q, nil", result, err, "ok")
	}
}
