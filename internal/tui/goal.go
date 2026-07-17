package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/goal"
	"github.com/syrull/pluto/internal/llm"
)

// dbgGoal is the debug component tag for the /goal turn loop.
const dbgGoal = "goal"

// goalMaxCondition caps the /goal condition length, matching upstream's 4,000
// character limit.
const goalMaxCondition = 4000

// goalTranscriptBudget bounds the transcript view sent to the evaluator so a huge
// conversation stays cheap; the most recent activity is kept.
const goalTranscriptBudget = 12000

// goalEvalTimeout bounds a full evaluate command (all retries) so a stuck
// evaluator can't hang the loop indefinitely.
const goalEvalTimeout = 60 * time.Second

// goalClearAliases are the single-word arguments that clear an active goal.
var goalClearAliases = map[string]bool{
	"clear": true, "stop": true, "off": true,
	"reset": true, "none": true, "cancel": true,
}

// goalEvalMsg delivers an evaluator verdict for a workspace's goal back to the
// UI, tagged with the workspace id so a background goal loop routes correctly.
type goalEvalMsg struct {
	id     int
	met    bool
	reason string
	err    error
}

// goalMaxTurns reads the optional PLUTO_GOAL_MAX_TURNS safety valve (a positive
// integer caps the number of turns the loop runs before pausing). 0 (unset or
// invalid) means unlimited, staying faithful to upstream's no-budget default.
func goalMaxTurns() int {
	if v := strings.TrimSpace(os.Getenv("PLUTO_GOAL_MAX_TURNS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// handleGoalCommand dispatches /goal: no argument shows status, a clear alias
// clears the active goal, and anything else is treated as a completion condition
// that is set and kicks off the first turn immediately.
func (m *model) handleGoalCommand(line string) (string, tea.Cmd) {
	fields := strings.Fields(line)
	arg := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
	if arg == "" {
		return m.goalStatus(), nil
	}
	if goalClearAliases[strings.ToLower(arg)] {
		return m.clearGoal("user")
	}
	return m.setGoal(arg)
}

// setGoal validates and stores a completion condition on the active workspace and
// starts the first turn with the condition as the directive.
func (m *model) setGoal(condition string) (string, tea.Cmd) {
	if m.evaluator == nil {
		return styleErr.Render("✗ /goal is unavailable — the evaluator is off (PLUTO_GOAL=off) or its model could not authenticate (see startup output / debug log)"), nil
	}
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return styleErr.Render("✗ /goal needs a condition, e.g. /goal all tests in ./... pass"), nil
	}
	if n := len([]rune(condition)); n > goalMaxCondition {
		return styleErr.Render(fmt.Sprintf("✗ goal condition too long: %d chars (max %d)", n, goalMaxCondition)), nil
	}
	m.goal = &goalState{condition: condition, startedAt: time.Now()}
	debug.Info(dbgGoal, "set", "condition_len", len([]rune(condition)), "turns_cap", m.goalMaxTurns)
	m.notice = "◎ goal set — working until met · esc stops · pair with /auto on for an unattended run"
	m.busy = true
	return m.renderGoalSet(condition), m.runAgent(condition, nil)
}

// clearGoal removes the active goal. reason tags the debug log (user|achieved|
// error|interrupt|max-turns) but only "user" is reachable here; the loop clears
// via applyGoalVerdict / interrupt directly.
func (m *model) clearGoal(reason string) (string, tea.Cmd) {
	if m.goal == nil {
		return styleHint.Render("no goal to clear"), nil
	}
	debug.Info(dbgGoal, "cleared", "reason", reason)
	m.goal = nil
	m.notice = "✓ goal cleared"
	return "", nil
}

// maybeContinueGoal is the heart of the loop: called after a turn finishes on the
// goal's workspace (busy already cleared, no pending steering, not canceled), it
// counts the turn, accumulates token spend, keeps the loop working, and fires the
// evaluator asynchronously. It returns nil (and logs the "nothing happened"
// branch) when there is no active goal to advance.
func (m *model) maybeContinueGoal() tea.Cmd {
	g := m.goal
	if !g.active() {
		if g != nil {
			debug.Debug(dbgGoal, "turn done", "outcome", "skipped", "achieved", g.achieved, "paused", g.paused)
		}
		return nil
	}
	if m.evaluator == nil {
		g.paused = true
		g.lastReason = "evaluator unavailable"
		m.busy = false
		debug.Warn(dbgGoal, "cleared", "reason", "error", "err", "evaluator unavailable")
		return nil
	}
	g.turns++
	if used, _, ok := m.agent.ContextUsage(); ok {
		g.tokens += used
	}
	m.busy = true
	debug.Debug(dbgGoal, "turn done", "turns", g.turns, "tokens", g.tokens, "busy", m.busy)
	return m.evalGoalCmd()
}

// evalGoalCmd snapshots the transcript and runs the evaluator off the UI
// goroutine, returning a goalEvalMsg. The whole call is wrapped in a timer.
func (m *model) evalGoalCmd() tea.Cmd {
	if m.evaluator == nil || !m.goal.active() {
		return nil
	}
	id := m.activeID()
	condition := m.goal.condition
	transcript := renderGoalTranscript(m.agent.Snapshot())
	ev := m.evaluator
	return func() tea.Msg {
		timer := debug.NewTimer(dbgGoal, "evaluate")
		ctx, cancel := context.WithTimeout(context.Background(), goalEvalTimeout)
		defer cancel()
		v, err := ev.Evaluate(ctx, goal.Request{Condition: condition, Transcript: transcript})
		if err != nil {
			timer.Stop("id", id, "outcome", "error")
			return goalEvalMsg{id: id, err: err}
		}
		timer.Stop("id", id, "outcome", "ok", "met", v.Met)
		return goalEvalMsg{id: id, met: v.Met, reason: truncCells(v.Reason, 200)}
	}
}

// applyGoalVerdict records an evaluator verdict on the current workspace and
// returns a continuation directive to start the next turn with, or "" when the
// loop should stop (met, error, interrupt raced in, or turn cap). The caller must
// autosave before starting the returned turn to preserve the doneMsg
// autosave-before-Run ordering.
func (m *model) applyGoalVerdict(msg goalEvalMsg) string {
	g := m.goal
	if g == nil || g.achieved || g.paused {
		// Cleared or paused (interrupt) while the evaluator was in flight: drop the
		// verdict and make sure we're not stuck busy.
		m.busy = false
		debug.Debug(dbgGoal, "verdict ignored", "reason", "no active goal")
		return ""
	}
	if msg.err != nil {
		g.paused = true
		g.lastReason = "evaluator error: " + oneLine(msg.err.Error())
		m.busy = false
		m.pushText(styleErr.Render("⚠ goal evaluator failed — loop paused, goal kept. /goal to inspect · /goal <condition> to retry · /goal clear to cancel"))
		debug.Warn(dbgGoal, "cleared", "reason", "error", "turns", g.turns, "err", msg.err)
		m.syncViewport()
		return ""
	}
	g.lastReason = msg.reason
	if msg.met {
		g.achieved = true
		g.achievedAt = time.Now()
		m.busy = false
		m.pushText(m.renderGoalAchieved(g))
		m.notice = "✓ goal achieved"
		debug.Info(dbgGoal, "achieved", "turns", g.turns, "tokens", g.tokens, "elapsed", time.Since(g.startedAt))
		m.syncViewport()
		return ""
	}
	if m.goalMaxTurns > 0 && g.turns >= m.goalMaxTurns {
		g.paused = true
		m.busy = false
		m.pushText(styleReview.Render(fmt.Sprintf("◎ goal paused after %d turn(s) (PLUTO_GOAL_MAX_TURNS=%d) — not yet met: %s", g.turns, m.goalMaxTurns, oneLine(msg.reason))))
		debug.Info(dbgGoal, "cleared", "reason", "max-turns", "turns", g.turns)
		m.syncViewport()
		return ""
	}
	m.busy = true
	debug.Info(dbgGoal, "continue", "turns", g.turns)
	m.pushText(styleReview.Render("◎ goal not yet met — " + oneLine(msg.reason) + " · continuing"))
	m.syncViewport()
	return goalContinuation(g.condition, msg.reason)
}

// goalContinuation builds the directive for the next turn, folding the
// evaluator's reason in as guidance exactly as upstream does.
func goalContinuation(condition, reason string) string {
	r := strings.TrimSpace(reason)
	if r == "" {
		r = "the condition is not yet demonstrated in your output"
	}
	return "Goal not yet met: " + r + ".\n\n" +
		"Keep working toward this completion condition, and make sure your own output demonstrates it:\n" + condition
}

// renderGoalTranscript renders the agent transcript into a bounded, plain view
// for the evaluator: system turns are dropped, each message is truncated, and
// only the most recent activity within the rune budget is kept.
func renderGoalTranscript(msgs []llm.Message) string {
	const perMsg = 4000
	blocks := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		var b string
		switch msg.Role {
		case llm.RoleUser:
			b = "USER: " + truncGoal(msg.Content, perMsg)
		case llm.RoleModel:
			var parts []string
			if txt := strings.TrimSpace(msg.Content); txt != "" {
				parts = append(parts, "ASSISTANT: "+truncGoal(txt, perMsg))
			}
			for _, c := range msg.ToolCalls {
				parts = append(parts, "ASSISTANT ran tool "+c.Name+": "+truncGoal(string(c.Args), 400))
			}
			b = strings.Join(parts, "\n")
		case llm.RoleTool:
			label := msg.ToolName
			if label == "" {
				label = "tool"
			}
			b = "TOOL RESULT (" + label + "): " + truncGoal(msg.Content, perMsg)
		default:
			continue
		}
		if strings.TrimSpace(b) == "" {
			continue
		}
		blocks = append(blocks, b)
	}
	total, start := 0, len(blocks)
	for i := len(blocks) - 1; i >= 0; i-- {
		total += len([]rune(blocks[i])) + 1
		if total > goalTranscriptBudget {
			break
		}
		start = i
	}
	if start > 0 {
		return "…(earlier turns omitted)\n" + strings.Join(blocks[start:], "\n")
	}
	return strings.Join(blocks, "\n")
}

// truncGoal trims s and clips it to n runes, marking how many were dropped.
func truncGoal(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + fmt.Sprintf("…(+%d)", len(r)-n)
}

// renderGoalSet is the transcript banner shown when a goal is set.
func (m *model) renderGoalSet(condition string) string {
	return styleReview.Render("◎ goal set — "+truncCells(oneLine(condition), 200)) + "\n" +
		styleHint.Render("Working until an evaluator judges it met. esc stops · /goal clear cancels · /auto on for unattended runs.")
}

// renderGoalAchieved is the transcript banner shown when the condition is met.
func (m *model) renderGoalAchieved(g *goalState) string {
	return styleDone.Render("◎ goal achieved") + "\n" +
		styleHint.Render(fmt.Sprintf("%s · %d turn(s) · %s tokens · %s",
			truncCells(oneLine(g.condition), 160), g.turns, formatTokens(g.tokens), oneLine(g.lastReason)))
}

// goalStatus renders the /goal (no-arg) status block for the active workspace.
func (m *model) goalStatus() string {
	g := m.goal
	if g == nil {
		if m.evaluator == nil {
			return styleHint.Render("no goal set — /goal is unavailable (evaluator off or its model could not authenticate)")
		}
		return styleHint.Render("no goal set — /goal <condition> keeps working until an evaluator judges it met (pair with /auto on)")
	}
	var b strings.Builder
	switch {
	case g.achieved:
		fmt.Fprintf(&b, "%s\n", styleDone.Render("◎ goal achieved"))
	case g.paused:
		fmt.Fprintf(&b, "%s\n", styleReview.Render("◎ goal paused"))
	default:
		fmt.Fprintf(&b, "%s\n", styleReview.Render("◎ goal active"))
	}
	fmt.Fprintf(&b, "%s %s\n", styleHint.Render("condition:"), truncCells(oneLine(g.condition), 200))
	elapsed := time.Since(g.startedAt)
	if g.achieved {
		elapsed = g.achievedAt.Sub(g.startedAt)
	}
	fmt.Fprintf(&b, "%s %s · %s %d · %s %s\n",
		styleHint.Render("elapsed:"), fmtGoalElapsed(elapsed),
		styleHint.Render("turns:"), g.turns,
		styleHint.Render("tokens:"), formatTokens(g.tokens))
	if strings.TrimSpace(g.lastReason) != "" {
		fmt.Fprintf(&b, "%s %s\n", styleHint.Render("last check:"), truncCells(oneLine(g.lastReason), 200))
	}
	switch {
	case g.achieved:
		fmt.Fprintf(&b, "%s", styleHint.Render("/goal <condition> to set a new goal"))
	case g.paused:
		fmt.Fprintf(&b, "%s", styleHint.Render("paused — /goal <condition> to retry · /goal clear to cancel"))
	default:
		fmt.Fprintf(&b, "%s", styleHint.Render("esc stops · /goal clear cancels · pair with /auto on"))
	}
	return b.String()
}

// goalChip renders the status-line goal indicator: "◎ goal <elapsed>" while
// active, "◎ goal paused" once stopped, or nothing (achieved/cleared/no goal).
// It returns the styled span and the raw text (empty ⇒ no chip).
func goalChip(g *goalState) (string, string) {
	switch {
	case g == nil, g.achieved:
		return "", ""
	case g.paused:
		return styleStatusGoal.Render("◎ goal paused"), "◎ goal paused"
	default:
		raw := "◎ goal " + fmtGoalElapsed(time.Since(g.startedAt))
		return styleStatusGoal.Render(raw), raw
	}
}

// fmtGoalElapsed formats a goal's running time compactly.
func fmtGoalElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// goalFromSaved rebuilds an active goal from a persisted condition, resetting the
// timer/turn/token baseline as upstream does on resume. An empty condition
// (unset or an achieved/cleared goal) restores no goal.
func goalFromSaved(condition string) *goalState {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return nil
	}
	return &goalState{condition: condition, startedAt: time.Now()}
}
