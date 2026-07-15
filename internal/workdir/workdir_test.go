package workdir

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFromDefaultsEmpty(t *testing.T) {
	if got := From(context.Background()); got != "" {
		t.Fatalf("From(empty ctx) = %q, want empty", got)
	}
}

func TestWithAndFrom(t *testing.T) {
	ctx := With(context.Background(), "/tmp/agent")
	if got := From(ctx); got != "/tmp/agent" {
		t.Fatalf("From = %q, want /tmp/agent", got)
	}
	// An empty dir must not shadow an existing value.
	if got := From(With(ctx, "")); got != "/tmp/agent" {
		t.Fatalf("With empty should be a no-op, From = %q", got)
	}
}

func TestResolve(t *testing.T) {
	ctx := With(context.Background(), "/base")
	if got := Resolve(ctx, "sub/file.go"); got != filepath.Join("/base", "sub/file.go") {
		t.Fatalf("relative Resolve = %q", got)
	}
	if got := Resolve(ctx, "/abs/file.go"); got != "/abs/file.go" {
		t.Fatalf("absolute path should pass through, got %q", got)
	}
	if got := Resolve(context.Background(), "rel.go"); got != "rel.go" {
		t.Fatalf("no working dir should pass path through, got %q", got)
	}
}
