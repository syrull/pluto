package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/session"
	"github.com/syrull/pluto/internal/tui/widgets"
	"github.com/syrull/pluto/internal/workdir"
)

// listen delivers the next event from a workspace's stream, tagging it (and the
// terminating doneMsg) with the workspace id so the UI routes it correctly.
func listen(id int, ch chan eventMsg) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return doneMsg{id: id}
		}
		return ev
	}
}

// runAgent starts the active workspace's agent on input, scoping its tools and
// the review gate to that agent's working directory and tagging its events with
// the agent id so background runs stay independent.
func (m *model) runAgent(input string, attachments []llm.Attachment) tea.Cmd {
	id := m.activeID()
	ag := m.agent
	ch := make(chan eventMsg, 16)
	m.events = ch
	ctx, cancel := context.WithCancel(workdir.With(context.Background(), m.activeCwd()))
	m.cancel = cancel
	go func() {
		defer close(ch)
		_, _ = ag.Run(ctx, input, attachments, func(ev agent.Event) {
			ch <- eventMsg{id: id, Kind: ev.Kind, Text: ev.Text, Tool: ev.Tool}
		})
	}()
	return listen(id, ch)
}

// applyEvent renders one agent Event into the current (active or swapped-in)
// workspace state, returning an optional follow-up command. A write/edit result
// refreshes the sidebar so file mutations show up live.
func (m *model) applyEvent(ev agent.Event) tea.Cmd {
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
		if ev.Tool == "write" || ev.Tool == "edit" {
			extra = m.gatherGit()
		}
		m.appendToolResult(ev)
	default:
		m.pushText(renderEvent(m.contentWidth(), ev))
	}
	return extra
}

// interrupt aborts the in-flight Run: it cancels the request context (stopping
// the LLM stream and any running tool) and drops any queued steering so the
// canceled turn doesn't immediately restart. The pending listener delivers
// doneMsg once the run unwinds, which clears busy. An active goal loop is paused
// (not cleared) so the canceled turn doesn't restart it, while the condition
// stays inspectable via /goal.
func (m *model) interrupt() {
	debug.Info(dbgTUI, "interrupt", "id", m.activeID())
	running := m.cancel != nil
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.agent.TakeSteering()
	if m.goal.active() {
		m.goal.paused = true
		m.goal.lastReason = "interrupted"
		debug.Info(dbgGoal, "cleared", "reason", "interrupt", "turns", m.goal.turns)
		// During the evaluate phase there is no Run to unwind (no doneMsg will
		// clear busy), so drop busy now; a pending verdict is ignored on arrival.
		if !running {
			m.busy = false
		}
	}
	m.notice = "✗ canceled request"
}

// finishTurn wraps up a completed run for the current (active or swapped-in)
// workspace: it clears run state, requests an auto-label after the first turn,
// and refreshes git. It does NOT start the steered follow-up itself — it returns
// any messages steered in as the run ended so the caller can persist (autosave)
// before restartSteering starts a new goroutine, avoiding a Snapshot/Run race.
func (m *model) finishTurn() ([]agent.SteerMessage, tea.Cmd) {
	m.flushStream()
	m.busy = false
	m.events = nil
	m.cancel = nil
	label := m.maybeLabel(m.active)
	pending := m.agent.TakeSteering()
	m.syncViewport()
	// Refresh the sidebar to reflect any files the turn changed.
	return pending, tea.Batch(m.gatherGit(), label)
}

// restartSteering continues the conversation with messages that were steered in
// as the previous run ended, carrying along any attachments. It must run in the
// owning workspace's context (m.active pointing at it) so the follow-up run uses
// that agent's directory and id.
func (m *model) restartSteering(pending []agent.SteerMessage) tea.Cmd {
	m.busy = true
	texts := make([]string, 0, len(pending))
	var atts []llm.Attachment
	for _, p := range pending {
		texts = append(texts, p.Text)
		atts = append(atts, p.Attachments...)
	}
	cmd := m.runAgent(strings.Join(texts, "\n\n"), atts)
	m.syncViewport()
	return cmd
}

func (m *model) handleCommand(line string) (string, tea.Cmd) {
	fields := strings.Fields(line)
	debug.Info(dbgTUI, "slash command", "cmd", fields[0], "args", len(fields)-1)
	switch fields[0] {
	case "/new":
		// Clears only the active agent's conversation; other agents keep running.
		m.agent.Reset()
		m.lines = nil
		m.outputs = nil
		m.codeBlocks = nil
		m.pendingTool = ""
		m.pendingArgs = ""
		m.streamText = ""
		m.streamThink = ""
		m.sessionName = ""
		if m.goal != nil {
			debug.Info(dbgGoal, "cleared", "reason", "new")
		}
		m.goal = nil
		if w := m.workspaceAt(m.active); w != nil {
			w.label = ""
			w.labeled = false
			w.goal = nil
		}
		m.showHome = true
		m.orbitEpoch++
		m.notice = "✓ started a new conversation"
		return "", orbitTick(m.orbitEpoch)

	case "/close":
		// Closes (deletes) the active agent, tearing down its run and workspace.
		return "", m.promptClose()

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
		arg := ""
		if len(fields) > 1 {
			arg = fields[1]
		}
		if arg == "" || arg == "--all" || arg == "-a" {
			all := arg != ""
			metas, err := store.ListForCwd(m.sessionCwd())
			if all {
				metas, err = store.List()
			}
			if err != nil {
				return styleErr.Render("✗ " + err.Error()), nil
			}
			scope := "folder"
			if all {
				scope = "all"
			}
			debug.Debug(dbgTUI, "resume list", "scope", scope, "cwd", m.sessionCwd(), "count", len(metas))
			if len(metas) == 0 {
				hint := "no saved conversations for this folder — use /save, or /resume --all to list every folder"
				if all {
					hint = "no saved conversations yet — use /save first"
				}
				return styleHint.Render(hint), nil
			}
			m.picker = newResumePicker(metas)
			m.picker.SetSize(m.width, m.height)
			m.pickerKind = pickerResume
			return "", nil
		}
		return "", m.resume(arg)

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

	case "/goal":
		return m.handleGoalCommand(line)

	case "/install-mcp":
		return m.handleInstallMCP(line)

	case "/learn":
		if len(fields) > 1 {
			switch fields[1] {
			case "on":
				m.agent.SetLearnMode(true)
			case "off":
				m.agent.SetLearnMode(false)
			default:
				return styleErr.Render("✗ usage: /learn [on|off]"), nil
			}
		} else {
			m.agent.SetLearnMode(!m.agent.LearnMode())
		}
		if m.agent.LearnMode() {
			m.notice = "✓ learn mode on — I'll explain Go and the codebase as we go"
		} else {
			m.notice = "✓ learn mode off"
		}
		return "", nil

	case "/gh":
		if !ghAvailable() {
			return styleErr.Render("✗ gh unavailable — install the GitHub CLI and use a github.com remote"), nil
		}
		m.ghm = newGHModal()
		m.ghm.SetSize(m.width, m.height)
		m.ghm.SetContext(m.ghContextRefs())
		return "", fetchGitHubCmd

	case "/image":
		if len(fields) < 2 {
			return styleErr.Render("✗ usage: /image <path>"), nil
		}
		path := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		att, err := loadImageAttachment(path)
		if err != nil {
			return styleErr.Render("✗ " + err.Error()), nil
		}
		m.attachments = append(m.attachments, att)
		m.notice = fmt.Sprintf("✓ attached %s (%s) — %d staged, send with your next message", att.Name, humanBytes(len(att.Data)), len(m.attachments))
		return "", nil

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

// Update logs each incoming message and the resulting state (when debug logging
// is on) then delegates to update, so a session can be replayed frame by frame.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	lvl := msgLevel(msg)
	if debug.Should(dbgTUI, lvl) {
		typ, kv := msgSummary(msg)
		debug.Event(dbgTUI, lvl, "update "+typ, kv...)
	}
	next, cmd := m.update(msg)
	if debug.Should(dbgTUI, lvl) {
		if nm, ok := next.(model); ok {
			off := 0
			if nm.ready {
				off = nm.vp.YOffset()
			}
			debug.Event(dbgTUI, lvl, "state",
				"focus", focusName(nm.focus), "busy", nm.busy, "home", nm.showHome,
				"overlay", nm.overlayName(), "w", nm.width, "h", nm.height,
				"scroll", off, "lines", len(nm.lines), "cmd", cmd != nil)
		}
	}
	return next, cmd
}

// update handles Bubbletea messages and returns the updated model and commands.
func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			m.tree = newFileTree(m.activeCwd())
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
		debug.Debug(dbgTUI, "layout", "w", msg.Width, "h", msg.Height,
			"conv_w", cw, "conv_h", ch, "sidebar_w", m.sidebarWidth(), "input_w", inW)
		return m, nil

	case tea.KeyPressMsg:
		ks := msg.String()
		m.notice = ""
		// A pending judge-error approval is modal: it captures y/a/n (and esc ⇒ no)
		// and swallows everything else until the human answers.
		if m.approval != nil {
			switch ks {
			case "ctrl+c":
				return m, tea.Quit
			case "y":
				return m, m.answerApproval(agent.ApprovalYes)
			case "a":
				return m, m.answerApproval(agent.ApprovalPattern)
			case "n", "esc":
				return m, m.answerApproval(agent.ApprovalNo)
			}
			return m, nil
		}
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
				return m, m.applyPick(kind, target)
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
			// ↑/↓ always scroll the transcript; recall lives on ctrl+p/ctrl+n so
			// reading back a finished turn never falls into the last message.
			debug.Trace(dbgTUI, "chat key", "key", ks, "action", "scroll")
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		case "ctrl+p":
			// Recall the previous input; on an empty buffer only. When there's
			// nothing to recall, fall through so the textarea moves the cursor up a
			// line in a multi-line draft.
			if m.historyPrev() {
				debug.Trace(dbgTUI, "chat key", "key", ks, "action", "recall")
				m.refreshCommandMenu()
				return m, nil
			}
			debug.Trace(dbgTUI, "chat key", "key", ks, "action", "cursor")
			return m.editInput(msg)
		case "ctrl+n":
			if m.historyNext() {
				debug.Trace(dbgTUI, "chat key", "key", ks, "action", "recall")
				m.refreshCommandMenu()
				return m, nil
			}
			debug.Trace(dbgTUI, "chat key", "key", ks, "action", "cursor")
			return m.editInput(msg)
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
			// A bare enter with staged images or issue context still sends (an
			// attachment/context-only turn).
			if in == "" && len(m.attachments) == 0 && len(m.ghContext) == 0 {
				return m, nil
			}
			m.showHome = false
			m.recordHistory(in)
			// Inline shell: `!cmd` runs immediately, independent of the agent,
			// whether or not a turn is in flight.
			if strings.HasPrefix(in, "!") {
				cmd := m.handleInline(in)
				m.syncViewport()
				return m, cmd
			}
			// The input stays live during generation: a plain message is queued
			// to steer the running turn; background-safe slash commands dispatch
			// immediately, and the rest wait until it's idle.
			if m.busy {
				if strings.HasPrefix(in, "/") {
					// A few commands act on TUI/gate state without touching the
					// running turn (e.g. /gh, /auto), so they're dispatched in the
					// background instead of being deferred; the rest still wait.
					if runsInBackground(in) {
						debug.Info(dbgTUI, "background command while busy", "cmd", strings.Fields(in)[0])
						m.input.Reset()
						status, cmd := m.handleCommand(in)
						if status != "" {
							m.pushText(status)
						}
						m.syncViewport()
						return m, cmd
					}
					debug.Debug(dbgTUI, "command deferred while busy", "cmd", strings.Fields(in)[0])
					m.pushText(styleErr.Render("✗ commands are unavailable while the agent is working"))
					m.syncViewport()
					return m, nil
				}
				atts := m.takeAttachments()
				issues := m.takeGHContext()
				m.pushText(m.renderUserLine(in, atts, issues))
				m.input.Reset()
				m.agent.Steer(composeWithGHContext(issues, in), atts...)
				m.syncViewport()
				return m, nil
			}
			// Most slash commands are TUI actions, not messages to the agent, so
			// they're dispatched without echoing the command text into the
			// transcript; only any command status/output is shown. /image is the
			// exception: it composes the next message by staging an attachment, so
			// it still echoes as a user line (rendered without a chip).
			if strings.HasPrefix(in, "/") {
				if strings.Fields(in)[0] == "/image" {
					m.pushText(m.renderUserLine(in, nil, nil))
				}
				m.input.Reset()
				status, cmd := m.handleCommand(in)
				if status != "" {
					m.pushText(status)
				}
				m.syncViewport()
				return m, cmd
			}
			atts := m.takeAttachments()
			issues := m.takeGHContext()
			m.pushText(m.renderUserLine(in, atts, issues))
			m.input.Reset()
			m.busy = true
			cmd := m.runAgent(composeWithGHContext(issues, in), atts)
			m.syncViewport()
			return m, cmd
		default:
			// The textarea's word-backward (alt/Command+←) infinite-loops when only
			// whitespace lies to the left; there's nothing to jump to, so no-op.
			if slices.Contains(m.input.KeyMap.WordBackward.Keys(), ks) && m.nothingLeftOfCursor() {
				debug.Trace(dbgTUI, "word jump", "dir", "back", "outcome", "nothing-left")
				return m, nil
			}
			return m.editInput(msg)
		}

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case eventMsg:
		ev := msg.event()
		i := m.workspaceIndex(msg.id)
		switch {
		case len(m.workspaces) == 0: // bare/test model: apply directly
			extra := m.applyEvent(ev)
			m.syncViewport()
			if extra != nil {
				return m, tea.Batch(listen(msg.id, m.events), extra)
			}
			return m, listen(msg.id, m.events)
		case i < 0:
			return m, nil // stale event from a workspace that no longer exists
		case i == m.active:
			extra := m.applyEvent(ev)
			m.syncViewport()
			if extra != nil {
				return m, tea.Batch(listen(msg.id, m.events), extra)
			}
			return m, listen(msg.id, m.events)
		default:
			// Background agent: render into its own state and flag unread progress.
			ws := m.workspaces[i]
			m.onWorkspace(i, func() { m.applyEvent(ev) })
			ws.unread = true
			return m, listen(msg.id, ws.events)
		}

	case approvalReqMsg:
		// The requesting agent goroutine is blocked in Approve until the user
		// answers; render the prompt and wait. The listener is re-armed only after
		// the decision (answerApproval), so queued requests are handled one at a time.
		m.approval = msg.req
		cmd, intent, why := approvalArgs(msg.req.call)
		debug.Info(dbgTUI, "approval prompt shown",
			"tool", msg.req.call.Name, "cmd", truncCells(oneLine(cmd), 200),
			"intent", truncCells(oneLine(intent), 120), "why", truncCells(oneLine(why), 120),
			"pattern", truncCells(msg.req.rr.Pattern, 200), "reason", msg.req.rr.Reason)
		m.syncViewport()
		return m, nil

	case bashInlineMsg:
		// Drop a result from a canceled or superseded inline run.
		if msg.epoch != m.inlineEpoch {
			return m, nil
		}
		m.applyInlineResult(msg)
		m.syncViewport()
		// An inline command can mutate the tree and branch; refresh the sidebar.
		return m, m.gatherGit()

	case doneMsg:
		i := m.workspaceIndex(msg.id)
		switch {
		case len(m.workspaces) == 0, i == m.active: // bare/test model or the active agent
			pending, cmd := m.finishTurn()
			// Autosave before any restarted goroutine so Snapshot never races Run.
			m.autosave()
			if len(pending) > 0 {
				// Steering takes priority; the goal loop resumes after that turn.
				if m.goal.active() {
					debug.Debug(dbgGoal, "turn done", "outcome", "skipped", "reason", "steering")
				}
				cmd = tea.Batch(cmd, m.restartSteering(pending))
				return m, cmd
			}
			if gc := m.maybeContinueGoal(); gc != nil {
				cmd = tea.Batch(cmd, gc)
			}
			return m, cmd
		case i < 0:
			return m, nil
		default:
			var pending []agent.SteerMessage
			var cmd tea.Cmd
			m.onWorkspace(i, func() { pending, cmd = m.finishTurn() })
			m.workspaces[i].unread = true
			// autosave runs here (active restored) so it records the real active,
			// and before restartSteering starts the follow-up run's goroutine.
			m.autosave()
			if len(pending) > 0 {
				if m.workspaces[i].goal.active() {
					debug.Debug(dbgGoal, "turn done", "outcome", "skipped", "reason", "steering", "id", msg.id)
				}
				var rcmd tea.Cmd
				m.onWorkspace(i, func() { rcmd = m.restartSteering(pending) })
				return m, tea.Batch(cmd, rcmd)
			}
			// Advance the background workspace's goal loop under its own context.
			var gc tea.Cmd
			m.onWorkspace(i, func() { gc = m.maybeContinueGoal() })
			if gc != nil {
				cmd = tea.Batch(cmd, gc)
			}
			return m, cmd
		}
	case goalEvalMsg:
		i := m.workspaceIndex(msg.id)
		switch {
		case len(m.workspaces) == 0, i == m.active: // bare/test model or the active agent
			directive := m.applyGoalVerdict(msg)
			// Autosave before the continuation goroutine so Snapshot never races Run.
			m.autosave()
			if directive != "" {
				cmd := m.runAgent(directive, nil)
				return m, cmd
			}
			return m, nil
		case i < 0:
			return m, nil
		default:
			var directive string
			m.onWorkspace(i, func() { directive = m.applyGoalVerdict(msg) })
			m.workspaces[i].unread = true
			m.autosave()
			if directive != "" {
				var cmd tea.Cmd
				m.onWorkspace(i, func() { cmd = m.runAgent(directive, nil) })
				return m, cmd
			}
			return m, nil
		}
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
		m.applyGitInfo(msg)
		return m, nil
	case editorDoneMsg:
		// The editor may have changed the file; refresh a still-open file/diff
		// modal so it reflects the edit, and refresh the sidebar.
		if msg.err == nil && m.modal != nil && m.modalIsFile && m.modalPath != "" {
			m.openFileDiff(m.modalPath)
		}
		if msg.err == nil {
			return m, m.gatherGit()
		}
		return m, nil
	case labelMsg:
		if i := m.workspaceIndex(msg.id); i >= 0 && strings.TrimSpace(msg.label) != "" {
			m.workspaces[i].label = msg.label
		}
		return m, nil
	}
	// Bracketed paste (tea.PasteMsg) and the textarea's async ctrl+v clipboard
	// read arrive as unhandled messages; route them into the input buffer.
	return m.forwardToInput(msg)
}

// editInput forwards a key into the input textarea, dropping out of history
// navigation when the edit changes the buffer so a later recall can't silently
// clobber it; a pure cursor move (e.g. ctrl+p/ctrl+n line nav) stays in.
func (m model) editInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.showHome = false
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.navigatingHistory() && m.input.Value() != before {
		m.histPos = len(m.history)
	}
	m.refreshCommandMenu()
	return m, cmd
}

// navigatingHistory reports whether ctrl+p/ctrl+n are currently walking recalled
// inputs rather than editing a fresh line.
func (m *model) navigatingHistory() bool { return m.histPos < len(m.history) }

// nothingLeftOfCursor reports whether every rune before the cursor is
// whitespace. The bundled textarea's word-backward (alt/Command+←) infinite-
// loops whenever it walks left through only whitespace back to the start of the
// buffer — empty buffer, leading spaces, or an empty first line above the
// cursor all trigger it — so callers guard it. There's nothing to jump to anyway.
func (m *model) nothingLeftOfCursor() bool {
	lines := strings.Split(m.input.Value(), "\n")
	row := m.input.Line()
	if row < 0 || row >= len(lines) {
		return true
	}
	for _, l := range lines[:row] {
		if strings.TrimSpace(l) != "" {
			return false
		}
	}
	li := m.input.LineInfo()
	col := li.StartColumn + li.ColumnOffset
	cur := []rune(lines[row])
	if col > len(cur) {
		col = len(cur)
	}
	return strings.TrimSpace(string(cur[:col])) == ""
}

// recordHistory appends a submitted input to the recall history (skipping blanks
// and consecutive duplicates) and rewinds the navigation cursor to the end so the
// next ctrl+p starts from the most recent entry.
func (m *model) recordHistory(in string) {
	if in == "" {
		m.histPos = len(m.history)
		return
	}
	added := false
	if n := len(m.history); n == 0 || m.history[n-1] != in {
		m.history = append(m.history, in)
		added = true
	}
	m.histPos = len(m.history)
	debug.Debug(dbgTUI, "history record", "added", added, "entries", len(m.history), "chars", len(in))
}

// setInputFromHistory loads the entry at histPos into the buffer (cursor at end)
// and dismisses the home screen so recall behaves like typing.
func (m *model) setInputFromHistory() {
	m.input.SetValue(m.history[m.histPos])
	m.input.CursorEnd()
	m.showHome = false
}

// historyPrev recalls the previous (older) submitted input, starting recall only
// from an empty buffer. It reports whether it consumed the key so the caller
// falls through to the textarea (cursor up a line) when there is nothing to recall.
func (m *model) historyPrev() bool {
	if len(m.history) == 0 {
		debug.Trace(dbgTUI, "history recall", "dir", "prev", "outcome", "empty")
		return false
	}
	if !m.navigatingHistory() {
		if strings.TrimSpace(m.input.Value()) != "" {
			debug.Trace(dbgTUI, "history recall", "dir", "prev", "outcome", "buffer-dirty")
			return false
		}
		m.histPos = len(m.history)
	}
	if m.histPos == 0 {
		debug.Trace(dbgTUI, "history recall", "dir", "prev", "pos", m.histPos, "outcome", "at-oldest")
		return true
	}
	m.histPos--
	m.setInputFromHistory()
	debug.Debug(dbgTUI, "history recall", "dir", "prev", "pos", m.histPos, "entries", len(m.history))
	return true
}

// historyNext walks toward the newest submitted input, clearing the buffer once
// it steps past the last entry. It acts only while navigating; otherwise it
// reports false so the caller falls through to the textarea (cursor down a line).
func (m *model) historyNext() bool {
	if !m.navigatingHistory() {
		debug.Trace(dbgTUI, "history recall", "dir", "next", "outcome", "not-navigating")
		return false
	}
	m.histPos++
	if m.histPos >= len(m.history) {
		m.histPos = len(m.history)
		m.input.Reset()
		m.showHome = false
		debug.Debug(dbgTUI, "history recall", "dir", "next", "pos", m.histPos, "outcome", "cleared")
		return true
	}
	m.setInputFromHistory()
	debug.Debug(dbgTUI, "history recall", "dir", "next", "pos", m.histPos, "entries", len(m.history))
	return true
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
	case ghOutcomeAddContext:
		m.toggleGHContext(out.ctx)
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
	m.pushText(m.renderUserLine(label, nil, nil))
	m.busy = true
	cmd := m.runAgent(prompt, nil)
	m.syncViewport()
	return cmd
}

// applyPick applies a picker selection, surfacing a transient notice and, for
// the new-agent picker, returning the command that spawns the workspace.
func (m *model) applyPick(kind pickerKind, target string) tea.Cmd {
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
		return m.resume(target)
	case pickerNewAgent:
		return m.applyNewAgentPick(target)
	case pickerCloseAgent:
		return m.applyClosePick(target)
	}
	return nil
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

func newNewAgentPicker() *widgets.ListPicker {
	return widgets.NewListPicker(
		"new agent — ↑/↓ move · enter select · esc cancel",
		[]string{worktreeOption, currentDirOption},
		worktreeOption,
		pickerStyle(),
	)
}

// newCloseAgentPicker builds the close confirmation. When the agent owns a
// worktree it offers remove-vs-keep (defaulting to keep); otherwise a plain
// confirm/cancel.
func newCloseAgentPicker(worktree bool) *widgets.ListPicker {
	items := []string{closeConfirmOption, closeCancelOption}
	active := closeConfirmOption
	if worktree {
		items = []string{closeRemoveWorktreeOption, closeKeepWorktreeOption, closeCancelOption}
		active = closeKeepWorktreeOption
	}
	return widgets.NewListPicker(
		"close agent — ↑/↓ move · enter select · esc cancel",
		items,
		active,
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
