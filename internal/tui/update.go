package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tui/widgets"
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
		m.outputs = nil
		m.codeBlocks = nil
		m.notice = ""
		m.pendingTool = ""
		m.pendingArgs = ""
		m.streamText = ""
		m.streamThink = ""
		return styleHint.Render("✓ started a new conversation"), nil

	case "/login":
		if m.login == nil {
			return styleErr.Render("✗ login is not available in this build"), nil
		}
		// Manual paste fallback: `/login <redirect-url-or-code>` completes a
		// pending flow when the browser is on another machine.
		if len(fields) > 1 {
			if m.loginFlow == nil {
				return styleErr.Render("✗ no login in progress — run /login first"), nil
			}
			flow := m.loginFlow
			pasted := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
			return styleHint.Render("completing login…"), func() tea.Msg {
				status, err := m.login.Complete(flow, pasted)
				return loginDoneMsg{status: status, err: err}
			}
		}
		url, flow, err := m.login.Authorize()
		if err != nil {
			return styleErr.Render("✗ login failed: " + err.Error()), nil
		}
		m.loginFlow = flow
		openBrowser(url)
		hint := "opening browser to authorize with Anthropic…\n" +
			styleToolArgs.Render(url) + "\n" +
			"If the browser is on another machine, complete login there and run:\n" +
			styleToolArgs.Render("/login <paste the redirect URL or code>")
		return styleHint.Render(hint), func() tea.Msg {
			status, err := m.login.Wait(flow)
			return loginDoneMsg{status: status, err: err}
		}

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
			m.pickerKind = pickerModel
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
			m.picker = newThinkPicker(levels, th.ThinkLevel())
			m.pickerKind = pickerThink
			return "", nil
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

	case "/auto":
		ctrl, ok := m.agent.Auto()
		if !ok {
			return styleErr.Render("✗ auto mode is not available in this build"), nil
		}
		if len(fields) == 1 {
			return renderAutoStatus(ctrl), nil
		}
		switch fields[1] {
		case "on":
			ctrl.SetAutoEnabled(true)
			return renderAutoStatus(ctrl), nil
		case "off":
			ctrl.SetAutoEnabled(false)
			return styleHint.Render("✓ auto mode off — bash commands run without review"), nil
		default:
			return styleErr.Render("✗ usage: /auto [on|off]"), nil
		}

	default:
		return styleErr.Render("✗ unknown command: " + fields[0]), nil
	}
}

func renderAutoStatus(c agent.AutoController) string {
	if !c.AutoEnabled() {
		return styleHint.Render("auto mode: off")
	}
	return styleReview.Render(fmt.Sprintf("auto mode: on · judge %s", c.JudgeName()))
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
		m.height = msg.Height
		m.md = newRenderer(msg.Width)
		h := msg.Height - footerHeight
		if h < 1 {
			h = 1
		}
		if !m.ready {
			m.vp = viewport.New(viewport.WithWidth(msg.Width), viewport.WithHeight(h))
			m.vp.KeyMap = scrollKeymap()
			m.input = newInput(msg.Width)
			m.ready = true
		} else {
			m.vp.SetWidth(msg.Width)
			m.vp.SetHeight(h)
			m.input.SetWidth(msg.Width)
		}
		m.syncViewport()
		m.resizeModal()
		return m, nil

	case tea.KeyPressMsg:
		ks := msg.String()
		m.notice = ""
		if m.modal != nil {
			switch ks {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.modal = nil
			case "c", "y":
				m.modal.MarkCopied()
				return m, tea.SetClipboard(m.modal.Content())
			default:
				return m, m.modal.Update(msg)
			}
			return m, nil
		}
		if m.picker != nil {
			switch ks {
			case "ctrl+c":
				return m, tea.Quit
			case "up":
				m.picker.Up()
			case "down":
				m.picker.Down()
			case "enter":
				target := m.picker.Selected()
				kind := m.pickerKind
				m.picker = nil
				m.pickerKind = pickerNone
				if status := m.applyPick(kind, target); status != "" {
					m.pushText(status)
					m.syncViewport()
				}
			case "esc":
				m.picker = nil
				m.pickerKind = pickerNone
			}
			return m, nil
		}
		switch ks {
		case "ctrl+c":
			return m, tea.Quit
		case "pgup", "pgdown", "ctrl+u", "ctrl+d", "up", "down":
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		case "ctrl+o":
			if o, ok := m.lastOutput(); ok {
				m.openModal(o)
			}
			return m, nil
		case "ctrl+y":
			if b, ok := m.lastCode(); ok {
				m.notice = styleHint.Render("✓ copied " + b.title() + " to clipboard")
				return m, tea.SetClipboard(b.code)
			}
			return m, nil
		case "ctrl+t":
			m.mouse = !m.mouse
			if m.mouse {
				m.notice = styleHint.Render("✓ wheel scroll and click; ctrl+t to select text")
			} else {
				m.notice = styleHint.Render("✓ drag to select text; ctrl+t to re-enable")
			}
			return m, nil
		case "alt+enter":
			m.input.InsertRune('\n')
			return m, nil
		case "enter":
			in := strings.TrimSpace(m.input.Value())
			if in == "" {
				return m, nil
			}
			// The input stays live during generation: a plain message is queued
			// to steer the running turn; slash commands wait until it's idle.
			if m.busy {
				if strings.HasPrefix(in, "/") {
					m.pushText(styleErr.Render("✗ commands are unavailable while the agent is working"))
					m.syncViewport()
					return m, nil
				}
				m.pushText(m.renderUserLine(in))
				m.input.Reset()
				m.agent.Steer(in)
				m.syncViewport()
				return m, nil
			}
			m.pushText(m.renderUserLine(in))
			m.input.Reset()
			if strings.HasPrefix(in, "/") {
				status, cmd := m.handleCommand(in)
				if status != "" {
					m.pushText(status)
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

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case eventMsg:
		ev := agent.Event(msg)
		switch ev.Kind {
		case "text_delta":
			m.streamText += ev.Text
		case "thinking_delta":
			m.streamThink += ev.Text
		case "tool_review":
			m.flushStream()
			m.pushText(renderToolReview(m.width, ev.Text))
		case "tool_call":
			m.flushStream()
			m.pendingTool = ev.Tool
			m.pendingArgs = ev.Text
			m.pushText(renderToolCall(m.width, ev.Tool, ev.Text))
		case "tool_result":
			m.appendToolResult(ev)
		default:
			m.pushText(renderEvent(m.width, ev))
		}
		m.syncViewport()
		return m, listen(m.events)

	case doneMsg:
		m.flushStream()
		m.busy = false
		m.events = nil
		// A message steered in as the run was ending wasn't folded into it;
		// continue the conversation with it as the next turn.
		if pending := m.agent.TakeSteering(); len(pending) > 0 {
			m.busy = true
			cmd := m.runAgent(strings.Join(pending, "\n\n"))
			m.syncViewport()
			return m, cmd
		}
		m.syncViewport()
		return m, nil
	case loginDoneMsg:
		m.busy = false
		m.loginFlow = nil
		if msg.err != nil {
			m.pushText(styleErr.Render("✗ login failed: " + msg.err.Error()))
		} else {
			m.pushText(styleHint.Render("✓ " + msg.status))
		}
		m.syncViewport()
		return m, nil
	}
	// Bracketed paste (tea.PasteMsg) and the textarea's async ctrl+v clipboard
	// read arrive as unhandled messages; route them into the input buffer.
	return m.forwardToInput(msg)
}

// forwardToInput routes a message into the input textarea unless a modal or
// picker is capturing keys.
func (m model) forwardToInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.modal != nil || m.picker != nil {
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleMouse scrolls with the wheel and, on a left click over a truncated tool
// result, opens its full output in a modal.
func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.modal != nil {
		return m, m.modal.Update(msg)
	}
	if m.picker != nil {
		return m, nil
	}
	switch e := msg.(type) {
	case tea.MouseWheelMsg:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case tea.MouseClickMsg:
		if e.Button == tea.MouseLeft {
			m.notice = ""
			if o, ok := m.outputAtScreen(e.Y); ok {
				m.openModal(o)
			} else if b, ok := m.codeAtScreen(e.Y); ok {
				m.notice = styleHint.Render("✓ copied " + b.title() + " to clipboard")
				return m, tea.SetClipboard(b.code)
			}
		}
	}
	return m, nil
}

// applyPick applies a picker selection and returns a status line to append.
func (m *model) applyPick(kind pickerKind, target string) string {
	switch kind {
	case pickerModel:
		if sw, ok := m.agent.Switcher(); ok {
			sw.SetModel(target)
			return styleHint.Render("switched to " + m.agent.ProviderName())
		}
	case pickerThink:
		if th, ok := m.agent.Thinker(); ok {
			th.SetThinkLevel(llm.ThinkLevel(target))
			return renderThinkStatus(th.ThinkLevel())
		}
	}
	return ""
}

func newModelPicker(models []string, active string) *widgets.ListPicker {
	return widgets.NewListPicker(
		"select model — ↑/↓ move · enter switch · esc cancel",
		models,
		active,
		widgets.ListStyle{Title: styleHint, Selected: stylePickSel, Item: styleModel},
	)
}

func newThinkPicker(levels []llm.ThinkLevel, active llm.ThinkLevel) *widgets.ListPicker {
	items := make([]string, len(levels))
	for i, l := range levels {
		items[i] = string(l)
	}
	return widgets.NewListPicker(
		"extended thinking — ↑/↓ move · enter set · esc cancel",
		items,
		string(active),
		widgets.ListStyle{Title: styleHint, Selected: stylePickSel, Item: styleModel},
	)
}
