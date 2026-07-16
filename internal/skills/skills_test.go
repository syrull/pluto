package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/debug"
)

// captureDebug enables the debug logger scoped to the "tool" component (the tag
// the skills package logs under) and returns a reader for the captured output.
func captureDebug(t *testing.T) func() string {
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

func seed(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestListMissingDir(t *testing.T) {
	if got := List(filepath.Join(t.TempDir(), "nope")); got != nil {
		t.Fatalf("List(missing) = %v, want nil", got)
	}
}

func TestListEmptyDir(t *testing.T) {
	if got := List(t.TempDir()); got != nil {
		t.Fatalf("List(empty) = %v, want nil", got)
	}
}

func TestListDiscoversAndSorts(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "run-tests.md", "# Run the test suite\n\nRun `go test ./...`.\n")
	seed(t, dir, "cut-release.txt", "Cut a tagged release\nmore body\n")

	list := List(dir)
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2: %+v", len(list), list)
	}
	if list[0].Name != "cut-release" || list[1].Name != "run-tests" {
		t.Fatalf("List() not sorted by name: %+v", list)
	}
	if list[0].Summary != "Cut a tagged release" {
		t.Fatalf("txt summary = %q", list[0].Summary)
	}
	if list[1].Summary != "Run the test suite" {
		t.Fatalf("markdown heading not stripped: %q", list[1].Summary)
	}
}

func TestListSkipsNonSkillFilesDirsAndEmpty(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "keep.md", "Keep me\n")
	seed(t, dir, "ignore.go", "package x\n")
	seed(t, dir, ".hidden.md", "Hidden\n")
	seed(t, dir, "blank.md", "   \n\t\n")
	if err := os.MkdirAll(filepath.Join(dir, "sub.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	list := List(dir)
	if len(list) != 1 || list[0].Name != "keep" {
		t.Fatalf("List() = %+v, want only keep", list)
	}
}

func TestListSummaryTruncated(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "long.md", strings.Repeat("x", maxSummaryLen+50)+"\n")
	list := List(dir)
	if len(list) != 1 {
		t.Fatalf("List() len = %d", len(list))
	}
	if r := []rune(list[0].Summary); len(r) != maxSummaryLen+1 || !strings.HasSuffix(list[0].Summary, "…") {
		t.Fatalf("summary not truncated with ellipsis: %q (%d runes)", list[0].Summary, len(r))
	}
}

func TestLoadReturnsFullBody(t *testing.T) {
	dir := t.TempDir()
	body := "# Run the test suite\n\nRun `go test ./...` and read failures top-down.\n"
	seed(t, dir, "run-tests.md", body)

	got, err := Load(dir, "run-tests")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != strings.TrimSpace(body) {
		t.Fatalf("Load() = %q, want trimmed body", got)
	}
}

func TestLoadAcceptsNameWithExtension(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "cut-release.txt", "Cut a release\n")
	got, err := Load(dir, "cut-release.txt")
	if err != nil {
		t.Fatalf("Load(with ext) error = %v", err)
	}
	if got != "Cut a release" {
		t.Fatalf("Load(with ext) = %q", got)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(t.TempDir(), "nope")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Load(missing) error = %v, want not found", err)
	}
}

func TestLoadEmptyName(t *testing.T) {
	if _, err := Load(t.TempDir(), "   "); err == nil {
		t.Fatal("Load(empty) error = nil, want error")
	}
}

func TestLoadEmptyBody(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "blank.md", "  \n\t\n")
	_, err := Load(dir, "blank")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("Load(empty body) error = %v, want empty", err)
	}
}

func TestLoadRejectsUnsafeName(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "secret.md", "top secret\n")
	for _, name := range []string{"../secret", "sub/secret", `..\secret`} {
		if _, err := Load(dir, name); err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Fatalf("Load(%q) error = %v, want invalid", name, err)
		}
	}
}

func TestListDedupesByExtensionPreference(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "foo.md", "From markdown\nmd body\n")
	seed(t, dir, "foo.txt", "From text\ntxt body\n")

	list := List(dir)
	if len(list) != 1 {
		t.Fatalf("List() len = %d, want 1 (deduped): %+v", len(list), list)
	}
	if list[0].Name != "foo" || list[0].Summary != "From markdown" {
		t.Fatalf("List() kept wrong file, want the .md: %+v", list[0])
	}
	body, err := Load(dir, "foo")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !strings.Contains(body, "md body") {
		t.Fatalf("Load() = %q, want the preferred .md body", body)
	}
}

func TestListMissingDirIsSilent(t *testing.T) {
	read := captureDebug(t)
	if got := List(filepath.Join(t.TempDir(), "nope")); got != nil {
		t.Fatalf("List(missing) = %v, want nil", got)
	}
	if out := read(); strings.Contains(out, "unreadable") {
		t.Fatalf("missing dir must not warn:\n%s", out)
	}
}

func TestListWarnsOnUnreadableDir(t *testing.T) {
	read := captureDebug(t)
	// A regular file where a directory is expected makes ReadDir fail with a
	// non-not-exist error, which must be surfaced instead of silently skipped.
	notDir := filepath.Join(t.TempDir(), "skills")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := List(notDir); got != nil {
		t.Fatalf("List(non-dir) = %v, want nil", got)
	}
	if out := read(); !strings.Contains(out, "skills dir unreadable") {
		t.Fatalf("unreadable dir not logged:\n%s", out)
	}
}

func TestCanonical(t *testing.T) {
	cases := map[string]string{
		"run-tests":       "run-tests",
		"run-tests.md":    "run-tests",
		"cut-release.txt": "cut-release",
		"notes.go":        "notes.go",
		"  x.md  ":        "x",
	}
	for in, want := range cases {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRender(t *testing.T) {
	if got := Render(nil); got != "" {
		t.Fatalf("Render(nil) = %q, want empty", got)
	}
	got := Render([]Skill{{Name: "a", Summary: "first"}, {Name: "b", Summary: "second"}})
	want := "- a: first\n- b: second"
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}
