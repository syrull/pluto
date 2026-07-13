package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"golang.org/x/term"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/diff"
	"github.com/syrull/pluto/internal/tui/widgets"
)

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
// continuation lines under the prompt so multi-line input doesn't run off screen.
func (m *model) renderUserLine(in string) string {
	w := m.width
	if w <= 0 {
		w = 80
	}
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

// pushMarkdown commits assistant markdown and retains each fenced code block it
// contains behind a copy affordance.
func (m *model) pushMarkdown(text string) {
	m.pushText(m.renderMarkdown(text))
	for _, b := range extractCodeBlocks(text) {
		m.codeBlocks = append(m.codeBlocks, b)
		m.lines = append(m.lines, entry{text: m.copyAffordance(b), copyID: len(m.codeBlocks)})
	}
}

// copyAffordance renders the marker for a retained code block: a clickable
// button when the mouse is captured, otherwise the keyboard hint that copies it.
func (m *model) copyAffordance(b codeBlock) string {
	if m.mouse {
		return "  " + styleCopyBtn.Render(" Copy "+b.title()+" ▸ ")
	}
	return "  " + styleHint.Render("[ctrl+y] copy "+b.title())
}

func (m *model) thinkBoxWidth() int {
	w := m.width
	if w <= 0 {
		w = 80
	}
	w -= 4
	if w < 10 {
		w = 10
	}
	return w
}

func (m *model) renderThinkBox(think string) string {
	hdr := styleThinkHdr.Render("✻ Thinking…")
	body := styleThink.Render(think)
	return styleThinkBox.Width(m.thinkBoxWidth()).Render(hdr + "\n" + body)
}

// View renders the transcript layout with the status line and input footer.
func (m model) View() tea.View {
	v := tea.NewView(m.content())
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

// content renders the screen body: the modal when open, otherwise the
// transcript viewport above the status line and input footer.
func (m model) content() string {
	if m.modal != nil && m.ready {
		return m.modal.View()
	}
	footer := m.modelStatus() + "\n" + m.footer()
	if m.picker != nil {
		footer = m.modelStatus() + "\n" + m.picker.View()
	}
	if !m.ready {
		return m.transcript() + "\n\n" + footer
	}
	return m.vp.View() + "\n" + footer
}

func (m model) modelStatus() string {
	name := "no provider"
	if m.agent != nil {
		name = m.agent.ProviderName()
	}
	status := name
	if m.agent != nil {
		if th, ok := m.agent.Thinker(); ok {
			level := th.ThinkLevel()
			if level.Thinking() {
				status += " · thinking: " + string(level)
			} else {
				status += " · thinking: off"
			}
		}
		if used, window, ok := m.agent.ContextUsage(); ok && window > 0 {
			pct := used * 100 / window
			status += fmt.Sprintf(" · context: %d%% / %s", pct, formatTokens(window))
		}
	}
	if m.mouse {
		status += " · mouse: on"
	} else {
		status += " · mouse: off"
	}
	if cwd := shortCwd(); cwd != "" {
		reserved := len([]rune(status))
		if m.busy {
			reserved += len([]rune("● working… · "))
		}
		if m.width > 0 && reserved+len([]rune(" · "+cwd)) > m.width {
			if base := filepath.Base(cwd); base != cwd {
				cwd = base
			}
		}
		status += " · " + cwd
	}
	line := styleModelStatus.Render(status)
	if m.busy {
		// The input stays live for steering, so the working state is surfaced
		// here rather than by replacing the input box.
		line = styleWorking.Render("● working…") + styleModelStatus.Render(" · "+status)
	}
	if m.notice != "" {
		line += "  " + m.notice
	}
	return line
}

// shortCwd returns the working directory with the home prefix collapsed to "~",
// or "" if it can't be determined.
func shortCwd() string {
	dir, err := os.Getwd()
	if err != nil || dir == "" {
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
	return m.input.View()
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
