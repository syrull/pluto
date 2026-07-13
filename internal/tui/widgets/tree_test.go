package widgets

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func testLoader(p string) []*TreeNode {
	switch p {
	case "/r":
		return []*TreeNode{
			{Name: "a", Path: "/r/a", IsDir: true},
			{Name: "f.go", Path: "/r/f.go"},
		}
	case "/r/a":
		return []*TreeNode{{Name: "b.go", Path: "/r/a/b.go"}}
	}
	return nil
}

func newTestTree() *Tree {
	return NewTree(&TreeNode{Name: "r", Path: "/r"}, testLoader, TreeStyle{})
}

func TestTreeInitialVisible(t *testing.T) {
	tr := newTestTree()
	if n, _ := tr.Selected(); n.Path != "/r" {
		t.Fatalf("cursor should start at root, got %s", n.Path)
	}
	if len(tr.visible) != 3 { // root, a (collapsed), f.go
		t.Fatalf("visible = %d, want 3", len(tr.visible))
	}
}

func TestTreeExpandCollapse(t *testing.T) {
	tr := newTestTree()
	tr.Down()
	if n, _ := tr.Selected(); n.Path != "/r/a" {
		t.Fatalf("want a, got %s", n.Path)
	}
	tr.Expand()
	if len(tr.visible) != 4 {
		t.Fatalf("after expand visible = %d, want 4", len(tr.visible))
	}
	tr.Down()
	if n, _ := tr.Selected(); n.Path != "/r/a/b.go" {
		t.Fatalf("want b.go, got %s", n.Path)
	}
	tr.Collapse() // on a file, jumps to parent
	if n, _ := tr.Selected(); n.Path != "/r/a" {
		t.Fatalf("collapse on file should select parent a, got %s", n.Path)
	}
	tr.Collapse() // closes a
	if len(tr.visible) != 3 {
		t.Fatalf("after collapse visible = %d, want 3", len(tr.visible))
	}
}

func TestTreeStatusColorsName(t *testing.T) {
	tr := newTestTree()
	tr.SetSize(24, 5)
	before := tr.View()
	tr.SetStatus(map[string]lipgloss.Style{"/r/f.go": lipgloss.NewStyle().Foreground(lipgloss.Color("1"))})
	after := tr.View()
	if before == after {
		t.Fatalf("SetStatus should recolor a changed file's name:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestTreeViewHeightFixed(t *testing.T) {
	tr := newTestTree()
	tr.SetSize(20, 2)
	if got := strings.Count(tr.View(), "\n") + 1; got != 2 {
		t.Fatalf("View lines = %d, want 2", got)
	}
}

func TestTreeViewShowsCursorAndNames(t *testing.T) {
	tr := newTestTree()
	tr.SetSize(24, 5)
	out := tr.View()
	for _, want := range []string{"r/", "›", "f.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View missing %q:\n%s", want, out)
		}
	}
}
