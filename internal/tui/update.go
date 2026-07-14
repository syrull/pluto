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
	"github.com/syrull/pluto/internal/session"
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
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go func() {
		defer close(ch)
		_, _ = m.agent.Run(ctx, input, func(ev agent.Event) {
			ch <- eventMsg(ev)
		})
	}()
	return listen(ch)
}

// interrupt aborts the in-flight Run: it cancels the request context (stopping
// the LLM stream and any running tool) and drops any queued steering so the
// canceled turn doesn't immediately restart. The pending listener delivers
// doneMsg once the run unwinds, which clears busy.
func (m *model) interrupt() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.agent.TakeSteering()
	m.notice = "✗ canceled request"
}

func (m *model) handleCommand(line string) (string, tea.Cmd) {
	fields := strings.Fields(line)
	switch fields[0] {
	case "/new":
		m.agent.Reset()
		m.lines = nil
		m.outputs = nil
		m.codeBlocks = nil
		m.pendingTool = ""
		m.pendingArgs = ""
		m.streamText = ""
		m.streamThink = ""
		m.sessionName = ""
		m.notice = "✓ started a new conversation"
		return "", nil

	case "/dash":
		m.showHome = true
		// Restart the animation loop under a fresh epoch so any stale tick stops.
		m.orbitEpoch++
		return "", orbitTick(m.orbitEpoch)

	case "/save":
		name := ""
		if len(fields) > 1 {
			name = strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		}
		return m.save(name), nil

	case "/resume":
		store, err := m.sessionStore()
		if err != nil {
			return styleErr.Render("✗ sessions unavailable: " + err.Error()), nil
		}
		if len(fields) == 1 {
			metas, err := store.List()
			if err != nil {
				return styleErr.Render("✗ " + err.Error()), nil
			}
			if len(metas) == 0 {
				return styleHint.Render("no saved conversations yet — use /save first"), nil
			}
			m.picker = newResumePicker(metas)
			m.picker.SetSize(m.width, m.height)
			m.pickerKind = pickerResume
			return "", nil
		}
		m.resume(fields[1])
		return "", nil

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
			m.busy = true
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
		m.busy = true
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
			m.picker.SetSize(m.width, m.height)
			m.pickerKind = pickerModel
			return "", nil
		}
		target := fields[1]
		if !slices.Contains(sw.Available(), target) {
			return styleErr.Render(fmt.Sprintf("✗ unknown model %q — run /model to list", target)), nil
		}
		sw.SetModel(target)
		m.notice = "✓ switched to " + m.agent.ProviderName()
		return "", nil

	case "/think":
		th, ok := m.agent.Thinker()
		if !ok {
			return styleErr.Render("✗ current provider does not support thinking"), nil
		}
		levels := th.ThinkLevels()
		if len(fields) == 1 {
			m.picker = newThinkPicker(levels, th.ThinkLevel())
			m.picker.SetSize(m.width, m.height)
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
		m.notice = thinkNotice(th.ThinkLevel())
		return "", nil

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
			m.notice = "✓ auto mode on · judge " + ctrl.JudgeName()
			return "", nil
		case "off":
			ctrl.SetAutoEnabled(false)
			m.notice = "✓ auto mode off — bash commands run without review"
			return "", nil
		default:
			return styleErr.Render("✗ usage: /auto [on|off]"), nil
		}

	case "/gh":
		if !ghAvailable() {
			return styleErr.Render("✗ gh unavailable — install the GitHub CLI and use a github.com remote"), nil
		}
		m.ghm = newGHModal()
		m.ghm.SetSize(m.width, m.height)
		return "", fetchGitHubCmd

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

// thinkNotice returns the transient notice text for a think level.
func thinkNotice(level llm.ThinkLevel) string {
	if !level.Thinking() {
		return "✓ extended thinking disabled"
	}
	return "✓ extended thinking: " + string(level)
}

// Update handles Bubbletea messages and returns the updated model and commands.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cw := m.contentWidth()
		ch := m.convBodyHeight()
		m.md = newRenderer(cw)
		inW := msg.Width - 2 // footer box interior (border)
		if inW < 10 {
			inW = 10
		}
		if !m.ready {
			m.vp = viewport.New(viewport.WithWidth(cw), viewport.WithHeight(ch))
			m.vp.KeyMap = scrollKeymap()
			m.input = newInput(inW)
			m.ready = true
		} else {
			m.vp.SetWidth(cw)
			m.vp.SetHeight(ch)
			m.input.SetWidth(inW)
		}
		if m.tree == nil {
			m.tree = newFileTree()
			if m.tree != nil && m.gitReady {
				m.tree.SetStatus(m.buildStatusStyles())
				m.changes = m.buildChangesList()
			}
		}
		if m.picker != nil {
			m.picker.SetSize(msg.Width, msg.Height)
		}
		if m.finder != nil {
			m.finder.SetSize(msg.Width, msg.Height)
		}
		if m.ghm != nil {
			m.ghm.SetSize(msg.Width, msg.Height)
		}
		m.syncViewport()
		m.resizeModal()
		return m, nil

	case tea.KeyPressMsg:
		ks := msg.String()
		m.notice = ""
		if m.ghm != nil {
			if ks == "ctrl+c" {
				return m, tea.Quit
			}
			handled, out := m.ghm.handleKey(ks)
			if !handled {
				return m, m.ghm.Update(msg)
			}
			return m, m.applyGHOutcome(out)
		}
		if m.modal != nil {
			switch ks {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.modal = nil
			case "c", "y":
				m.modal.MarkCopied()
				return m, tea.SetClipboard(m.modal.Content())
			case "ctrl+g":
				if cmd := openInEditor(m.modalPath); cmd != nil {
					return m, cmd
				}
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
				m.applyPick(kind, target)
			case "esc":
				m.picker = nil
				m.pickerKind = pickerNone
			}
			return m, nil
		}
		if m.finder != nil {
			switch ks {
			case "ctrl+c":
				return m, tea.Quit
			case "up":
				m.finder.Up()
			case "down":
				m.finder.Down()
			case "enter":
				sel, ok := m.finder.Selected()
				m.finder = nil
				if ok {
					m.openFinderFile(sel)
				}
			case "esc":
				m.finder = nil
			case "backspace":
				m.finder.Backspace()
			default:
				if msg.Text != "" {
					m.finder.Insert(msg.Text)
				}
			}
			return m, nil
		}
		// The slash-command popup claims navigation/completion keys before the
		// pane and input handlers so Tab completes instead of cycling focus and
		// Enter completes instead of submitting; typing falls through to the input.
		if m.cmdMenu != nil {
			switch ks {
			case "up", "shift+tab":
				m.cmdMenu.Up()
				return m, nil
			case "down":
				m.cmdMenu.Down()
				return m, nil
			case "tab":
				if m.cmdMenu.Len() == 1 {
					m.completeCommand()
				} else {
					m.cmdMenu.Cycle()
				}
				return m, nil
			case "enter":
				m.completeCommand()
				return m, nil
			case "esc":
				m.cmdMenu = nil
				return m, nil
			}
		}
		// Tab cycles pane focus; while a sidebar pane holds focus, arrows/enter
		// drive it. Unclaimed keys (typing, ctrl+*) fall through to chat handling.
		if done, cmd := m.paneKey(ks); done {
			return m, cmd
		}
		// Esc aborts a running inline `!` command, else cancels the in-flight
		// request; otherwise it falls through to the input's normal editing.
		if ks == "esc" && m.inlineCancel != nil {
			m.inlineCancel()
			m.inlineCancel = nil
			m.inlineEpoch++ // drop the canceled run's late-arriving result
			m.notice = "✗ canceled command"
			return m, nil
		}
		if ks == "esc" && m.busy {
			m.interrupt()
			m.syncViewport()
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
				m.notice = "✓ copied " + b.title() + " to clipboard"
				return m, tea.SetClipboard(b.code)
			}
			return m, nil
		case "ctrl+t":
			m.mouse = !m.mouse
			if m.mouse {
				m.notice = "✓ wheel scroll and click; ctrl+t to select text"
			} else {
				m.notice = "✓ drag to select text; ctrl+t to re-enable"
			}
			return m, nil
		case "alt+enter":
			m.showHome = false
			m.input.InsertRune('\n')
			m.refreshCommandMenu()
			return m, nil
		case "enter":
			in := strings.TrimSpace(m.input.Value())
			if in == "" {
				return m, nil
			}
			m.showHome = false
			// Inline shell: `!cmd` runs immediately, independent of the agent,
			// whether or not a turn is in flight.
			if strings.HasPrefix(in, "!") {
				cmd := m.handleInline(in)
				m.syncViewport()
				return m, cmd
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
				m.syncViewport()
				return m, cmd
			}
			m.busy = true
			cmd := m.runAgent(in)
			m.syncViewport()
			return m, cmd
		default:
			m.showHome = false
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			m.refreshCommandMenu()
			return m, cmd
		}

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case eventMsg:
		ev := agent.Event(msg)
		var extra tea.Cmd
		switch ev.Kind {
		case "text_delta":
			m.streamText += ev.Text
		case "thinking_delta":
			m.streamThink += ev.Text
		case "tool_review":
			m.flushStream()
			m.pushText(renderToolReview(m.contentWidth(), ev.Text))
		case "tool_call":
			m.flushStream()
			m.pendingTool = ev.Tool
			m.pendingArgs = ev.Text
			m.pushText(renderToolCall(m.contentWidth(), ev.Tool, ev.Text))
		case "tool_result":
			// Refresh the sidebar so file mutations show up live in the tree/changes.
			if ev.Tool == "write" || ev.Tool == "edit" {
				extra = gatherGitCmd
			}
			m.appendToolResult(ev)
		default:
			m.pushText(renderEvent(m.contentWidth(), ev))
		}
		m.syncViewport()
		if extra != nil {
			return m, tea.Batch(listen(m.events), extra)
		}
		return m, listen(m.events)

	case bashInlineMsg:
		// Drop a result from a canceled or superseded inline run.
		if msg.epoch != m.inlineEpoch {
			return m, nil
		}
		m.applyInlineResult(msg)
		m.syncViewport()
		return m, nil

	case doneMsg:
		m.flushStream()
		m.busy = false
		m.events = nil
		m.cancel = nil
		m.autosave()
		// A message steered in as the run was ending wasn't folded into it;
		// continue the conversation with it as the next turn.
		if pending := m.agent.TakeSteering(); len(pending) > 0 {
			m.busy = true
			cmd := m.runAgent(strings.Join(pending, "\n\n"))
			m.syncViewport()
			return m, tea.Batch(cmd, gatherGitCmd)
		}
		m.syncViewport()
		// Refresh the sidebar to reflect any files the turn changed.
		return m, gatherGitCmd
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
	case ghDataMsg:
		if m.ghm != nil {
			if msg.err != nil {
				m.ghm.SetError(msg.err)
			} else {
				m.ghm.SetData(msg.issues, msg.prs)
			}
		}
		return m, nil
	case ghChecksMsg:
		if m.ghm != nil {
			m.ghm.SetChecks(msg.pr, msg.checks, msg.err)
		}
		return m, nil
	case ghCloseMsg:
		if msg.err != nil {
			m.notice = fmt.Sprintf("✗ closing issue #%d failed: %s", msg.number, msg.err.Error())
			return m, nil
		}
		m.notice = fmt.Sprintf("✓ closed issue #%d", msg.number)
		// The closed issue drops out of the open list; return to it and refresh.
		if m.ghm != nil {
			m.ghm.BackToList()
			return m, fetchGitHubCmd
		}
		return m, nil
	case ghMergeMsg:
		if msg.err != nil {
			m.notice = fmt.Sprintf("✗ merging PR #%d failed: %s", msg.number, msg.err.Error())
			return m, nil
		}
		m.notice = fmt.Sprintf("✓ merged PR #%d", msg.number)
		// The merged PR drops out of the open list; return to it and refresh.
		if m.ghm != nil {
			m.ghm.BackToList()
			return m, fetchGitHubCmd
		}
		return m, nil
	case orbitTickMsg:
		// Advance the planet only while home, and only for the live tick loop.
		if !m.showHome || msg.epoch != m.orbitEpoch {
			return m, nil
		}
		m.orbitFrame = (m.orbitFrame + 1) % widgets.OrbitSteps
		return m, orbitTick(m.orbitEpoch)
	case gitInfoMsg:
		m.git = gitInfo(msg)
		m.gitReady = true
		if m.tree != nil {
			m.tree.SetStatus(m.buildStatusStyles())
		}
		m.changes = m.buildChangesList()
		if m.changes == nil && m.focus == paneChanges {
			m.focus = paneTree
		}
		return m, nil
	case editorDoneMsg:
		// The editor may have changed the file; refresh a still-open file/diff
		// modal so it reflects the edit, and refresh the sidebar.
		if msg.err == nil && m.modal != nil && m.modalIsFile && m.modalPath != "" {
			m.openFileDiff(m.modalPath)
		}
		if msg.err == nil {
			return m, gatherGitCmd
		}
		return m, nil
	}
	// Bracketed paste (tea.PasteMsg) and the textarea's async ctrl+v clipboard
	// read arrive as unhandled messages; route them into the input buffer.
	return m.forwardToInput(msg)
}

// forwardToInput routes a message into the input textarea unless a modal or
// picker is capturing keys.
func (m model) forwardToInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.modal != nil || m.picker != nil || m.finder != nil {
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleMouse scrolls with the wheel and, on a left click over a truncated tool
// result, opens its full output in a modal.
func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.ghm != nil {
		return m, m.ghm.Update(msg)
	}
	if m.modal != nil {
		return m, m.modal.Update(msg)
	}
	if m.picker != nil || m.finder != nil {
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
				m.notice = "✓ copied " + b.title() + " to clipboard"
				return m, tea.SetClipboard(b.code)
			}
		}
	}
	return m, nil
}

// applyGHOutcome acts on a key press in the GitHub browser: closing it, opening a
// URL, or seeding a develop/review conversation.
func (m *model) applyGHOutcome(out ghOutcome) tea.Cmd {
	switch out.kind {
	case ghOutcomeClose:
		m.ghm = nil
	case ghOutcomeOpenURL:
		openBrowser(out.url)
		m.notice = "✓ opened " + out.url
	case ghOutcomeFetchChecks:
		return fetchChecksCmd(out.pr.Number)
	case ghOutcomeCloseIssue:
		m.notice = fmt.Sprintf("closing issue #%d…", out.issue.Number)
		return closeIssueCmd(out.issue.Number)
	case ghOutcomeMergePR:
		if out.pr.Draft {
			m.notice = fmt.Sprintf("✗ PR #%d is a draft — mark it ready before merging", out.pr.Number)
			return nil
		}
		m.notice = fmt.Sprintf("merging PR #%d…", out.pr.Number)
		return mergePRCmd(out.pr.Number)
	case ghOutcomeDevelop:
		if m.busy {
			m.notice = "✗ agent is working — try again once it's idle"
			return nil
		}
		m.ghm = nil
		return m.startGHConversation(developPrompt(out.issue),
			fmt.Sprintf("develop issue #%d: %s", out.issue.Number, out.issue.Title))
	case ghOutcomeReview:
		if m.busy {
			m.notice = "✗ agent is working — try again once it's idle"
			return nil
		}
		m.ghm = nil
		return m.startGHConversation(reviewPrompt(out.pr),
			fmt.Sprintf("review PR #%d: %s", out.pr.Number, out.pr.Title))
	}
	return nil
}

// startGHConversation seeds the agent with prompt while showing a concise label
// in the transcript, mirroring a normal submitted message.
func (m *model) startGHConversation(prompt, label string) tea.Cmd {
	m.showHome = false
	m.pushText(m.renderUserLine(label))
	m.busy = true
	cmd := m.runAgent(prompt)
	m.syncViewport()
	return cmd
}

// applyPick applies a picker selection, surfacing a transient notice.
func (m *model) applyPick(kind pickerKind, target string) {
	switch kind {
	case pickerModel:
		if sw, ok := m.agent.Switcher(); ok {
			sw.SetModel(target)
			m.notice = "✓ switched to " + m.agent.ProviderName()
		}
	case pickerThink:
		if th, ok := m.agent.Thinker(); ok {
			th.SetThinkLevel(llm.ThinkLevel(target))
			m.notice = thinkNotice(th.ThinkLevel())
		}
	case pickerResume:
		m.resume(target)
	}
}

func pickerStyle() widgets.ListStyle {
	return widgets.ListStyle{Title: styleHint, Selected: stylePickSel, Item: styleModel, Box: styleModalBox}
}

func newResumePicker(metas []session.Meta) *widgets.ListPicker {
	items := make([]string, len(metas))
	for i, meta := range metas {
		items[i] = meta.ID
	}
	return widgets.NewListPicker(
		"resume conversation — ↑/↓ move · enter resume · esc cancel",
		items,
		"",
		pickerStyle(),
	)
}

func newModelPicker(models []string, active string) *widgets.ListPicker {
	return widgets.NewListPicker(
		"select model — ↑/↓ move · enter switch · esc cancel",
		models,
		active,
		pickerStyle(),
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
		pickerStyle(),
	)
}
