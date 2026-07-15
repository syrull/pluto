package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/tui/widgets"
)

const (
	// maxTreeEntries caps how many children a single directory contributes so a
	// pathological folder can't blow up the sidebar.
	maxTreeEntries = 300
	// maxFinderFiles caps how many files the fuzzy finder ranks so a giant repo
	// can't stall the UI.
	maxFinderFiles = 20_000
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

// newFileTree builds the sidebar file explorer rooted at dir (the active agent's
// working directory), falling back to the process cwd when dir is empty.
func newFileTree(dir string) *widgets.Tree {
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return nil
		}
	}
	if dir == "" {
		return nil
	}
	root := &widgets.TreeNode{Name: filepath.Base(dir), Path: dir, IsDir: true}
	debug.Info(dbgTUI, "file tree rooted", "dir", dir)
	return widgets.NewTree(root, loadDir, treeStyle())
}

// loadDir lists a directory's children (dirs first, then files, alphabetical),
// skipping the .git directory.
func loadDir(path string) []*widgets.TreeNode {
	entries, err := os.ReadDir(path)
	if err != nil {
		debug.Warn(dbgTUI, "loadDir failed", "dir", path, "err", err)
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
	capped := false
	if len(nodes) > maxTreeEntries {
		nodes = nodes[:maxTreeEntries]
		capped = true
	}
	debug.Debug(dbgTUI, fmt.Sprintf("showing %d files in %s", len(nodes), path),
		"dir", path, "files", len(nodes), "capped", capped)
	return nodes
}

// collectFiles lists the fuzzy finder's candidate files as paths relative to
// dir: git's tracked + untracked (gitignore-respecting) set when in a repo,
// otherwise a plain walk from dir that skips .git.
func collectFiles(dir string) []string {
	if out, err := gitRun("-C", dir, "ls-files", "--cached", "--others", "--exclude-standard", "-z"); err == nil {
		var files []string
		for _, p := range strings.Split(out, "\x00") {
			if p == "" {
				continue
			}
			files = append(files, p)
			if len(files) >= maxFinderFiles {
				break
			}
		}
		if len(files) > 0 {
			sort.Strings(files)
			return files
		}
	}
	return walkFiles(dir)
}

// walkFiles walks dir for files (skipping .git), returning paths relative to dir.
func walkFiles(dir string) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(dir, path); err == nil {
			files = append(files, rel)
		}
		if len(files) >= maxFinderFiles {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(files)
	return files
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
	if debug.Should(dbgTUI, debug.LevelDebug) {
		names := make([]string, len(changes))
		for i, c := range changes {
			names[i] = c.rel + ":" + strings.TrimSpace(m.git.status[c.abs])
		}
		debug.Debug(dbgTUI, "changes pane built", "files", len(changes), "entries", strings.Join(names, ","))
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

// focusOrder returns the panes Tab cycles through, following the sidebar's
// visual top-to-bottom order: chat, then Agents, the file tree, and Changes
// (only when the tree is dirty). Agents is drawn on top of the sidebar (see
// sidebarView), so a single Tab out of the chat lands on it, and shift+tab
// reverses the same order.
func (m *model) focusOrder() []focusPane {
	order := []focusPane{paneChat, paneAgents, paneTree}
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
	from := m.focus
	m.focus = order[idx]
	debug.Debug(dbgTUI, "focus change", "from", focusName(from), "to", focusName(m.focus), "back", back)
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
	if m.focus == paneAgents {
		return m.agentsKey(ks)
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
	case "-":
		m.toggleCollapse(m.focus)
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
	case "/":
		// Only the Files pane opens the fuzzy finder; elsewhere '/' starts a slash command.
		if m.focus != paneTree {
			m.focus = paneChat
			return false, nil
		}
		m.openFinder()
	case "esc":
		m.focus = paneChat
	default:
		// Any other key (typing, ctrl+*) returns to the chat pane and is handled there.
		m.focus = paneChat
		return false, nil
	}
	return true, nil
}

// agentsKey drives the Agents pane: navigating the list, switching agents,
// creating a new one, and collapsing the pane.
func (m *model) agentsKey(ks string) (bool, tea.Cmd) {
	newRow := len(m.workspaces) // the row past the last agent is "＋ new agent"
	switch ks {
	case "up":
		if m.agentsCursor > 0 {
			m.agentsCursor--
		}
	case "down":
		if m.agentsCursor < newRow {
			m.agentsCursor++
		}
	case "enter":
		if m.agentsCursor >= newRow {
			return true, m.promptNewAgent()
		}
		return true, m.switchTo(m.agentsCursor)
	case "n":
		return true, m.promptNewAgent()
	case "d", "x":
		// Close the agent under the cursor: focus it first so the shared
		// close flow (which acts on the active agent) tears down the right one.
		if m.agentsCursor >= newRow {
			return true, nil
		}
		var cmds []tea.Cmd
		if m.agentsCursor != m.active {
			cmds = append(cmds, m.switchTo(m.agentsCursor))
		}
		cmds = append(cmds, m.promptClose())
		return true, tea.Batch(cmds...)
	case "-":
		m.toggleCollapse(paneAgents)
	case "esc":
		m.focus = paneChat
	default:
		m.focus = paneChat
		return false, nil
	}
	return true, nil
}

// toggleCollapse flips the collapsed state of a sidebar pane.
func (m *model) toggleCollapse(p focusPane) {
	switch p {
	case paneAgents:
		m.collapsedAgents = !m.collapsedAgents
	case paneTree:
		m.collapsedFiles = !m.collapsedFiles
	case paneChanges:
		m.collapsedChanges = !m.collapsedChanges
	}
}

// openFinder opens the fuzzy file picker over the active agent's files,
// remembering the base directory selections resolve against.
func (m *model) openFinder() {
	base := m.activeCwd()
	if base == "" {
		return
	}
	files := collectFiles(base)
	if len(files) == 0 {
		return
	}
	debug.Info(dbgTUI, "fuzzy finder opened", "base", base, "files", len(files))
	m.finderBase = base
	m.finder = widgets.NewFuzzyPicker(
		"find file — type to filter · ↑/↓ move · ↵ open · esc cancel",
		files,
		pickerStyle(),
	)
	m.finder.SetSize(m.width, m.height)
}

// openFinderFile opens a finder selection (a path relative to finderBase) in
// the file/diff modal, reusing the tree's open logic.
func (m *model) openFinderFile(rel string) {
	m.openFileDiff(filepath.Join(m.finderBase, rel))
}

// openFileDiff shows a file's working-tree diff in a modal, falling back to its
// (syntax-highlighted) contents when there is no diff.
func (m *model) openFileDiff(path string) {
	debug.Info(dbgTUI, "open file diff", "path", path)
	title, body, isDiff := fileDiff(path, m.git.root, m.activeCwd())
	m.modal = widgets.NewModal(title, body, modalStyle())
	if isDiff {
		w := diffContentWidth(m.width)
		m.modal.Highlight(func(s string) string { return renderUnifiedDiff(s, w) })
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

func fileDiff(path, root, dir string) (title, body string, isDiff bool) {
	rel := path
	if root != "" {
		if r, err := filepath.Rel(root, path); err == nil {
			rel = r
		}
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return rel, "cannot open a directory", false
	}
	if out, err := gitRunDir(dir, "diff", "HEAD", "--", path); err == nil {
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

// diffContentWidth is the interior width available for the diff body inside the
// modal, mirroring the viewport width the modal wraps content to.
func diffContentWidth(termW int) int {
	if termW <= 0 {
		return defaultWrapWidth
	}
	if w := termW - 8; w > 20 {
		return w
	}
	return 20
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
		body = clipLines(centerVertical(m.dashboardView(w-2), bodyH), bodyH)
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

// sidebarPane describes one stacked sidebar pane for layout.
type sidebarPane struct {
	title     string
	focus     focusPane
	collapsed bool
	tree      *widgets.Tree // nil for the Agents pane
	agents    bool
}

// sidebarView stacks the Agents, Files, and (when dirty) Changes panes, each
// collapsible to a single header line, exactly height rows tall including the
// hint. Expanded panes share the space freed by collapsed ones.
func (m model) sidebarView(height int) string {
	sw := m.sidebarWidth()
	content := sw - 2
	if content < 6 {
		content = 6
	}
	avail := height - 1 // reserve the hint line beneath the panes
	if avail < 3 {
		avail = 3
	}

	panes := []sidebarPane{
		{title: "Agents", focus: paneAgents, collapsed: m.collapsedAgents, agents: true},
		{title: "Files", focus: paneTree, collapsed: m.collapsedFiles, tree: m.tree},
	}
	if m.changes != nil {
		panes = append(panes, sidebarPane{title: "Changes", focus: paneChanges, collapsed: m.collapsedChanges, tree: m.changes})
	}

	expanded := 0
	for _, p := range panes {
		if !p.collapsed {
			expanded++
		}
	}
	// Space left for expanded panes after each collapsed pane takes its header row.
	remaining := avail - (len(panes) - expanded)
	if remaining < 0 {
		remaining = 0
	}

	var rows []string
	used, seen := 0, 0
	for _, p := range panes {
		focused := m.focus == p.focus
		if p.collapsed {
			rows = append(rows, collapsedHeader(sw, p.title, focused))
			used++
			continue
		}
		seen++
		h := remaining / expanded
		if seen == expanded { // last expanded pane absorbs the remainder
			h = remaining - (remaining/expanded)*(expanded-1)
		}
		if h < 3 {
			h = 3
		}
		if p.agents {
			rows = append(rows, m.agentsPane(sw, content, h, focused))
		} else {
			rows = append(rows, m.paneView(sw, content, h, p.title, p.tree, focused))
		}
		used += h
	}
	body := strings.Join(rows, "\n")
	for used < avail { // pad so the sidebar always fills its height
		body += "\n"
		used++
	}

	hintText := "tab pane · ↑/↓ move · ↵ open · - collapse"
	if m.focus == paneAgents {
		hintText = "↑/↓ move · ↵ switch · n new · d close · - collapse"
	}
	hint := lipgloss.NewStyle().MaxWidth(sw).Render(styleHint.Render(hintText))
	return body + "\n" + hint
}

// collapsedHeader renders a collapsed pane as just its titled top-border line.
func collapsedHeader(sw int, title string, focused bool) string {
	borderStyle := styleTreeBorder
	if focused {
		borderStyle = styleTreeBorderFocus
	}
	return borderTitle(sw, title, borderStyle)
}

// agentsPane renders the Agents list inside a titled box of total height boxH.
func (m model) agentsPane(sw, content, boxH int, focused bool) string {
	bodyH := boxH - 2
	if bodyH < 1 {
		bodyH = 1
	}
	return titledBox(sw, boxH, "Agents", m.agentsBody(content, bodyH), focused)
}

// agentsBody renders the agent rows plus the "＋ new agent" action, marking the
// active agent, the navigation cursor, and per-agent status.
func (m model) agentsBody(width, height int) string {
	if height < 1 {
		height = 1
	}
	rows := make([]string, 0, len(m.workspaces)+1)
	for i, w := range m.workspaces {
		rows = append(rows, m.agentRow(i, w, width, m.focus == paneAgents && i == m.agentsCursor))
	}
	newSel := m.focus == paneAgents && m.agentsCursor >= len(m.workspaces)
	rows = append(rows, agentActionRow(width, newSel))

	// Keep the cursor row visible within the available height.
	start := 0
	if len(rows) > height {
		cur := m.agentsCursor
		if cur >= len(rows) {
			cur = len(rows) - 1
		}
		if cur >= height {
			start = cur - height + 1
		}
		rows = rows[start : start+height]
	}
	return strings.Join(rows, "\n")
}

// agentRow renders a single agent entry: cursor gutter, number, label, status.
// The active row reads live model state (m.busy/m.git) rather than the
// workspace copy, which is only synced on stash, so its status is never stale.
func (m model) agentRow(i int, w *workspace, width int, selected bool) string {
	gutter := "  "
	if selected {
		gutter = styleTreeCursor.Render("› ")
	}
	name := fmt.Sprintf("%d %s", i+1, m.workspaceLabel(i))
	nameStyle := styleTreeFile
	busy, unread, changes := w.busy, w.unread, len(w.git.status) > 0
	if i == m.active {
		nameStyle = styleTreeDir.Bold(true)
		busy, unread, changes = m.busy, false, len(m.git.status) > 0
	}
	status := agentStatus(busy, unread, changes)
	avail := width - 2 - lipgloss.Width(status)
	if avail < 4 {
		avail = 4
	}
	label := nameStyle.Render(truncCells(name, avail))
	if status != "" {
		label += " " + status
	}
	return gutter + label
}

// agentStatus renders the trailing per-agent status glyph: working, unread
// background progress, or a dirty-tree marker.
func agentStatus(busy, unread, changes bool) string {
	switch {
	case busy:
		return styleWorking.Render("● working")
	case unread:
		return styleReview.Render("• updated")
	case changes:
		return styleHint.Render("✎")
	default:
		return ""
	}
}

// agentActionRow renders the "＋ new agent" affordance beneath the agent list.
func agentActionRow(width int, selected bool) string {
	gutter := "  "
	if selected {
		gutter = styleTreeCursor.Render("› ")
	}
	return gutter + stylePrompt.Render(truncCells("＋ new agent", width-2))
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

// centerVertical pads s with blank lines on top so its content sits centered
// within h rows; it's a no-op when the content is already at least that tall.
func centerVertical(s string, h int) string {
	n := strings.Count(s, "\n") + 1
	if n >= h {
		return s
	}
	return strings.Repeat("\n", (h-n)/2) + s
}
