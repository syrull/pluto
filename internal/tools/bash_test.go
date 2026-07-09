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
