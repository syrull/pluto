package policy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/judge"
)

func TestAllowlistSkipsJudgeAfterApproval(t *testing.T) {
	// A judge that always errors would otherwise force approval every time.
	g := newGate(t, judge.Fake{Err: errAssess}, func(c *Config) { c.FastPath = false })

	first := g.Review(context.Background(), bashCall("go test ./..."))
	if !first.NeedsApproval || first.Pattern != "go test" {
		t.Fatalf("first review = %+v, want needs-approval with pattern 'go test'", first)
	}

	g.Allow(first.Pattern)

	// A different command in the same class skips the judge entirely now.
	rr := g.Review(context.Background(), bashCall("go test ./internal/policy/"))
	if !rr.Allowed || rr.Source != "allowlist" {
		t.Fatalf("review after allow = %+v, want allowed allowlist", rr)
	}
	// A different subcommand is not covered and still needs approval.
	other := g.Review(context.Background(), bashCall("go build ./..."))
	if !other.NeedsApproval {
		t.Fatalf("unrelated command should still need approval, got %+v", other)
	}
}

func TestAllowlistNeverBypassesGuard(t *testing.T) {
	g := newGate(t, judge.Fake{Err: errAssess}, nil)
	// Even if someone allowlisted "rm", guard must still block a catastrophic rm.
	g.Allow("rm")
	rr := g.Review(context.Background(), bashCall("rm -rf /"))
	if rr.Allowed || rr.Source != "guard" {
		t.Fatalf("guard must win over the allowlist, got %+v", rr)
	}
}

func TestAllowIgnoresBlank(t *testing.T) {
	g := newGate(t, judge.Fake{Err: errAssess}, nil)
	g.Allow("   ")
	if g.allowed("") {
		t.Fatal("a blank pattern must not be allowlisted")
	}
}

func TestAllowPatternDerivation(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{"go test ./...", "go test"},
		{"git status -s", "git status"},
		{"npm   install   lodash", "npm install"},
		{"go", "go"},
		{"ls -la", "ls -la"},             // flag first arg ⇒ verbatim
		{"rm -rf build", "rm -rf build"}, // not a subcommand tool ⇒ verbatim
		{"make deploy && curl x", "make deploy && curl x"}, // metachars ⇒ verbatim
		{"cat foo | grep bar", "cat foo | grep bar"},
		{"   ", ""},
	}
	for _, tc := range cases {
		if got := allowPattern(tc.cmd); got != tc.want {
			t.Errorf("allowPattern(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

func TestAllowlistAdditionLogged(t *testing.T) {
	read := capturePolicyDebug(t)
	g := newGate(t, judge.Fake{Err: errAssess}, nil)
	g.Allow("go test")
	g.Allow("go test") // duplicate: should not log a second add

	out := read()
	if strings.Count(out, "allowlist add") != 1 {
		t.Fatalf("allowlist add should be logged once, got:\n%s", out)
	}
	if !strings.Contains(out, "pattern=") {
		t.Fatalf("allowlist add should log the pattern:\n%s", out)
	}
}

func TestAllowlistMatchLogged(t *testing.T) {
	read := capturePolicyDebug(t)
	g := newGate(t, judge.Fake{Err: errAssess}, func(c *Config) { c.FastPath = false })
	g.Allow("go test")
	g.Review(context.Background(), bashCall("go test ./..."))

	out := read()
	if !strings.Contains(out, "allowlist match") {
		t.Fatalf("allowlist match not logged:\n%s", out)
	}
}

// capturePolicyDebug enables the debug logger scoped to the "policy" component.
func capturePolicyDebug(t *testing.T) func() string {
	t.Helper()
	_ = debug.Close()
	path := filepath.Join(t.TempDir(), "pluto-debug.log")
	t.Setenv("PLUTO_DEBUG", "1")
	t.Setenv("PLUTO_DEBUG_FILE", path)
	t.Setenv("PLUTO_DEBUG_LEVEL", "debug")
	t.Setenv("PLUTO_DEBUG_COMPONENTS", "policy")
	t.Setenv("PLUTO_DEBUG_FRAMES", "")
	if _, err := debug.Init(); err != nil {
		t.Fatalf("debug.Init: %v", err)
	}
	t.Cleanup(func() { _ = debug.Close() })
	return func() string {
		_ = debug.Close()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return string(data)
	}
}
