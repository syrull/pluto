package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"golang.org/x/term"

	"github.com/pluto/harness/internal/agent"
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
	if s := os.Getenv("HARNESS_MD_STYLE"); s != "" {
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
			return styleErr.Render(fmt.Sprintf("✗ %s: %s", ev.Tool, ev.Text))
		}
		return styleErr.Render("✗ " + ev.Text)
	default:
		return ev.Text
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
	default:
		return wrapBody("", ln, styleDiffCtx, width)
	}
}

func (m *model) flushStream() {
	if think := strings.TrimSpace(m.streamThink); think != "" {
		m.lines = append(m.lines, m.renderThinkBox(think))
		m.streamThink = ""
	}
	if text := strings.TrimSpace(m.streamText); text != "" {
		m.lines = append(m.lines, m.renderMarkdown(text))
		m.streamText = ""
	}
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
func (m model) View() string {
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
	status := "model: " + name
	if m.agent != nil {
		if th, ok := m.agent.Thinker(); ok {
			level := th.ThinkLevel()
			if level.Thinking() {
				status += " · thinking: " + string(level)
			} else {
				status += " · thinking: off"
			}
		}
	}
	return styleModelStatus.Render(status)
}

func (m model) footer() string {
	if m.busy {
		return styleHint.Render("working…") + strings.Repeat("\n", inputHeight-1)
	}
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
