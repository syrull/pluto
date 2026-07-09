package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/pluto/harness/internal/agent"
	"github.com/pluto/harness/internal/llm"
	"github.com/pluto/harness/internal/tui/widgets"
)

func listen(ch chan eventMsg) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return doneMsg{}
		}
		return ev
	}
}

func (m *model) runAgent(input string) tea.Cmd {
	ch := make(chan eventMsg, 16)
	m.events = ch
	go func() {
		defer close(ch)
		_, _ = m.agent.Run(context.Background(), input, func(ev agent.Event) {
			ch <- eventMsg(ev)
		})
	}()
	return listen(ch)
}

func (m *model) handleCommand(line string) (string, tea.Cmd) {
	fields := strings.Fields(line)
	switch fields[0] {
	case "/new":
		m.agent.Reset()
		m.lines = nil
		m.streamText = ""
		m.streamThink = ""
		return styleHint.Render("✓ started a new conversation"), nil

	case "/login":
		if m.login == nil {
			return styleErr.Render("✗ login is not available in this build"), nil
		}
		cmd := m.login.Command()
		return styleHint.Render("launching Anthropic login (claude setup-token)…"),
			tea.ExecProcess(cmd, func(err error) tea.Msg { return loginDoneMsg{err: err} })

	case "/model":
		sw, ok := m.agent.Switcher()
		if !ok {
			return styleErr.Render("✗ current provider does not support model switching"), nil
		}
		if len(fields) == 1 {
			models := sw.Available()
			if len(models) == 0 {
				return styleErr.Render("✗ no models available to switch to"), nil
			}
			m.picker = newModelPicker(models, sw.Model())
			return "", nil
		}
		target := fields[1]
		if !slices.Contains(sw.Available(), target) {
			return styleErr.Render(fmt.Sprintf("✗ unknown model %q — run /model to list", target)), nil
		}
		sw.SetModel(target)
		return styleHint.Render("switched to " + m.agent.ProviderName()), nil

	case "/think":
		th, ok := m.agent.Thinker()
		if !ok {
			return styleErr.Render("✗ current provider does not support thinking"), nil
		}
		levels := th.ThinkLevels()
		if len(fields) == 1 {
			th.SetThinkLevel(nextThinkLevel(levels, th.ThinkLevel()))
			return renderThinkStatus(th.ThinkLevel()), nil
		}
		arg := fields[1]
		var target llm.ThinkLevel
		switch arg {
		case "off":
			target = llm.ThinkNone
		case "on":
			target = levels[len(levels)-1]
		default:
			lvl := llm.ThinkLevel(arg)
			if !slices.Contains(levels, lvl) {
				return styleErr.Render(fmt.Sprintf("✗ usage: /think [%s], got %q", thinkLevelList(levels), arg)), nil
			}
			target = lvl
		}
		th.SetThinkLevel(target)
		return renderThinkStatus(th.ThinkLevel()), nil

	default:
		return styleErr.Render("✗ unknown command: " + fields[0]), nil
	}
}

func nextThinkLevel(levels []llm.ThinkLevel, cur llm.ThinkLevel) llm.ThinkLevel {
	i := slices.Index(levels, cur)
	if i < 0 {
		return levels[0]
	}
	return levels[(i+1)%len(levels)]
}

func thinkLevelList(levels []llm.ThinkLevel) string {
	parts := make([]string, len(levels))
	for i, l := range levels {
		parts[i] = string(l)
	}
	return strings.Join(parts, "|")
}

func renderThinkStatus(level llm.ThinkLevel) string {
	if !level.Thinking() {
		return styleHint.Render("✓ extended thinking disabled")
	}
	return styleHint.Render("✓ extended thinking: " + string(level))
}

// Update handles Bubbletea messages and returns the updated model and commands.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.md = newRenderer(msg.Width)
		h := msg.Height - footerHeight
		if h < 1 {
			h = 1
		}
		if !m.ready {
			m.vp = viewport.New(msg.Width, h)
			m.vp.KeyMap = scrollKeymap()
			m.input = newInput(msg.Width)
			m.ready = true
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = h
			m.input.SetWidth(msg.Width)
		}
		m.syncViewport()
		return m, nil

	case tea.KeyMsg:
		if m.picker != nil {
			switch msg.Type {
			case tea.KeyCtrlC:
				return m, tea.Quit
			case tea.KeyUp:
				m.picker.Up()
			case tea.KeyDown:
				m.picker.Down()
			case tea.KeyEnter:
				target := m.picker.Selected()
				m.picker = nil
				if sw, ok := m.agent.Switcher(); ok {
					sw.SetModel(target)
					m.lines = append(m.lines, styleHint.Render("switched to "+m.agent.ProviderName()))
					m.syncViewport()
				}
			case tea.KeyEsc:
				m.picker = nil
			}
			return m, nil
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyPgUp, tea.KeyPgDown, tea.KeyCtrlU, tea.KeyCtrlD, tea.KeyUp, tea.KeyDown:
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		case tea.KeyEnter:
			if msg.Alt {
				m.input.InsertRune('\n')
				return m, nil
			}
			if m.busy || strings.TrimSpace(m.input.Value()) == "" {
				return m, nil
			}
			in := strings.TrimSpace(m.input.Value())
			m.lines = append(m.lines, m.renderUserLine(in))
			m.input.Reset()
			if strings.HasPrefix(in, "/") {
				status, cmd := m.handleCommand(in)
				if status != "" {
					m.lines = append(m.lines, status)
				}
				if cmd != nil {
					m.busy = true
				}
				m.syncViewport()
				return m, cmd
			}
			m.busy = true
			cmd := m.runAgent(in)
			m.syncViewport()
			return m, cmd
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

	case eventMsg:
		ev := agent.Event(msg)
		switch ev.Kind {
		case "text_delta":
			m.streamText += ev.Text
		case "thinking_delta":
			m.streamThink += ev.Text
		default:
			if ev.Kind == "tool_call" {
				m.flushStream()
			}
			m.lines = append(m.lines, renderEvent(ev))
		}
		m.syncViewport()
		return m, listen(m.events)

	case doneMsg:
		m.flushStream()
		m.busy = false
		m.events = nil
		m.syncViewport()
		return m, nil
	case loginDoneMsg:
		m.busy = false
		if m.login == nil {
			return m, nil
		}
		status, err := m.login.After(msg.err)
		if err != nil {
			m.lines = append(m.lines, styleErr.Render("✗ login failed: "+err.Error()))
		} else {
			m.lines = append(m.lines, styleHint.Render("✓ "+status))
		}
		m.syncViewport()
		return m, nil
	}
	return m, nil
}

func newModelPicker(models []string, active string) *widgets.ListPicker {
	return widgets.NewListPicker(
		"select model — ↑/↓ move · enter switch · esc cancel",
		models,
		active,
		widgets.ListStyle{Title: styleHint, Selected: stylePickSel, Item: styleModel},
	)
}
