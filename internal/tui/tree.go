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
	// convContentTop is the number of rows above the conversation viewport inside
	// its pane (the top border, which carries the title), used to map a screen row
	// to a transcript line.
	convContentTop = 1
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

// focusOrder returns the panes Tab cycles through, skipping Changes when clean.
func (m *model) focusOrder() []focusPane {
	order := []focusPane{paneChat, paneTree}
	if m.changes != nil {
		order = append(order, paneChanges)
	}
	return order
}

// cycleFocus moves keyboard focus to the next (or previous) pane.
func (m *model) cycleFocus(back bool) {
	order := m.focusOrder()
	idx := 0
	for i, p := range order {
		if p == m.focus {
			idx = i
			break
		}
	}
	if back {
		idx = (idx - 1 + len(order)) % len(order)
	} else {
		idx = (idx + 1) % len(order)
	}
	m.focus = order[idx]
}

// paneKey handles pane switching and, when a sidebar pane holds focus, its
// navigation: moving the cursor, expanding/collapsing, or opening a file's diff.
// It reports whether it consumed the key so unclaimed keys fall through to the
// chat/input handling.
func (m *model) paneKey(ks string) (bool, tea.Cmd) {
	switch ks {
	case "tab":
		m.cycleFocus(false)
		return true, nil
	case "shift+tab":
		m.cycleFocus(true)
		return true, nil
	}
	if m.focus != paneTree && m.focus != paneChanges {
		return false, nil
	}
	p := m.activePane()
	switch ks {
	case "up":
		if p != nil {
			p.Up()
		}
	case "down":
		if p != nil {
			p.Down()
		}
	case "left":
		if p != nil {
			p.Collapse()
		}
	case "right":
		if p != nil {
			p.Expand()
		}
	case "enter":
		if p != nil {
			if n, ok := p.Selected(); ok {
				if n.IsDir {
					p.Toggle()
				} else {
					m.openFileDiff(n.Path)
				}
			}
		}
	case "esc":
		m.focus = paneChat
	default:
		// Any other key (typing, ctrl+*) returns to the chat pane and is handled there.
		m.focus = paneChat
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

// mainHeight is the height of the main row (conversation + sidebar), i.e. the
// screen minus the footer pane.
func (m model) mainHeight() int {
	h := m.height - footerHeight
	if h < 1 {
		h = 1
	}
	return h
}

// convOuterWidth is the total width of the conversation pane (borders included),
// the terminal width minus the sidebar and a one-cell gap.
func (m model) convOuterWidth() int {
	if m.width <= 0 {
		return defaultWrapWidth
	}
	w := m.width - m.sidebarWidth() - 1
	if w < 24 {
		w = 24
	}
	return w
}

// contentWidth is the interior width transcript content wraps to inside the
// conversation pane (outer width minus the border).
func (m model) contentWidth() int {
	w := m.convOuterWidth() - 2
	if w < 10 {
		w = 10
	}
	return w
}

// convBodyHeight is the interior height of the conversation pane's viewport
// (main height minus the top and bottom border).
func (m model) convBodyHeight() int {
	h := m.mainHeight() - 2
	if h < 1 {
		h = 1
	}
	return h
}

// mainArea lays out the persistent main row: the conversation pane on the left
// and the file-tree/changes sidebar on the right, clipped to the main height so
// it never crowds the footer.
func (m model) mainArea() string {
	mainH := m.mainHeight()
	sidebar := m.sidebarView(mainH)
	convW := m.width - lipgloss.Width(sidebar) - 1
	if convW < 24 {
		convW = 24
	}
	conv := m.conversationPane(convW, mainH)
	joined := lipgloss.JoinHorizontal(lipgloss.Top, conv, " ", sidebar)
	return clipLines(joined, mainH)
}

// conversationPane renders the transcript (or the launch dashboard while at
// home) inside a box whose top border carries the title, highlighting the
// border when the chat pane holds focus.
func (m model) conversationPane(w, h int) string {
	bodyH := h - 2 // top + bottom border
	if bodyH < 1 {
		bodyH = 1
	}
	title := "Conversation"
	var body string
	switch {
	case m.showHome:
		title = "Pluto"
		body = clipLines(m.dashboardView(w-2), bodyH)
	case m.ready:
		body = m.vp.View()
	default:
		body = clipLines(m.transcript(), bodyH)
	}
	return titledBox(w, h, title, body, m.focus == paneChat)
}

// titledBox renders body inside a rounded box whose top border carries the
// title (e.g. "╭──── Conversation ────╮"), highlighting the border when focused.
func titledBox(w, h int, title, body string, focused bool) string {
	boxStyle, borderStyle := styleTreeBox, styleTreeBorder
	if focused {
		boxStyle, borderStyle = styleTreeBoxFocus, styleTreeBorderFocus
	}
	lines := strings.Split(boxStyle.Width(w).Height(h).Render(body), "\n")
	if len(lines) > 0 {
		lines[0] = borderTitle(w, title, borderStyle)
	}
	return strings.Join(lines, "\n")
}

// borderTitle builds a rounded top border of total width w with the title
// centered in it, falling back to a plain border when the title can't fit.
func borderTitle(w int, title string, borderStyle lipgloss.Style) string {
	inner := w - 2 // span between the corners
	if inner < 0 {
		inner = 0
	}
	label := ""
	if title != "" {
		label = " " + title + " "
	}
	if lipgloss.Width(label) > inner {
		label = ""
	}
	left := (inner - lipgloss.Width(label)) / 2
	right := inner - lipgloss.Width(label) - left
	return borderStyle.Render("╭"+strings.Repeat("─", left)) +
		styleDiffHdr.Render(label) +
		borderStyle.Render(strings.Repeat("─", right)+"╮")
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
		hintText = "tab pane · ↑/↓ move · ↵ diff"
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
		hintText = "tab pane · ↑/↓ move · ↵ diff"
	}
	hint := lipgloss.NewStyle().MaxWidth(sw).Render(styleHint.Render(hintText))
	return panes + "\n" + hint
}

// paneView renders one titled pane of total height boxH, giving the tree the
// interior dimensions and highlighting the border when focused. The title is
// carried in the top border.
func (m model) paneView(sw, content, boxH int, title string, t *widgets.Tree, focused bool) string {
	treeH := boxH - 2 // top + bottom border
	if treeH < 1 {
		treeH = 1
	}
	body := styleHint.Render("loading…")
	if t != nil {
		t.SetSize(content, treeH)
		body = t.View()
	}
	return titledBox(sw, boxH, title, body, focused)
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
