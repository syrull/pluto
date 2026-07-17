package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/tui/widgets"
)

// ghMaxChecksShown caps how many individual CI checks the detail pane lists.
const ghMaxChecksShown = 6

type ghTab int

const (
	ghTabIssues ghTab = iota
	ghTabPRs
)

type ghPane int

const (
	ghPaneList ghPane = iota
	ghPaneDetail
)

type ghOutcomeKind int

const (
	ghOutcomeNone ghOutcomeKind = iota
	ghOutcomeClose
	ghOutcomeOpenURL
	ghOutcomeDevelop
	ghOutcomeReview
	ghOutcomeFetchChecks
	ghOutcomeCloseIssue
	ghOutcomeMergePR
	ghOutcomeAddContext
)

// ghOutcome tells the model what to do after a key press in the browser. For
// ghOutcomeFetchChecks, pr.Number carries the PR whose CI status to fetch.
type ghOutcome struct {
	kind  ghOutcomeKind
	url   string
	issue ghIssue
	pr    ghPR
}

// ghChecks is the cached CI status for one PR.
type ghChecks struct {
	loading bool
	err     error
	items   []ghCheck
}

// ghModal is the tabbed GitHub browser: an Issues tab and a PRs tab, each a
// navigable list that opens a scrollable detail view with Develop/Review/Open
// actions. It is domain-aware and lives in the tui package.
type ghModal struct {
	issues []ghIssue
	prs    []ghPR

	tab  ghTab
	pane ghPane

	issueCursor, issueOffset int
	prCursor, prOffset       int

	vp      viewport.Model // detail body
	md      *glamour.TermRenderer
	loading bool
	err     error

	// checks caches CI status per PR number so reopening a detail doesn't refetch.
	checks map[int]*ghChecks

	// confirmClose arms the destructive Close action: the first [c] arms it, a
	// second confirms, and any other key disarms it.
	confirmClose bool

	// confirmMerge arms the irreversible Merge action the same way as confirmClose.
	confirmMerge bool

	// added tracks issue numbers already staged into the message context, so the
	// [a] action button reflects membership. Seeded from the model on open.
	added map[int]bool

	width, height int
}

func newGHModal() *ghModal {
	debug.Info(dbgTUI, "github browser opened")
	g := &ghModal{loading: true, vp: viewport.New(), checks: map[int]*ghChecks{}, added: map[int]bool{}}
	g.vp.KeyMap = ghDetailKeyMap()
	g.vp.FillHeight = true // pad short bodies so the modal stays full-page
	return g
}

// SetContext seeds the set of issue numbers already staged into the message
// context so the [a] action button opens in the right state.
func (g *ghModal) SetContext(numbers []int) {
	for _, n := range numbers {
		g.added[n] = true
	}
}

func ghDetailKeyMap() viewport.KeyMap {
	return viewport.KeyMap{
		Up:           key.NewBinding(key.WithKeys("up")),
		Down:         key.NewBinding(key.WithKeys("down")),
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		PageDown:     key.NewBinding(key.WithKeys("pgdown")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
	}
}

// SetData populates the browser once a fetch completes.
func (g *ghModal) SetData(issues []ghIssue, prs []ghPR) {
	g.issues, g.prs, g.loading, g.err = issues, prs, false, nil
	g.clampCursors()
	g.refreshDetail()
}

// SetError records a failed fetch so the browser can report it.
func (g *ghModal) SetError(err error) { g.err, g.loading = err, false }

// SetSize fits the browser to the terminal, rebuilds the markdown renderer at the
// new width, and resizes the detail viewport.
func (g *ghModal) SetSize(w, h int) {
	g.width, g.height = w, h
	cw := g.contentWidth()
	g.md = newRenderer(cw)
	g.vp.SetWidth(cw)
	g.refreshDetail()
}

// SetChecks records a PR's fetched CI status and resizes the detail body to fit.
func (g *ghModal) SetChecks(pr int, checks []ghCheck, err error) {
	g.checks[pr] = &ghChecks{items: checks, err: err}
	g.refreshDetail()
}

// innerWidth is the total modal box width (border and padding included), passed
// to the box's Width.
func (g *ghModal) innerWidth() int {
	w := g.width - 8
	if w < 24 {
		w = 24
	}
	return w
}

// contentWidth is the usable text width inside the box: innerWidth minus the
// rounded border (2) and horizontal padding (2). Full-width content (rules, list
// rows, the wrapped body) is sized to this so nothing spills onto a second row.
func (g *ghModal) contentWidth() int {
	w := g.innerWidth() - 4
	if w < 12 {
		w = 12
	}
	return w
}

// boxHeight is the interior content height of the modal box (border excluded),
// sized to fill the screen like the file/diff modal.
func (g *ghModal) boxHeight() int {
	h := g.height - 5
	if h < 6 {
		h = 6
	}
	return h
}

// detailBodyHeight is the number of rows the scrollable detail body gets, after
// reserving the meta/title/hint lines, the (possibly wrapped) action rows, and
// the checks pane.
func (g *ghModal) detailBodyHeight() int {
	actionRows := len(packButtons(g.actionButtons(), g.contentWidth()))
	h := g.boxHeight() - 5 - actionRows - g.checksPaneHeight()
	if h < 3 {
		h = 3
	}
	return h
}

// listBodyHeight is the number of list rows shown, after the title/tab/hint lines.
func (g *ghModal) listBodyHeight() int {
	h := g.boxHeight() - 5
	if h < 1 {
		h = 1
	}
	return h
}

func (g *ghModal) cursor() *int {
	if g.tab == ghTabPRs {
		return &g.prCursor
	}
	return &g.issueCursor
}

func (g *ghModal) offset() *int {
	if g.tab == ghTabPRs {
		return &g.prOffset
	}
	return &g.issueOffset
}

func (g *ghModal) count() int {
	if g.tab == ghTabPRs {
		return len(g.prs)
	}
	return len(g.issues)
}

func (g *ghModal) clampCursors() {
	if g.issueCursor >= len(g.issues) {
		g.issueCursor = len(g.issues) - 1
	}
	if g.issueCursor < 0 {
		g.issueCursor = 0
	}
	if g.prCursor >= len(g.prs) {
		g.prCursor = len(g.prs) - 1
	}
	if g.prCursor < 0 {
		g.prCursor = 0
	}
}

func (g *ghModal) moveCursor(d int) {
	n := g.count()
	if n == 0 {
		return
	}
	c := g.cursor()
	*c += d
	if *c < 0 {
		*c = 0
	}
	if *c >= n {
		*c = n - 1
	}
}

func (g *ghModal) selectedIssue() (ghIssue, bool) {
	if g.tab != ghTabIssues || g.issueCursor < 0 || g.issueCursor >= len(g.issues) {
		return ghIssue{}, false
	}
	return g.issues[g.issueCursor], true
}

func (g *ghModal) selectedPR() (ghPR, bool) {
	if g.tab != ghTabPRs || g.prCursor < 0 || g.prCursor >= len(g.prs) {
		return ghPR{}, false
	}
	return g.prs[g.prCursor], true
}

func (g *ghModal) selectedURL() string {
	if is, ok := g.selectedIssue(); ok {
		return is.URL
	}
	if pr, ok := g.selectedPR(); ok {
		return pr.URL
	}
	return ""
}

func (g *ghModal) prByNumber(n int) (ghPR, bool) {
	for _, pr := range g.prs {
		if pr.Number == n {
			return pr, true
		}
	}
	return ghPR{}, false
}

// reviewOutcome resolves the Review action: the selected PR, or the PR linked to
// the selected issue.
func (g *ghModal) reviewOutcome() (ghOutcome, bool) {
	if pr, ok := g.selectedPR(); ok {
		return ghOutcome{kind: ghOutcomeReview, pr: pr}, true
	}
	if is, ok := g.selectedIssue(); ok && is.LinkedPR != 0 {
		if pr, ok := g.prByNumber(is.LinkedPR); ok {
			return ghOutcome{kind: ghOutcomeReview, pr: pr}, true
		}
	}
	return ghOutcome{}, false
}

// detailPR returns the PR whose CI status the open detail should show: the
// selected PR, or the PR linked to the selected issue.
func (g *ghModal) detailPR() (int, bool) {
	if pr, ok := g.selectedPR(); ok {
		return pr.Number, true
	}
	if is, ok := g.selectedIssue(); ok && is.LinkedPR != 0 {
		return is.LinkedPR, true
	}
	return 0, false
}

// checksPaneHeight is the number of rows the checks pane occupies for the current
// selection, used to size the scrollable body above it.
func (g *ghModal) checksPaneHeight() int {
	return len(g.checksPane(g.contentWidth()))
}

// checksPane renders the bottom CI pane for the open detail as a slice of lines:
// a separator rule followed by a summary and the individual checks. It is empty
// when nothing is selected.
func (g *ghModal) checksPane(width int) []string {
	pr, hasPR := g.detailPR()
	if !hasPR {
		if _, ok := g.selectedIssue(); ok {
			return []string{styleHint.Render(strings.Repeat("─", width)), styleHint.Render("Checks: none (no linked PR)")}
		}
		return nil
	}
	lines := []string{styleHint.Render(strings.Repeat("─", width))}
	c := g.checks[pr]
	switch {
	case c == nil || c.loading:
		lines = append(lines, styleHint.Render(fmt.Sprintf("Checks for PR #%d: loading…", pr)))
	case c.err != nil:
		lines = append(lines, styleErr.Render(truncCells("Checks: "+widgets.Sanitize(c.err.Error()), width)))
	case len(c.items) == 0:
		lines = append(lines, styleHint.Render("Checks: none reported"))
	default:
		lines = append(lines, checksSummary(c.items))
		shown := c.items
		if len(shown) > ghMaxChecksShown {
			shown = shown[:ghMaxChecksShown]
		}
		for _, ck := range shown {
			lines = append(lines, checkLine(ck, width))
		}
		if len(c.items) > ghMaxChecksShown {
			lines = append(lines, styleHint.Render(fmt.Sprintf("  +%d more", len(c.items)-ghMaxChecksShown)))
		}
	}
	return lines
}

// checksSummary tallies the checks by bucket into a one-line colored summary.
func checksSummary(items []ghCheck) string {
	var pass, fail, pending, other int
	for _, c := range items {
		switch c.Bucket {
		case "pass":
			pass++
		case "fail":
			fail++
		case "pending":
			pending++
		default:
			other++
		}
	}
	var parts []string
	if pass > 0 {
		parts = append(parts, styleDiffAdd.Render(fmt.Sprintf("✓ %d passed", pass)))
	}
	if fail > 0 {
		parts = append(parts, styleErr.Render(fmt.Sprintf("✗ %d failing", fail)))
	}
	if pending > 0 {
		parts = append(parts, styleReview.Render(fmt.Sprintf("● %d pending", pending)))
	}
	if other > 0 {
		parts = append(parts, styleHint.Render(fmt.Sprintf("• %d other", other)))
	}
	return styleDiffHdr.Render("Checks ") + strings.Join(parts, styleHint.Render(" · "))
}

func checkLine(c ghCheck, width int) string {
	label := c.Name
	if c.Workflow != "" && c.Workflow != c.Name {
		label = c.Workflow + " / " + c.Name
	}
	return "  " + ghCheckGlyph(c.Bucket) + " " + styleModel.Render(truncCells(label, width-4))
}

func ghCheckGlyph(bucket string) string {
	switch bucket {
	case "pass":
		return styleDiffAdd.Render("✓")
	case "fail":
		return styleErr.Render("✗")
	case "pending":
		return styleReview.Render("●")
	case "cancel":
		return styleHint.Render("⊘")
	case "skipping":
		return styleHint.Render("−")
	default:
		return styleHint.Render("•")
	}
}

// handleKey processes a key press. It reports whether it consumed the key; when
// it returns false the caller forwards the event to the detail viewport so it
// scrolls.
func (g *ghModal) handleKey(ks string) (bool, ghOutcome) {
	if ks == "esc" {
		if g.pane == ghPaneDetail {
			g.pane = ghPaneList
			g.confirmClose = false
			g.confirmMerge = false
			return true, ghOutcome{}
		}
		return true, ghOutcome{kind: ghOutcomeClose}
	}
	if g.loading || g.err != nil {
		return true, ghOutcome{}
	}
	if g.pane == ghPaneDetail {
		return g.detailKey(ks)
	}
	return g.listKey(ks)
}

func (g *ghModal) listKey(ks string) (bool, ghOutcome) {
	switch ks {
	case "tab", "shift+tab", "left", "right":
		g.tab = 1 - g.tab
		g.refreshDetail()
	case "up", "k":
		g.moveCursor(-1)
	case "down", "j":
		g.moveCursor(1)
	case "enter":
		if g.count() == 0 {
			return true, ghOutcome{}
		}
		g.pane = ghPaneDetail
		g.confirmClose = false
		g.confirmMerge = false
		out := g.openChecks()
		g.refreshDetail()
		g.vp.GotoTop()
		return true, out
	case "o":
		if u := g.selectedURL(); u != "" {
			return true, ghOutcome{kind: ghOutcomeOpenURL, url: u}
		}
	}
	return true, ghOutcome{}
}

// openChecks marks the open detail's PR checks as loading and returns a fetch
// outcome, unless they are already cached (or there is no associated PR).
func (g *ghModal) openChecks() ghOutcome {
	pr, ok := g.detailPR()
	if !ok {
		return ghOutcome{}
	}
	if _, cached := g.checks[pr]; cached {
		return ghOutcome{}
	}
	g.checks[pr] = &ghChecks{loading: true}
	return ghOutcome{kind: ghOutcomeFetchChecks, pr: ghPR{Number: pr}}
}

func (g *ghModal) detailKey(ks string) (bool, ghOutcome) {
	if ks != "c" {
		g.confirmClose = false // any other key disarms a pending close
	}
	if ks != "m" {
		g.confirmMerge = false // any other key disarms a pending merge
	}
	switch ks {
	case "up", "down", "pgup", "pgdown", "ctrl+u", "ctrl+d":
		return false, ghOutcome{} // forwarded to the viewport for scrolling
	case "o":
		if u := g.selectedURL(); u != "" {
			return true, ghOutcome{kind: ghOutcomeOpenURL, url: u}
		}
	case "a":
		if is, ok := g.selectedIssue(); ok {
			g.added[is.Number] = !g.added[is.Number]
			return true, ghOutcome{kind: ghOutcomeAddContext, issue: is}
		}
	case "d":
		if is, ok := g.selectedIssue(); ok && is.LinkedPR == 0 {
			return true, ghOutcome{kind: ghOutcomeDevelop, issue: is}
		}
	case "r":
		if out, ok := g.reviewOutcome(); ok {
			return true, out
		}
	case "c":
		if is, ok := g.closableIssue(); ok {
			if !g.confirmClose {
				g.confirmClose = true // arm; require a second [c] to confirm
				return true, ghOutcome{}
			}
			g.confirmClose = false
			return true, ghOutcome{kind: ghOutcomeCloseIssue, issue: is}
		}
	case "m":
		if pr, ok := g.selectedPR(); ok {
			if pr.Draft {
				// A draft can't be merged; let the model surface the notice.
				return true, ghOutcome{kind: ghOutcomeMergePR, pr: pr}
			}
			if !g.confirmMerge {
				g.confirmMerge = true // arm; require a second [m] to confirm
				return true, ghOutcome{}
			}
			g.confirmMerge = false
			return true, ghOutcome{kind: ghOutcomeMergePR, pr: pr}
		}
	}
	return true, ghOutcome{}
}

// closableIssue returns the selected issue when it is an open issue that can be
// closed.
func (g *ghModal) closableIssue() (ghIssue, bool) {
	is, ok := g.selectedIssue()
	if !ok || strings.EqualFold(is.State, "closed") {
		return ghIssue{}, false
	}
	return is, true
}

// BackToList returns the browser to the list pane, e.g. after an issue closes.
func (g *ghModal) BackToList() {
	g.pane = ghPaneList
	g.confirmClose = false
	g.confirmMerge = false
}

// Update forwards scroll and wheel events to the detail viewport.
func (g *ghModal) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	g.vp, cmd = g.vp.Update(msg)
	return cmd
}

// refreshDetail resizes the body viewport to fit the current checks pane and
// re-renders the selected item's body into it.
func (g *ghModal) refreshDetail() {
	g.vp.SetHeight(g.detailBodyHeight())
	g.vp.SetContent(lipgloss.NewStyle().Width(g.contentWidth()).Render(g.detailBody()))
}

func (g *ghModal) detailBody() string {
	if is, ok := g.selectedIssue(); ok {
		return g.renderBody(is.Body)
	}
	if pr, ok := g.selectedPR(); ok {
		return g.renderBody(pr.Body)
	}
	return ""
}

// renderBody renders a sanitized issue/PR body as markdown, falling back to the
// plain sanitized text when no renderer is available or rendering fails.
func (g *ghModal) renderBody(raw string) string {
	s := strings.TrimSpace(widgets.Sanitize(raw))
	if s == "" {
		return styleHint.Render("(no description)")
	}
	if g.md != nil {
		if out, err := g.md.Render(s); err == nil {
			return strings.TrimRight(out, "\n")
		}
	}
	return s
}

// View renders the browser centered over the terminal, filling the screen with a
// fixed full-page box like the file/diff modal.
func (g *ghModal) View() string {
	cw := g.contentWidth()
	var body string
	switch {
	case g.loading:
		body = g.chrome(cw, styleHint.Render("loading issues and pull requests…"))
	case g.err != nil:
		body = g.chrome(cw, styleErr.Render("✗ "+widgets.Sanitize(g.err.Error())))
	case g.pane == ghPaneDetail:
		body = g.detailView(cw)
	default:
		body = g.listView(cw)
	}
	box := styleModalBox.Width(g.innerWidth()).Height(g.boxHeight()).Render(body)
	return lipgloss.Place(g.width, g.height, lipgloss.Center, lipgloss.Center, box)
}

// chrome renders the title above arbitrary content (loading/error states).
func (g *ghModal) chrome(cw int, content string) string {
	title := styleModalTitle.MaxWidth(cw).Render("GitHub")
	hint := styleHint.Render("esc close")
	return lipgloss.JoinVertical(lipgloss.Left, title, "", content, "", hint)
}

func (g *ghModal) tabBar() string {
	issues := fmt.Sprintf(" Issues (%d) ", len(g.issues))
	prs := fmt.Sprintf(" PRs (%d) ", len(g.prs))
	if g.tab == ghTabIssues {
		return stylePickSel.Render(issues) + "  " + styleHint.Render(prs)
	}
	return styleHint.Render(issues) + "  " + stylePickSel.Render(prs)
}

func (g *ghModal) listView(cw int) string {
	var b strings.Builder
	b.WriteString(styleModalTitle.MaxWidth(cw).Render("GitHub"))
	b.WriteByte('\n')
	b.WriteString(g.tabBar())
	b.WriteString("\n\n")

	rows := g.listBodyHeight()
	n := g.count()
	if n == 0 {
		what := "issues"
		if g.tab == ghTabPRs {
			what = "open pull requests"
		}
		b.WriteString(styleHint.Render("no " + what))
		b.WriteString(strings.Repeat("\n", rows-1)) // fill the pane so the hint sits at the bottom
		b.WriteString("\n")
		b.WriteString(styleHint.Render("tab switch · esc close"))
		return b.String()
	}

	cur, off := *g.cursor(), g.offset()
	if cur < *off {
		*off = cur
	}
	if cur >= *off+rows {
		*off = cur - rows + 1
	}
	if *off < 0 {
		*off = 0
	}
	// Emit exactly `rows` lines (blank-padded) so the hint stays pinned to the bottom.
	for i := 0; i < rows; i++ {
		if idx := *off + i; idx < n {
			b.WriteString(g.listRow(idx, idx == cur, cw-2))
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(styleHint.Render("↑/↓ move · ↵ open · tab switch · o browser · esc close"))
	return b.String()
}

func (g *ghModal) listRow(idx int, selected bool, w int) string {
	var num, title, marker string
	if g.tab == ghTabIssues {
		is := g.issues[idx]
		num = fmt.Sprintf("#%d", is.Number)
		title = is.Title
		if is.LinkedPR != 0 {
			marker = fmt.Sprintf("  → PR #%d", is.LinkedPR)
		}
	} else {
		pr := g.prs[idx]
		num = fmt.Sprintf("#%d", pr.Number)
		title = pr.Title
		if pr.Draft {
			marker = "  (draft)"
		}
	}
	line := truncCells(oneLine(num+"  "+title+marker), w)
	if selected {
		return stylePickSel.Render("▸ " + line)
	}
	return "  " + styleModel.Render(line)
}

func (g *ghModal) detailView(cw int) string {
	var meta, heading string
	if is, ok := g.selectedIssue(); ok {
		meta = fmt.Sprintf("Issue #%d · %s", is.Number, is.State)
		if is.Author != "" {
			meta += " · @" + is.Author
		}
		if opened := openedAgo(is.CreatedAt, time.Now()); opened != "" {
			meta += " · opened " + opened
		}
		if len(is.Labels) > 0 {
			meta += " · " + strings.Join(is.Labels, ", ")
		}
		heading = is.Title
		if is.LinkedPR != 0 {
			meta += fmt.Sprintf(" · linked PR #%d", is.LinkedPR)
		}
	} else if pr, ok := g.selectedPR(); ok {
		meta = fmt.Sprintf("PR #%d · %s", pr.Number, pr.State)
		if pr.Draft {
			meta += " · draft"
		}
		if pr.Branch != "" {
			meta += " · " + pr.Branch
		}
		if pr.Author != "" {
			meta += " · @" + pr.Author
		}
		if opened := openedAgo(pr.CreatedAt, time.Now()); opened != "" {
			meta += " · opened " + opened
		}
		heading = pr.Title
	}

	title := styleModalTitle.MaxWidth(cw).Render(widgets.Sanitize(heading))
	metaLine := styleHint.MaxWidth(cw).Render(widgets.Sanitize(meta))
	hint := styleHint.Render("↑/↓ scroll · esc back")

	segments := []string{metaLine, title, "", g.vp.View()}
	segments = append(segments, g.checksPane(cw)...)
	segments = append(segments, "")
	segments = append(segments, packButtons(g.actionButtons(), cw)...)
	segments = append(segments, hint)
	return lipgloss.JoinVertical(lipgloss.Left, segments...)
}

// actionButtons builds the action-button row for the open detail: Develop/Review
// and Add-to-Context/Close for an issue, Review/Merge for a PR, plus Open.
func (g *ghModal) actionButtons() []string {
	var actions []string
	if is, ok := g.selectedIssue(); ok {
		if is.LinkedPR != 0 {
			actions = append(actions, styleShowBtn.Render(fmt.Sprintf(" [r] Review PR #%d ", is.LinkedPR)))
		} else {
			actions = append(actions, styleShowBtn.Render(" [d] Develop "))
		}
		if g.added[is.Number] {
			actions = append(actions, styleAddBtn.Render(" [a] ✓ In context "))
		} else {
			actions = append(actions, styleAddBtn.Render(" [a] Add to Context "))
		}
		if g.confirmClose {
			actions = append(actions, styleErrBtn.Render(" [c] confirm close "))
		} else {
			actions = append(actions, styleCloseBtn.Render(" [c] Close "))
		}
	} else if pr, ok := g.selectedPR(); ok {
		actions = append(actions, styleShowBtn.Render(" [r] Review "))
		switch {
		case pr.Draft:
			actions = append(actions, styleHint.Render(" [m] Merge (draft) "))
		case g.confirmMerge:
			actions = append(actions, styleErrBtn.Render(" [m] confirm merge "))
		default:
			actions = append(actions, styleCloseBtn.Render(" [m] Merge "))
		}
	}
	return append(actions, styleCopyBtn.Render(" [o] Open in browser "))
}

// packButtons lays action buttons across as many lines as needed so no line
// exceeds width, breaking only between buttons so a button is never split.
func packButtons(buttons []string, width int) []string {
	var lines []string
	cur, curW := "", 0
	for _, b := range buttons {
		bw := lipgloss.Width(b)
		switch {
		case cur == "":
			cur, curW = b, bw
		case curW+2+bw <= width:
			cur += "  " + b
			curW += 2 + bw
		default:
			lines = append(lines, cur)
			cur, curW = b, bw
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
