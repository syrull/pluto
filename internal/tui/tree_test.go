package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

func TestLoadDirSkipsGitAndSortsDirsFirst(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{".git", "zdir"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "afile.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	nodes := loadDir(dir)
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2 (.git skipped)", len(nodes))
	}
	if !nodes[0].IsDir || nodes[0].Name != "zdir" {
		t.Fatalf("dirs should sort first, got %+v", nodes[0])
	}
	if nodes[1].Name != "afile.go" {
		t.Fatalf("file should come second, got %+v", nodes[1])
	}
}

func TestStatusMapParsesCodesAndRenames(t *testing.T) {
	out := " M internal/a.go\n?? new.txt\nR  old.go -> internal/new.go\n"
	m := statusMap(out, "/repo")
	if m["/repo/internal/a.go"] != " M" {
		t.Fatalf("a.go code = %q", m["/repo/internal/a.go"])
	}
	if m["/repo/new.txt"] != "??" {
		t.Fatalf("new.txt code = %q", m["/repo/new.txt"])
	}
	if _, ok := m["/repo/internal/new.go"]; !ok {
		t.Fatalf("rename target missing: %+v", m)
	}
}

func TestBuildStatusStylesOnlyChangedFiles(t *testing.T) {
	m := model{git: gitInfo{root: "/repo", status: map[string]string{"/repo/internal/tui/x.go": " M"}}}
	styles := m.buildStatusStyles()
	if _, ok := styles["/repo/internal/tui/x.go"]; !ok {
		t.Fatal("changed file should have a status style")
	}
	if _, ok := styles["/repo/internal"]; ok {
		t.Fatal("directories should not be styled (no dot propagation)")
	}
}

func TestStatusStyleColors(t *testing.T) {
	if statusStyle("??").GetForeground() != styleDiffAdd.GetForeground() {
		t.Fatal("untracked (newly created) should be green (styleDiffAdd)")
	}
	if statusStyle(" M").GetForeground() != styleReview.GetForeground() {
		t.Fatal("modified should be yellow (styleReview)")
	}
	if statusStyle("A ").GetForeground() != styleDiffAdd.GetForeground() {
		t.Fatal("added should be green (styleDiffAdd)")
	}
	if statusStyle("D ").GetForeground() != styleDiffDel.GetForeground() {
		t.Fatal("deleted should be red (styleDiffDel)")
	}
}

func TestFileDiffFallbackToContents(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(p, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	title, body, isDiff := fileDiff(p, "", "")
	if isDiff {
		t.Fatal("a file outside the repo should have no diff")
	}
	if !strings.Contains(body, "hello world") {
		t.Fatalf("fallback should show contents, got %q", body)
	}
	if !strings.Contains(title, "no diff") {
		t.Fatalf("title should note no diff, got %q", title)
	}
}

func TestFileDiffRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	title, body, isDiff := fileDiff(dir, "", "")
	if isDiff {
		t.Fatal("a directory has no diff")
	}
	if !strings.Contains(body, "directory") {
		t.Fatalf("body should explain it is a directory, got %q", body)
	}
	if strings.Contains(body, "is a directory") {
		t.Fatalf("should not surface the raw read error, got %q", body)
	}
	_ = title
}

func TestRenderUnifiedDiffColorsLines(t *testing.T) {
	out := renderUnifiedDiff("@@ -1 +1 @@\n-old\n+new\n ctx", 80)
	if !strings.Contains(out, "old") || !strings.Contains(out, "new") {
		t.Fatalf("render should preserve text: %q", out)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("render should add ANSI: %q", out)
	}
}

func TestOpenFileDiffSetsModal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(p, []byte("hi there"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &model{width: 80, height: 24}
	m.openFileDiff(p)
	if m.modal == nil {
		t.Fatal("openFileDiff should set a modal")
	}
	if !strings.Contains(m.modal.Content(), "hi there") {
		t.Fatalf("modal content = %q", m.modal.Content())
	}
}

func TestHomeArrowsDriveTreeWithoutDismissing(t *testing.T) {
	var tm tea.Model = model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), ""), md: newRenderer(80), input: newInput(80), showHome: true}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	got := tm.(model)
	if got.tree == nil {
		t.Fatal("tree should be built on the first WindowSizeMsg")
	}
	start, _ := got.tree.Selected()

	// Tab follows the sidebar's visual order (Agents first, then Files), so the
	// first Tab focuses Agents and the second reaches the file tree; arrows then
	// drive it.
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := tm.(model); got.focus != paneAgents {
		t.Fatalf("first tab should focus the agents pane, got focus %d", got.focus)
	}
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := tm.(model); got.focus != paneTree {
		t.Fatalf("second tab should focus the tree pane, got focus %d", got.focus)
	}
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	got = tm.(model)
	if !got.showHome {
		t.Fatal("navigating panes should not dismiss the dashboard")
	}
	moved, _ := got.tree.Selected()
	if start != nil && moved != nil && start.Path == moved.Path {
		t.Fatal("down arrow should move the tree selection")
	}
}
