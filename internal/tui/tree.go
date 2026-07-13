package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/syrull/pluto/internal/tui/widgets"
)

const (
	// maxTreeEntries caps how many children a single directory contributes so a
	// pathological folder can't blow up the sidebar.
	maxTreeEntries = 300
	sidebarMinW    = 24
	sidebarMaxW    = 40
	// maxFileView caps the bytes shown when a file has no diff and its contents
	// are displayed instead.
	maxFileView = 200_000
)

func treeStyle() widgets.TreeStyle {
	return widgets.TreeStyle{Cursor: styleTreeCursor, Dir: styleTreeDir, File: styleTreeFile}
}

// newFileTree builds the sidebar file explorer rooted at the working directory.
func newFileTree() *widgets.Tree {
	dir, err := os.Getwd()
	if err != nil || dir == "" {
		return nil
	}
	root := &widgets.TreeNode{Name: filepath.Base(dir), Path: dir, IsDir: true}
	return widgets.NewTree(root, loadDir, treeStyle())
}

// loadDir lists a directory's children (dirs first, then files, alphabetical),
// skipping the .git directory.
func loadDir(path string) []*widgets.TreeNode {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	nodes := make([]*widgets.TreeNode, 0, len(entries))
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		nodes = append(nodes, &widgets.TreeNode{Name: e.Name(), Path: filepath.Join(path, e.Name()), IsDir: e.IsDir()})
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].IsDir != nodes[j].IsDir {
			return nodes[i].IsDir
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	if len(nodes) > maxTreeEntries {
		nodes = nodes[:maxTreeEntries]
	}
	return nodes
}

// buildStatusStyles maps each changed file's absolute path to the color its name
// is rendered in, so git state shows on the filename itself.
func (m model) buildStatusStyles() map[string]lipgloss.Style {
	if len(m.git.status) == 0 {
		return nil
	}
	styles := make(map[string]lipgloss.Style, len(m.git.status))
	for path, code := range m.git.status {
		styles[path] = statusStyle(code)
	}
	return styles
}

// statusStyle maps a git porcelain XY code to a filename color: created/added
// green, deleted red, renamed cyan, modified (default) yellow.
func statusStyle(code string) lipgloss.Style {
	if strings.HasPrefix(code, "??") {
		return styleDiffAdd // untracked = newly created → green
	}
	c := strings.TrimSpace(code)
	if c == "" {
		return styleTreeFile
	}
	switch c[0] {
	case 'A':
		return styleDiffAdd
	case 'D':
		return styleDiffDel
	case 'R':
		return styleToolArgs
	default:
		return styleReview
	}
}

// buildChangesList builds the second sidebar pane: a flat, navigable list of the
// modified/created files (paths relative to the repo root), or nil when clean.
func (m model) buildChangesList() *widgets.Tree {
	if len(m.git.status) == 0 {
		return nil
	}
	type change struct{ rel, abs string }
	changes := make([]change, 0, len(m.git.status))
	for abs := range m.git.status {
		rel := abs
		if m.git.root != "" {
			if r, err := filepath.Rel(m.git.root, abs); err == nil {
				rel = r
			}
		}
		changes = append(changes, change{rel: rel, abs: abs})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].rel < changes[j].rel })
	items := make([]*widgets.TreeNode, len(changes))
	for i, c := range changes {
		items[i] = &widgets.TreeNode{Name: c.rel, Path: c.abs}
	}
	t := widgets.NewList(items, treeStyle())
	t.SetStatus(m.buildStatusStyles())
	return t
}

// statusMap parses `git status --porcelain` into absolute path → XY code,
// resolving rename arrows and quoted paths.
func statusMap(out, root string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		code := line[:2]
		p := strings.TrimSpace(line[3:])
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		p = unquotePath(p)
		if root != "" && !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		m[p] = code
	}
	return m
}

func unquotePath(p string) string {
	if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
		if uq, err := strconv.Unquote(p); err == nil {
			return uq
		}
	}
	return p
}

// activePane returns the tree the arrow keys currently drive.
func (m *model) activePane() *widgets.Tree {
	if m.focus == paneChanges && m.changes != nil {
		return m.changes
	}
	return m.tree
}

// homeKey handles keys while the dashboard is shown: switching/navigating the
// sidebar panes, opening a file's diff, or dismissing the dashboard. It reports
// whether it consumed the key so unclaimed keys fall through to normal handling.
func (m *model) homeKey(ks string) (bool, tea.Cmd) {
	pane := m.activePane()
	switch ks {
	case "tab":
		if m.changes != nil {
			if m.focus == paneTree {
				m.focus = paneChanges
			} else {
				m.focus = paneTree
			}
		}
	case "up":
		if pane != nil {
			pane.Up()
		}
	case "down":
		if pane != nil {
			pane.Down()
		}
	case "left":
		if pane != nil {
			pane.Collapse()
		}
	case "right":
		if pane != nil {
			pane.Expand()
		}
	case "enter":
		if pane != nil {
			if n, ok := pane.Selected(); ok {
				if n.IsDir {
					pane.Toggle()
				} else {
					m.openFileDiff(n.Path)
				}
			}
		}
	case "esc":
		m.showHome = false
	default:
		return false, nil
	}
	return true, nil
}

// openFileDiff shows a file's working-tree diff in a modal, falling back to its
// (syntax-highlighted) contents when there is no diff.
func (m *model) openFileDiff(path string) {
	title, body, isDiff := fileDiff(path, m.git.root)
	m.modal = widgets.NewModal(title, body, modalStyle())
	if isDiff {
		m.modal.Highlight(colorizeDiff)
	} else {
		m.modal.Highlight(func(s string) string { return highlightSource(s, path) })
	}
	m.modalPath = path
	m.modalIsFile = true
	if editorAvailable() {
		m.modal.SetEditable(true)
	}
	m.modal.SetSize(m.width, m.height)
}

func fileDiff(path, root string) (title, body string, isDiff bool) {
	rel := path
	if root != "" {
		if r, err := filepath.Rel(root, path); err == nil {
			rel = r
		}
	}
	if out, err := gitRun("diff", "HEAD", "--", path); err == nil {
		if d := strings.TrimRight(out, "\n"); strings.TrimSpace(d) != "" {
			return "diff · " + rel, d, true
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return rel, "error reading file: " + err.Error(), false
	}
	if len(data) > maxFileView {
		data = data[:maxFileView]
	}
	return rel + " (no diff)", string(data), false
}

// colorizeDiff colors a unified diff line by line with the diff palette.
func colorizeDiff(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = colorDiffLine(ln)
	}
	return strings.Join(lines, "\n")
}

func colorDiffLine(ln string) string {
	switch {
	case strings.HasPrefix(ln, "@@"):
		return stylePrompt.Render(ln)
	case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"):
		return styleDiffHdr.Render(ln)
	case strings.HasPrefix(ln, "diff "), strings.HasPrefix(ln, "index "):
		return styleHint.Render(ln)
	case strings.HasPrefix(ln, "+"):
		return styleDiffAdd.Render(ln)
	case strings.HasPrefix(ln, "-"):
		return styleDiffDel.Render(ln)
	default:
		return styleDiffCtx.Render(ln)
	}
}

// homeBody lays out the launch screen: a full-height file-tree sidebar on the
// left and the dashboard centered in the remaining width.
func (m model) homeBody() string {
	mainH := m.height - footerHeight
	if mainH < 1 {
		mainH = 1
	}
	sidebar := m.sidebarView(mainH)
	rightW := m.width - lipgloss.Width(sidebar) - 1
	if rightW < 20 {
		rightW = 20
	}
	dash := clipLines(m.dashboardView(rightW), mainH)
	right := lipgloss.Place(rightW, mainH, lipgloss.Center, lipgloss.Center, dash)
	return lipgloss.JoinHorizontal(lipgloss.Top, sidebar, " ", right)
}

func (m model) sidebarWidth() int {
	w := m.width / 3
	if w < sidebarMinW {
		w = sidebarMinW
	}
	if w > sidebarMaxW {
		w = sidebarMaxW
	}
	if w > m.width-24 {
		w = m.width - 24
	}
	if w < 12 {
		w = 12
	}
	return w
}

// sidebarView renders the file-tree panel and, when the working tree is dirty, a
// second "Changes" pane below it, exactly height rows tall including the hint.
func (m model) sidebarView(height int) string {
	sw := m.sidebarWidth()
	content := sw - 2
	if content < 6 {
		content = 6
	}
	avail := height - 1 // reserve the hint line beneath the panes
	if avail < 4 {
		avail = 4
	}
	var hintText string
	var panes string
	if m.changes == nil {
		panes = m.paneView(sw, content, avail, "Files", m.tree, m.focus == paneTree)
		hintText = "↑/↓ move · → open · ↵ diff"
	} else {
		changesH := avail / 3
		if changesH < 5 {
			changesH = 5
		}
		if changesH > avail-5 {
			changesH = avail - 5
		}
		if changesH < 4 {
			changesH = 4
		}
		filesH := avail - changesH
		files := m.paneView(sw, content, filesH, "Files", m.tree, m.focus == paneTree)
		changes := m.paneView(sw, content, changesH, "Changes", m.changes, m.focus == paneChanges)
		panes = files + "\n" + changes
		hintText = "↑/↓ move · tab pane · ↵ diff"
	}
	hint := lipgloss.NewStyle().MaxWidth(sw).Render(styleHint.Render(hintText))
	return panes + "\n" + hint
}

// paneView renders one bordered, titled pane of total height boxH, giving the
// tree the interior dimensions and highlighting the border when focused.
func (m model) paneView(sw, content, boxH int, title string, t *widgets.Tree, focused bool) string {
	treeH := boxH - 3 // borders (2) + title (1)
	if treeH < 1 {
		treeH = 1
	}
	body := styleHint.Render("loading…")
	if t != nil {
		t.SetSize(content, treeH)
		body = t.View()
	}
	style := styleTreeBox
	if focused {
		style = styleTreeBoxFocus
	}
	return style.Width(sw).Height(boxH).Render(styleDiffHdr.Render(title) + "\n" + body)
}

func clipLines(s string, n int) string {
	if n < 1 {
		return ""
	}
	if lines := strings.Split(s, "\n"); len(lines) > n {
		return strings.Join(lines[:n], "\n")
	}
	return s
}
