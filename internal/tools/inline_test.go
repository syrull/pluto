package tools

import (
	"context"
	"strings"
	"testing"
)

func TestRunInlineHappyPath(t *testing.T) {
	if got := RunInline(context.Background(), "echo hello"); got != "hello" {
		t.Fatalf("RunInline = %q, want %q", got, "hello")
	}
}

func TestRunInlineCapturesStderr(t *testing.T) {
	if got := RunInline(context.Background(), "echo oops >&2"); got != "oops" {
		t.Fatalf("RunInline = %q, want %q", got, "oops")
	}
}

func TestRunInlineNonzeroExit(t *testing.T) {
	if got := RunInline(context.Background(), "exit 3"); !strings.Contains(got, "error:") {
		t.Fatalf("RunInline = %q, want it to report the failure", got)
	}
}

func TestRunInlineEmptyCommand(t *testing.T) {
	if got := RunInline(context.Background(), "   "); !strings.Contains(got, "error:") {
		t.Fatalf("RunInline = %q, want a required-command error", got)
	}
}

func TestRunInlineNoOutputCap(t *testing.T) {
	// The inline path must never apply the bash tool's bashMaxBytes cap: the
	// user asked for the command explicitly and wants the whole output.
	got := RunInline(context.Background(), "yes x | head -c 100000")
	if len(got) < bashMaxBytes {
		t.Fatalf("RunInline output = %d bytes, want the full > %d (uncapped)", len(got), bashMaxBytes)
	}
	if strings.Contains(got, "output truncated") {
		t.Fatalf("RunInline should not truncate output, got a truncation marker")
	}
}

func TestRunInlineCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := RunInline(ctx, "sleep 5"); !strings.Contains(got, "canceled") {
		t.Fatalf("RunInline with canceled ctx = %q, want it to report canceled", got)
	}
}
