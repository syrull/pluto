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

// seed writes a skill folder dir/name/SKILL.md with the given content.
func seed(t *testing.T, dir, name, content string) {
	t.Helper()
	sd := filepath.Join(dir, name)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", sd, err)
	}
	if err := os.WriteFile(filepath.Join(sd, FileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// doc builds SKILL.md content with frontmatter and a body.
func doc(name, desc, body string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n" + body + "\n"
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
	seed(t, dir, "run-tests", doc("run-tests", "Run the test suite", "# Run\ngo test ./..."))
	seed(t, dir, "cut-release", doc("cut-release", "Cut a tagged release", "# Release\nmore body"))

	list := List(dir)
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2: %+v", len(list), list)
	}
	if list[0].Name != "cut-release" || list[1].Name != "run-tests" {
		t.Fatalf("List() not sorted by name: %+v", list)
	}
	if list[0].Summary != "Cut a tagged release" {
		t.Fatalf("summary = %q", list[0].Summary)
	}
	if list[1].Summary != "Run the test suite" {
		t.Fatalf("summary = %q", list[1].Summary)
	}
}

func TestListSkipsNonDirsHiddenAndDescriptionless(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "keep", doc("keep", "Keep me", "body"))
	seed(t, dir, ".hidden", doc("hidden", "Hidden", "body"))
	// A folder whose SKILL.md has no description can't be triggered; skip it.
	seed(t, dir, "no-desc", "---\nname: no-desc\n---\nbody\n")
	// A folder without any SKILL.md is not a skill.
	if err := os.MkdirAll(filepath.Join(dir, "empty-folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A stray regular file is not a skill folder.
	if err := os.WriteFile(filepath.Join(dir, "loose.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	list := List(dir)
	if len(list) != 1 || list[0].Name != "keep" {
		t.Fatalf("List() = %+v, want only keep", list)
	}
}

func TestListSummaryTruncated(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "long", doc("long", strings.Repeat("x", maxSummaryLen+50), "body"))
	list := List(dir)
	if len(list) != 1 {
		t.Fatalf("List() len = %d", len(list))
	}
	if r := []rune(list[0].Summary); len(r) != maxSummaryLen+1 || !strings.HasSuffix(list[0].Summary, "…") {
		t.Fatalf("summary not truncated with ellipsis: %q (%d runes)", list[0].Summary, len(r))
	}
}

func TestListCollapsesMultilineDescription(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "wrapped", "---\nname: wrapped\ndescription:   Use   this   when\t needed\n---\nbody\n")
	list := List(dir)
	if len(list) != 1 || list[0].Summary != "Use this when needed" {
		t.Fatalf("summary not collapsed to one line: %+v", list)
	}
}

func TestListNameMismatchLogged(t *testing.T) {
	read := captureDebug(t)
	dir := t.TempDir()
	seed(t, dir, "folder-name", doc("other-name", "Desc", "body"))
	if list := List(dir); len(list) != 1 || list[0].Name != "folder-name" {
		t.Fatalf("List() should key on folder name: %+v", list)
	}
	if out := read(); !strings.Contains(out, "skill name mismatch") {
		t.Fatalf("name mismatch not logged:\n%s", out)
	}
}

func TestLoadReturnsBodyWithoutFrontmatter(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "run-tests", doc("run-tests", "Run the suite", "# Run the test suite\n\nRun `go test ./...`."))

	got, err := Load(dir, "run-tests")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.Contains(got, "description:") || strings.HasPrefix(got, "---") {
		t.Fatalf("Load() leaked frontmatter: %q", got)
	}
	if !strings.HasPrefix(got, "# Run the test suite") || !strings.Contains(got, "go test ./...") {
		t.Fatalf("Load() body = %q", got)
	}
}

func TestLoadContentWithoutFrontmatter(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "plain", "# Just instructions\n\nno frontmatter here")
	got, err := Load(dir, "plain")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != "# Just instructions\n\nno frontmatter here" {
		t.Fatalf("Load() = %q", got)
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
	seed(t, dir, "blank", "---\nname: blank\ndescription: Blank\n---\n  \n\t\n")
	_, err := Load(dir, "blank")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("Load(empty body) error = %v, want empty", err)
	}
}

func TestLoadRejectsUnsafeName(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "secret", doc("secret", "Secret", "top secret"))
	for _, name := range []string{"../secret", "sub/secret", `..\secret`} {
		if _, err := Load(dir, name); err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Fatalf("Load(%q) error = %v, want invalid", name, err)
		}
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

func TestParse(t *testing.T) {
	meta, body := parse("---\nname: foo\ndescription: \"quoted desc\"\n---\nthe body\n")
	if meta["name"] != "foo" {
		t.Errorf("name = %q", meta["name"])
	}
	if meta["description"] != "quoted desc" {
		t.Errorf("description = %q", meta["description"])
	}
	if body != "the body\n" {
		t.Errorf("body = %q", body)
	}

	if m, b := parse("no frontmatter\nbody"); m != nil || b != "no frontmatter\nbody" {
		t.Errorf("parse(no frontmatter) = %v, %q", m, b)
	}

	if m, b := parse("---\nname: foo\nunterminated body"); m != nil || b != "---\nname: foo\nunterminated body" {
		t.Errorf("parse(unterminated) = %v, %q", m, b)
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
