package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/skills"
	"github.com/syrull/pluto/internal/workdir"
)

// captureToolDebug enables the debug logger scoped to the "tool" component and
// returns a reader for the captured output.
func captureToolDebug(t *testing.T) func() string {
	t.Helper()
	_ = debug.Close()
	path := filepath.Join(t.TempDir(), "pluto-debug.log")
	t.Setenv("PLUTO_DEBUG", "1")
	t.Setenv("PLUTO_DEBUG_FILE", path)
	t.Setenv("PLUTO_DEBUG_LEVEL", "debug")
	t.Setenv("PLUTO_DEBUG_COMPONENTS", "tool")
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

func seedSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	sd := filepath.Join(dir, skills.DirName)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sd, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestSkillListsAvailable(t *testing.T) {
	dir := t.TempDir()
	seedSkill(t, dir, "run-tests.md", "# Run the test suite\nbody\n")
	seedSkill(t, dir, "cut-release.md", "Cut a release\nbody\n")
	ctx := workdir.With(context.Background(), dir)

	out, err := Skill{}.Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, "- run-tests: Run the test suite") {
		t.Fatalf("list missing run-tests: %q", out)
	}
	if !strings.Contains(out, "- cut-release: Cut a release") {
		t.Fatalf("list missing cut-release: %q", out)
	}
}

func TestSkillListEmptyDir(t *testing.T) {
	ctx := workdir.With(context.Background(), t.TempDir())
	out, err := Skill{}.Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, "No skills available") {
		t.Fatalf("empty-dir list = %q", out)
	}
}

func TestSkillLoadsBody(t *testing.T) {
	dir := t.TempDir()
	seedSkill(t, dir, "run-tests.md", "# Run the test suite\n\nRun go test ./...\n")
	ctx := workdir.With(context.Background(), dir)

	out, err := Skill{}.Execute(ctx, json.RawMessage(`{"name":"run-tests"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.HasPrefix(out, "--- skill: run-tests ---\n") {
		t.Fatalf("load missing header: %q", out)
	}
	if !strings.Contains(out, "Run go test ./...") {
		t.Fatalf("load missing body: %q", out)
	}
}

func TestSkillLoadMissingListsAvailable(t *testing.T) {
	dir := t.TempDir()
	seedSkill(t, dir, "run-tests.md", "Run the test suite\n")
	ctx := workdir.With(context.Background(), dir)

	_, err := Skill{}.Execute(ctx, json.RawMessage(`{"name":"nope"}`))
	if err == nil {
		t.Fatal("Execute() error = nil, want not-found error")
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "run-tests") {
		t.Fatalf("error should name available skills: %v", err)
	}
}

func TestSkillLoadMissingNoSkills(t *testing.T) {
	ctx := workdir.With(context.Background(), t.TempDir())
	_, err := Skill{}.Execute(ctx, json.RawMessage(`{"name":"nope"}`))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Execute() error = %v, want not found", err)
	}
	if strings.Contains(err.Error(), "available skills") {
		t.Fatalf("should not list availability when none exist: %v", err)
	}
}

func TestSkillInvalidJSON(t *testing.T) {
	if _, err := (Skill{}).Execute(context.Background(), json.RawMessage(`{"name":}`)); err == nil {
		t.Fatal("Execute() error = nil, want invalid-arguments error")
	}
}

func TestSkillLoadIsLogged(t *testing.T) {
	read := captureToolDebug(t)
	dir := t.TempDir()
	seedSkill(t, dir, "run-tests.md", "Run the test suite\nbody\n")
	ctx := workdir.With(context.Background(), dir)

	if _, err := (Skill{}).Execute(ctx, json.RawMessage(`{"name":"run-tests"}`)); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	out := read()
	if !strings.Contains(out, "skill loaded") || !strings.Contains(out, "run-tests") {
		t.Fatalf("skill load not logged:\n%s", out)
	}
}
