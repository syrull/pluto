package reposcan

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestScanEmptyDir(t *testing.T) {
	out := Scan(t.TempDir())
	if !strings.Contains(out, "Repository snapshot") {
		t.Fatalf("missing header:\n%s", out)
	}
	if !strings.Contains(out, "Working directory:") {
		t.Fatalf("missing working directory:\n%s", out)
	}
	if strings.Contains(out, "Project type:") || strings.Contains(out, "Git:") {
		t.Fatalf("empty dir should have no project type or git line:\n%s", out)
	}
}

func TestScanUnreadableRoot(t *testing.T) {
	if got := Scan(filepath.Join(t.TempDir(), "does-not-exist")); got != "" {
		t.Fatalf("nonexistent root should yield empty snapshot, got:\n%s", got)
	}
}

func TestScanGoModule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module github.com/syrull/pluto\n\ngo 1.26.3\n")
	out := Scan(dir)
	if !strings.Contains(out, "Project type: Go module (github.com/syrull/pluto)") {
		t.Fatalf("go.mod not detected with module path:\n%s", out)
	}
}

func TestScanMultipleAndDedupedTypes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", "{}")
	writeFile(t, dir, "pyproject.toml", "")
	writeFile(t, dir, "requirements.txt", "")
	out := Scan(dir)
	if !strings.Contains(out, "Node.js / JavaScript") {
		t.Fatalf("node not detected:\n%s", out)
	}
	if strings.Count(out, "Python") != 1 {
		t.Fatalf("duplicate Python labels not deduped:\n%s", out)
	}
}

func TestListingDirsFirstAndGitSkipped(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{".git", "internal", "cmd"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, dir, "main.go", "package main")
	out := Scan(dir)
	if strings.Contains(out, ".git") {
		t.Fatalf(".git should be omitted from listing:\n%s", out)
	}
	cmdIdx := strings.Index(out, "cmd/")
	mainIdx := strings.Index(out, "main.go")
	if cmdIdx < 0 || mainIdx < 0 || cmdIdx > mainIdx {
		t.Fatalf("directories should be listed before files:\n%s", out)
	}
}

func TestListingTruncates(t *testing.T) {
	dir := t.TempDir()
	for i := range maxEntries + 10 {
		writeFile(t, dir, "f"+strconv.Itoa(i)+".txt", "x")
	}
	out := Scan(dir)
	if !strings.Contains(out, "more)") {
		t.Fatalf("expected overflow marker for >%d entries:\n%s", maxEntries, out)
	}
}

func TestReadmeHead(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Project\n\nA tool that does things.\n")
	out := Scan(dir)
	if !strings.Contains(out, "README (first lines):") {
		t.Fatalf("readme header missing:\n%s", out)
	}
	if !strings.Contains(out, "# Project") || !strings.Contains(out, "A tool that does things.") {
		t.Fatalf("readme content missing:\n%s", out)
	}
}

func TestReadmeHeadLineCap(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := range maxReadmeLines + 30 {
		sb.WriteString("line ")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteByte('\n')
	}
	writeFile(t, dir, "readme", sb.String())
	out := Scan(dir)
	readmeSection := out[strings.Index(out, "README (first lines):"):]
	if got := strings.Count(readmeSection, "line "); got > maxReadmeLines {
		t.Fatalf("readme lines = %d, want <= %d:\n%s", got, maxReadmeLines, readmeSection)
	}
}

func TestBoundCapsOutput(t *testing.T) {
	if got := bound(strings.Repeat("x", maxOutputBytes+100)); len(got) <= maxOutputBytes+len("\n… (snapshot truncated)") {
		if !strings.Contains(got, "snapshot truncated") {
			t.Fatalf("oversized output not truncated, len=%d", len(got))
		}
	}
}

func TestChangeSummary(t *testing.T) {
	if got := changeSummary(""); got != "clean" {
		t.Fatalf("empty porcelain = %q, want clean", got)
	}
	got := changeSummary("M  a.go\n M b.go\n?? c.go\n")
	if !strings.Contains(got, "1 staged") || !strings.Contains(got, "1 modified") || !strings.Contains(got, "1 untracked") {
		t.Fatalf("changeSummary = %q, want staged/modified/untracked counts", got)
	}
}

func TestGitSummary(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "trunk")
	writeFile(t, dir, "a.txt", "hello")
	run("add", "a.txt")
	run("commit", "-m", "initial commit")
	writeFile(t, dir, "b.txt", "untracked")

	out := Scan(dir)
	if !strings.Contains(out, "Git: branch trunk") {
		t.Fatalf("branch not reported:\n%s", out)
	}
	if !strings.Contains(out, "untracked") {
		t.Fatalf("untracked change not reported:\n%s", out)
	}
	if !strings.Contains(out, "last commit") || !strings.Contains(out, "initial commit") {
		t.Fatalf("last commit not reported:\n%s", out)
	}
}

func TestOverviewRespectsDisableEnv(t *testing.T) {
	t.Setenv("PLUTO_REPO_SCAN", "off")
	if got := Overview(); got != "" {
		t.Fatalf("PLUTO_REPO_SCAN=off should disable snapshot, got:\n%s", got)
	}
	t.Setenv("PLUTO_REPO_SCAN", "")
	if got := Overview(); !strings.Contains(got, "Repository snapshot") {
		t.Fatalf("Overview should produce a snapshot when enabled, got:\n%s", got)
	}
}
