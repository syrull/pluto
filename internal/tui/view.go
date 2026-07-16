package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"golang.org/x/term"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/diff"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tui/widgets"
)

// workingLabel is the status-line indicator shown while the agent runs; it also
// advertises esc as the way to cancel the in-flight request.
const workingLabel = "● working… (esc to cancel)"

// newRenderer uses explicit style rather than glamour.WithAutoStyle() to avoid OSC 11 background-probe leaking onto stdin.
func newRenderer(width int) *glamour.TermRenderer {
	if width <= 0 {
		width = 80
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	return r
}

// glamourStyle picks the markdown theme without probing the terminal for its background color.
func glamourStyle() string {
	if s := os.Getenv("PLUTO_MD_STYLE"); s != "" {
		return s
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return styles.NoTTYStyle
	}
	return styles.DarkStyle
}

func (m *model) renderMarkdown(src string) string {
	if m.md == nil {
		return strings.TrimRight(src, "\n")
	}
	out, err := m.md.Render(src)
	if err != nil {
		return strings.TrimRight(src, "\n")
	}
	return strings.TrimRight(out, "\n")
}

// renderUserLine wraps a user message to the viewport width, indenting
// continuation lines under the prompt so multi-line input doesn't run off
// screen, and appends a chip when the turn carries image attachments.
func (m *model) renderUserLine(in string, atts ...llm.Attachment) string {
	w := m.contentWidth()
	const prefix = "› "
	w -= len(prefix)
	if w < 10 {
		w = 10
	}
	wrapped := styleUser.Width(w).Render(in)
	lines := strings.Split(wrapped, "\n")
	indent := strings.Repeat(" ", len(prefix))
	for i, ln := range lines {
		if i == 0 {
			lines[i] = stylePrompt.Render(prefix) + ln
		} else {
			lines[i] = indent + ln
		}
	}
	if chip := attachmentChip(atts); chip != "" {
		lines = append(lines, indent+styleHint.Render(chip))
	}
	return strings.Join(lines, "\n")
}

func renderEvent(width int, ev agent.Event) string {
	switch ev.Kind {
	case "text":
		return styleModel.Render(ev.Text)
	case "tool_call":
		return renderToolCall(width, ev.Tool, ev.Text)
	case "tool_result":
		return renderToolResult(width, ev.Tool, ev.Text)
	case "error":
		if ev.Tool != "" {
			return styleErr.Render(fmt.Sprintf("✗ %s: %s", ev.Tool, widgets.Sanitize(ev.Text)))
		}
		return styleErr.Render("✗ " + widgets.Sanitize(ev.Text))
	default:
		return widgets.Sanitize(ev.Text)
	}
}

// renderWriteResult renders a write tool's result as a diff.
func renderWriteResult(width int, result string) string {
	return renderDiffResult(width, "write", result)
}

func renderDiffLine(width int, ln string) string {
	if ln == "" {
		return ""
	}
	switch ln[0] {
	case '+':
		return wrapBody("", ln, styleDiffAdd, width)
	case '-':
		return wrapBody("", ln, styleDiffDel, width)
	case diff.GapOp:
		return wrapBody("  ", ln[1:], styleHint, width)
	default:
		return wrapBody("", ln, styleDiffCtx, width)
	}
}

func (m *model) flushStream() {
	if think := strings.TrimSpace(m.streamThink); think != "" {
		m.pushText(m.renderThinkBox(think))
		m.streamThink = ""
	}
	if text := strings.TrimSpace(m.streamText); text != "" {
		m.pushMarkdown(text)
		m.streamText = ""
	}
}

// pushMarkdown commits assistant markdown, rendering each fenced code block with
// its own copy affordance interleaved right after it rather than pooling every
// button at the end of the response.
func (m *model) pushMarkdown(text string) {
	for _, seg := range splitMarkdown(text) {
		rendered := m.renderMarkdown(seg.raw)
		if !seg.isCode {
			if strings.TrimSpace(rendered) != "" {
				m.pushText(rendered)
			}
			continue
		}
		m.pushText(rendered)
		m.codeBlocks = append(m.codeBlocks, seg.code)
		m.lines = append(m.lines, entry{text: m.copyAffordance(seg.code), copyID: len(m.codeBlocks)})
	}
}

// copyAffordance renders a code block's copy marker right-aligned beneath the
// block: a clickable button when the mouse is captured, otherwise the ctrl+y hint.
func (m *model) copyAffordance(b codeBlock) string {
	var btn string
	if m.mouse {
		btn = styleCopyBtn.Render(" Copy " + b.title() + " ▸ ")
	} else {
		btn = styleHint.Render("[ctrl+y] copy " + b.title())
	}
	return lipgloss.PlaceHorizontal(m.contentWidth(), lipgloss.Right, btn)
}

func (m *model) renderThinkBox(think string) string {
	hdr := styleThinkHdr.Render("✻ Thinking…")
	body := styleThink.Render(think)
	return styleThinkBox.Width(m.contentWidth()).Render(hdr + "\n" + body)
}

// View renders the transcript layout with the status line and input footer. Frame
// renders are logged at TRACE and coalesced (see logFrame) so the high-volume
// render loop doesn't drown the log by default.
func (m model) View() tea.View {
	start := time.Now()
	body := m.content()
	m.logFrame(body, time.Since(start))
	v := tea.NewView(body)
	v.AltScreen = true
	if m.mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// mouseEnabled reports whether to capture mouse events. Off by default so the
// terminal keeps native click-drag text selection; PLUTO_MOUSE=on enables
// capture for wheel scroll and click-to-open.
func mouseEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PLUTO_MOUSE"))) {
	case "on", "1", "true", "yes":
		return true
	}
	return false
}

// content renders the screen body: a modal/picker when open, otherwise the main
// row (conversation pane plus file-tree/changes sidebar) above the footer pane.
func (m model) content() string {
	if m.approval != nil && m.ready {
		return renderApprovalPrompt(m.width, m.height, m.approval)
	}
	if m.ghm != nil && m.ready {
		return m.ghm.View()
	}
	if m.modal != nil && m.ready {
		return m.modal.View()
	}
	if m.picker != nil && m.ready {
		return m.picker.View()
	}
	if m.finder != nil && m.ready {
		return m.finder.View()
	}
	main := m.mainArea()
	if m.cmdMenu != nil {
		main = m.overlayCommandMenu(main)
	}
	return main + "\n" + m.footerPane()
}

// overlayCommandMenu draws the slash-command popup over the bottom rows of the
// main area so it sits directly above the footer without changing the overall
// height. It's a no-op when the menu has no matches or the screen is too short.
func (m model) overlayCommandMenu(main string) string {
	lines := strings.Split(main, "\n")
	if len(lines) < 4 {
		return main
	}
	w := m.width
	if w <= 0 {
		w = defaultWrapWidth
	}
	menu := m.cmdMenu.View(w, len(lines)-3)
	if menu == "" {
		return main
	}
	menuLines := strings.Split(menu, "\n")
	if len(menuLines) > len(lines) {
		menuLines = menuLines[len(menuLines)-len(lines):]
	}
	start := len(lines) - len(menuLines)
	for i, ml := range menuLines {
		lines[start+i] = ml
	}
	return strings.Join(lines, "\n")
}

// footerPane renders the footer: a transient notice line above a bordered box
// holding the status line and the input box, giving them a clear separation from
// the panes above. The border is highlighted while the chat pane holds focus.
func (m model) footerPane() string {
	w := m.width
	if w <= 0 {
		w = defaultWrapWidth
	}
	inW := w - 2
	if inW < 10 {
		inW = 10
	}
	status := lipgloss.NewStyle().MaxWidth(inW).Render(m.modelStatus())
	box := styleTreeBox
	if m.focus == paneChat {
		box = styleTreeBoxFocus
	}
	pane := box.Width(w).Render(status + "\n" + m.footer())
	return m.notifications() + "\n" + pane
}

// notifications renders the notifications widget shown directly above the status
// line: a single line carrying the latest transient notice, or blank when there
// is none. Clipping to the viewport width keeps a long notice from wrapping and
// pushing the status line and input off screen.
func (m model) notifications() string {
	if m.notice == "" {
		return ""
	}
	w := m.width
	if w <= 0 {
		w = defaultWrapWidth
	}
	return styleHint.MaxWidth(w).Render(m.notice)
}

func (m model) modelStatus() string {
	sep := styleHint.Render(" · ")

	name := "no provider"
	if m.agent != nil {
		name = m.agent.ProviderName()
	}

	var spans, plain []string
	add := func(styled, raw string) {
		spans = append(spans, styled)
		plain = append(plain, raw)
	}

	shortName := shortModelName(name)
	add(styleStatusModel.Render(shortName), shortName)

	if m.agent != nil {
		if th, ok := m.agent.Thinker(); ok {
			raw := "T:" + thinkStatusText(th.ThinkLevel())
			add(styleStatusThink.Render(raw), raw)
		}
		if used, window, ok := m.agent.ContextUsage(); ok && window > 0 {
			raw := fmt.Sprintf("C: %d%% / %s", used*100/window, formatTokens(window))
			add(styleStatusCtx.Render(raw), raw)
		}
		if m.agent.LearnMode() {
			add(styleStatusLearn.Render("learn"), "learn")
		}
	}

	if m.mouse {
		add(styleStatusMouse.Render("mouse: on"), "mouse: on")
	}

	if chip := attachmentChip(m.attachments); chip != "" {
		add(styleStatusAttach.Render(chip), chip)
	}

	if m.git.isRepo {
		raw := "⎇ " + m.git.branchLine()
		add(styleStatusGit.Render(raw), raw)
	}

	if cwd := shortCwdOf(m.activeCwd()); cwd != "" {
		reserved := len([]rune(strings.Join(plain, " · ")))
		if m.busy {
			reserved += len([]rune(workingLabel + " · "))
		}
		if m.width > 0 && reserved+len([]rune(" · "+cwd)) > m.width {
			if base := filepath.Base(cwd); base != cwd {
				cwd = base
			}
		}
		add(styleStatusCwd.Render(cwd), cwd)
	}

	line := strings.Join(spans, sep)
	if m.busy {
		// The input stays live for steering, so the working state — and the esc
		// hint to cancel it — is surfaced here rather than by replacing the input box.
		line = styleWorking.Render(workingLabel) + sep + line
	}
	return line
}

// shortModelName compacts a provider name for the status line, e.g.
// "anthropic/claude-opus-4-8" → "opus4-8". It strips the "anthropic/" and
// "claude-" prefixes and joins the family to its version (dropping the first
// hyphen). Names without the anthropic prefix are returned unchanged.
func shortModelName(name string) string {
	if !strings.HasPrefix(name, "anthropic/") {
		return name
	}
	s := strings.TrimPrefix(name, "anthropic/")
	s = strings.TrimPrefix(s, "claude-")
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i] + s[i+1:]
	}
	return s
}

// thinkStatusText renders the extended-thinking level compactly for the status
// line: "off", "max", or a run of "+" that grows with effort (low ++ … xhigh +++++).
func thinkStatusText(level llm.ThinkLevel) string {
	switch level {
	case llm.ThinkNone:
		return "off"
	case llm.ThinkLow:
		return "++"
	case llm.ThinkMedium:
		return "+++"
	case llm.ThinkHigh:
		return "++++"
	case llm.ThinkXHigh:
		return "+++++"
	case llm.ThinkMax:
		return "max"
	default:
		return string(level)
	}
}

// shortCwd returns the process working directory with the home prefix collapsed
// to "~", or "" if it can't be determined.
func shortCwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return shortCwdOf(dir)
}

// shortCwdOf collapses dir's home prefix to "~", returning "" for an empty dir.
func shortCwdOf(dir string) string {
	if dir == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if dir == home {
			return "~"
		}
		if strings.HasPrefix(dir, home+string(os.PathSeparator)) {
			return "~" + dir[len(home):]
		}
	}
	return dir
}

// formatTokens renders a token count compactly (e.g. 1000000 → "1M", 200000 → "200K").
func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return trimZero(float64(n)/1_000_000) + "M"
	case n >= 1_000:
		return trimZero(float64(n)/1_000) + "K"
	default:
		return fmt.Sprintf("%d", n)
	}
}

// trimZero formats v with one decimal, dropping a trailing ".0".
func trimZero(v float64) string {
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0")
}

// footer renders the input box, kept live even while the agent is working so the
// user can steer the running turn.
func (m model) footer() string {
	return m.inputView()
}

func (m model) liveRegion() string {
	var parts []string
	if think := strings.TrimSpace(m.streamThink); think != "" {
		parts = append(parts, m.renderThinkBox(think))
	}
	if text := m.streamText; text != "" {
		parts = append(parts, m.renderMarkdown(text))
	}
	return strings.Join(parts, "\n")
}
