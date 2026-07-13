package widgets

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// TreeNode is one entry in a Tree. Path is opaque to the widget: it is passed to
// the loader and returned on selection.
type TreeNode struct {
	Name  string
	Path  string
	IsDir bool

	depth    int
	expanded bool
	loaded   bool
	children []*TreeNode
}

// TreeStyle carries the styles a Tree renders with.
type TreeStyle struct {
	Cursor lipgloss.Style
	Dir    lipgloss.Style
	File   lipgloss.Style
}

// Tree is a keyboard-driven, expandable file explorer. It is domain-free:
// directory children are produced by an injected loader and per-path name styles
// by SetStatus, so the widget never touches the filesystem or git.
type Tree struct {
	root     *TreeNode
	loader   func(path string) []*TreeNode
	style    TreeStyle
	status   map[string]lipgloss.Style
	hideRoot bool
	width    int
	height   int
	cursor   int
	offset   int
	visible  []*TreeNode
}

// NewTree builds a Tree rooted at root, loading and expanding the top level.
func NewTree(root *TreeNode, loader func(path string) []*TreeNode, style TreeStyle) *Tree {
	root.IsDir = true
	t := &Tree{root: root, loader: loader, style: style}
	t.load(root)
	root.expanded = true
	t.rebuild()
	return t
}

// NewList builds a flat, root-hidden Tree over items — a simple navigable list.
func NewList(items []*TreeNode, style TreeStyle) *Tree {
	root := &TreeNode{IsDir: true, expanded: true, loaded: true, children: items}
	t := &Tree{root: root, style: style, hideRoot: true}
	t.rebuild()
	return t
}

func (t *Tree) load(n *TreeNode) {
	if n.loaded || t.loader == nil {
		return
	}
	n.children = t.loader(n.Path)
	n.loaded = true
}

// rebuild flattens the expanded tree into the visible slice and re-applies markers.
func (t *Tree) rebuild() {
	t.visible = t.visible[:0]
	var walk func(n *TreeNode, depth int)
	walk = func(n *TreeNode, depth int) {
		n.depth = depth
		t.visible = append(t.visible, n)
		if n.IsDir && n.expanded {
			for _, c := range n.children {
				walk(c, depth+1)
			}
		}
	}
	if t.hideRoot {
		for _, c := range t.root.children {
			walk(c, 0)
		}
	} else {
		walk(t.root, 0)
	}
	if t.cursor >= len(t.visible) {
		t.cursor = len(t.visible) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
}

// SetStatus replaces the path→name-style map used to color changed files.
func (t *Tree) SetStatus(s map[string]lipgloss.Style) { t.status = s }

// SetSize sets the render area in cells.
func (t *Tree) SetSize(w, h int) { t.width, t.height = w, h }

// Up/Down move the cursor over the visible rows, clamping at the ends.
func (t *Tree) Up() {
	if t.cursor > 0 {
		t.cursor--
	}
}

func (t *Tree) Down() {
	if t.cursor < len(t.visible)-1 {
		t.cursor++
	}
}

// Selected returns the node under the cursor.
func (t *Tree) Selected() (*TreeNode, bool) {
	if t.cursor < 0 || t.cursor >= len(t.visible) {
		return nil, false
	}
	return t.visible[t.cursor], true
}

// Expand opens the selected directory (loading it on first open) or steps into
// it when it is already open.
func (t *Tree) Expand() {
	n, ok := t.Selected()
	if !ok || !n.IsDir {
		return
	}
	if n.expanded {
		if len(n.children) > 0 {
			t.Down()
		}
		return
	}
	t.load(n)
	n.expanded = true
	t.rebuild()
}

// Collapse closes the selected open directory, otherwise jumps to the parent.
func (t *Tree) Collapse() {
	n, ok := t.Selected()
	if !ok {
		return
	}
	if n.IsDir && n.expanded {
		n.expanded = false
		t.rebuild()
		return
	}
	for i := t.cursor - 1; i >= 0; i-- {
		if t.visible[i].depth == n.depth-1 {
			t.cursor = i
			return
		}
	}
}

// Toggle flips the selected directory's expanded state.
func (t *Tree) Toggle() {
	n, ok := t.Selected()
	if !ok || !n.IsDir {
		return
	}
	if n.expanded {
		n.expanded = false
	} else {
		t.load(n)
		n.expanded = true
	}
	t.rebuild()
}

// View renders the tree, scrolled to keep the cursor visible, as exactly height
// rows when a height is set.
func (t *Tree) View() string {
	rows := t.height
	if rows < 1 {
		rows = len(t.visible)
	}
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+rows {
		t.offset = t.cursor - rows + 1
	}
	if t.offset < 0 {
		t.offset = 0
	}
	var b strings.Builder
	for i := 0; i < rows; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		if idx := t.offset + i; idx < len(t.visible) {
			b.WriteString(t.renderRow(t.visible[idx], idx == t.cursor))
		}
	}
	return b.String()
}

func (t *Tree) renderRow(n *TreeNode, selected bool) string {
	w := t.width
	if w < 6 {
		w = 6
	}
	caret := "  "
	if n.IsDir {
		if n.expanded {
			caret = "▾ "
		} else {
			caret = "▸ "
		}
	}
	name := n.Name
	if n.IsDir {
		name += "/"
	}
	// A changed file colors its own name; otherwise fall back to dir/file styling.
	nameStyle, ok := t.status[n.Path]
	if !ok {
		nameStyle = t.style.File
		if n.IsDir {
			nameStyle = t.style.Dir
		}
	}
	gutter := "  "
	if selected {
		gutter = t.style.Cursor.Render("› ")
		nameStyle = nameStyle.Bold(true)
	}
	label := truncCells(strings.Repeat("  ", n.depth)+caret+name, w-2)
	return gutter + nameStyle.Render(label)
}

func truncCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}
