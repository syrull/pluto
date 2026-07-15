// Package workdir threads a per-agent working directory through context so tools
// and the review gate resolve paths and run commands against the owning agent's
// directory (its worktree) instead of the single process-wide cwd.
package workdir

import (
	"context"
	"path/filepath"
)

type ctxKey struct{}

// With returns a context carrying dir as the working directory. An empty dir
// leaves the context unchanged.
func With(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, dir)
}

// From returns the working directory carried by ctx, or "" when none is set.
func From(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if d, ok := ctx.Value(ctxKey{}).(string); ok {
		return d
	}
	return ""
}

// Resolve joins path against the context working directory when path is relative
// and a working directory is set; absolute paths and the no-dir case are returned
// unchanged.
func Resolve(ctx context.Context, path string) string {
	dir := From(ctx)
	if dir == "" || path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}
