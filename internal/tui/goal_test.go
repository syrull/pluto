package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/goal"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

// goalModel builds a bare model wired with a Fake evaluator.
func goalModel(ev goal.Evaluator) model {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	return model{agent: ag, md: newRenderer(80), input: newInput(80), evaluator: ev}
}

func hasLine(m model, want string) bool {
	for _, e := range m.lines {
		if strings.Contains(e.text, want) {
			return true
		}
	}
	return false
}

func TestGoalSetStartsTurn(t *testing.T) {
	m := goalModel(goal.Fake{})
	status, cmd := m.handleCommand("/goal make all tests pass")
	if m.goal == nil || m.goal.condition != "make all tests pass" {
		t.Fatalf("goal not set: %+v", m.goal)
	}
	if !m.busy {
		t.Fatal("setting a goal should start a turn (busy)")
	}
	if cmd == nil {
		t.Fatal("setting a goal should return a run command")
	}
	if !strings.Contains(status, "goal set") {
		t.Fatalf("status should confirm goal set, got %q", status)
	}
}

func TestGoalReplaceResetsCounters(t *testing.T) {
	m := goalModel(goal.Fake{})
	m.goal = &goalState{condition: "old", startedAt: time.Now(), turns: 7, tokens: 500, lastReason: "stale"}
	m.handleCommand("/goal brand new condition")
	if m.goal.condition != "brand new condition" {
		t.Fatalf("replace should set the new condition, got %q", m.goal.condition)
	}
	if m.goal.turns != 0 || m.goal.tokens != 0 || m.goal.lastReason != "" {
		t.Fatalf("replace should reset counters, got %+v", m.goal)
	}
}

func TestGoalClearAliases(t *testing.T) {
	for _, alias := range []string{"clear", "stop", "off", "reset", "none", "cancel"} {
		m := goalModel(goal.Fake{})
		m.goal = &goalState{condition: "x", startedAt: time.Now()}
		m.handleCommand("/goal " + alias)
		if m.goal != nil {
			t.Fatalf("/goal %s should clear the goal", alias)
		}
	}
}

func TestGoalConditionNotAClearAlias(t *testing.T) {
	m := goalModel(goal.Fake{})
	// A multi-word condition beginning with an alias word is a condition, not clear.
	m.handleCommand("/goal stop the flaky test failures")
	if m.goal == nil || m.goal.condition != "stop the flaky test failures" {
		t.Fatalf("a multi-word condition must not be treated as clear: %+v", m.goal)
	}
}

func TestGoalEmptyConditionRejected(t *testing.T) {
	m := goalModel(goal.Fake{})
	status, cmd := m.setGoal("   ")
	if m.goal != nil {
		t.Fatal("an empty condition must not set a goal")
	}
	if cmd != nil || !strings.Contains(status, "needs a condition") {
		t.Fatalf("empty condition should be rejected, got status %q", status)
	}
}

func TestGoalTooLongRejected(t *testing.T) {
	m := goalModel(goal.Fake{})
	long := strings.Repeat("x", goalMaxCondition+1)
	status, _ := m.handleCommand("/goal " + long)
	if m.goal != nil {
		t.Fatal("an over-long condition must not set a goal")
	}
	if !strings.Contains(status, "too long") {
		t.Fatalf("over-long condition should be rejected, got %q", status)
	}
}

func TestGoalUnavailableWithoutEvaluator(t *testing.T) {
	m := goalModel(nil) // no evaluator wired
	status, cmd := m.handleCommand("/goal do the thing")
	if m.goal != nil {
		t.Fatal("no goal should be set without an evaluator")
	}
	if cmd != nil || !strings.Contains(status, "unavailable") {
		t.Fatalf("/goal without an evaluator should report unavailable, got %q", status)
	}
}

func TestGoalTurnDoneFiresEvaluator(t *testing.T) {
	m := goalModel(goal.Fake{Verdict: goal.Verdict{Met: true, Reason: "done"}})
	m.goal = &goalState{condition: "c", startedAt: time.Now()}
	cmd := m.maybeContinueGoal()
	if cmd == nil {
		t.Fatal("an active goal should fire the evaluator after a turn")
	}
	if m.goal.turns != 1 {
		t.Fatalf("turns = %d, want 1", m.goal.turns)
	}
	if !m.busy {
		t.Fatal("the loop should keep working while evaluating")
	}
	msg, ok := cmd().(goalEvalMsg)
	if !ok {
		t.Fatalf("eval cmd should return goalEvalMsg, got %T", cmd())
	}
	if !msg.met || msg.reason != "done" {
		t.Fatalf("eval msg = %+v, want met/done", msg)
	}
}

func TestGoalLoopSkipsWhenPaused(t *testing.T) {
	m := goalModel(goal.Fake{})
	m.goal = &goalState{condition: "c", startedAt: time.Now(), paused: true}
	if cmd := m.maybeContinueGoal(); cmd != nil {
		t.Fatal("a paused goal must not fire the evaluator")
	}
	if m.goal.turns != 0 {
		t.Fatal("a paused goal must not count turns")
	}
}

func TestGoalNotMetContinuesWithReason(t *testing.T) {
	m := goalModel(goal.Fake{})
	m.goal = &goalState{condition: "make tests pass", startedAt: time.Now(), turns: 1}
	directive := m.applyGoalVerdict(goalEvalMsg{met: false, reason: "tests still failing"})
	if directive == "" {
		t.Fatal("a not-met verdict should return a continuation directive")
	}
	if !strings.Contains(directive, "tests still failing") {
		t.Fatalf("directive should fold in the reason, got %q", directive)
	}
	if !strings.Contains(directive, "make tests pass") {
		t.Fatalf("directive should carry the condition, got %q", directive)
	}
	if !m.busy {
		t.Fatal("a not-met verdict should keep the loop working")
	}
	if m.goal.lastReason != "tests still failing" {
		t.Fatalf("lastReason = %q", m.goal.lastReason)
	}
}

func TestGoalMetClearsAndRecordsAchieved(t *testing.T) {
	m := goalModel(goal.Fake{})
	m.goal = &goalState{condition: "c", startedAt: time.Now(), turns: 2}
	directive := m.applyGoalVerdict(goalEvalMsg{met: true, reason: "all pass"})
	if directive != "" {
		t.Fatalf("a met verdict should not continue, got %q", directive)
	}
	if m.goal == nil || !m.goal.achieved {
		t.Fatal("a met verdict should record the goal as achieved")
	}
	if m.busy {
		t.Fatal("a met verdict should stop the loop")
	}
	if !hasLine(m, "achieved") {
		t.Fatal("a met verdict should emit an achieved line")
	}
}

func TestGoalEvaluatorErrorPauses(t *testing.T) {
	m := goalModel(goal.Fake{})
	m.goal = &goalState{condition: "c", startedAt: time.Now(), turns: 1}
	directive := m.applyGoalVerdict(goalEvalMsg{err: errors.New("provider down")})
	if directive != "" {
		t.Fatal("an evaluator error must not continue the loop")
	}
	if m.goal == nil {
		t.Fatal("an evaluator error should keep the goal for inspection")
	}
	if !m.goal.paused {
		t.Fatal("an evaluator error should pause the loop")
	}
	if m.busy {
		t.Fatal("an evaluator error should stop the loop working")
	}
}

func TestGoalMaxTurnsPauses(t *testing.T) {
	m := goalModel(goal.Fake{})
	m.goalMaxTurns = 2
	m.goal = &goalState{condition: "c", startedAt: time.Now(), turns: 2}
	directive := m.applyGoalVerdict(goalEvalMsg{met: false, reason: "not yet"})
	if directive != "" {
		t.Fatal("reaching the turn cap should pause, not continue")
	}
	if !m.goal.paused {
		t.Fatal("reaching the turn cap should pause the goal")
	}
	if m.busy {
		t.Fatal("reaching the turn cap should stop the loop working")
	}
}

func TestGoalInterruptStopsLoop(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	_, cancel := context.WithCancel(context.Background())
	m := model{
		agent: ag, md: newRenderer(80), input: newInput(80), busy: true, cancel: cancel,
		evaluator: goal.Fake{}, goal: &goalState{condition: "c", startedAt: time.Now()},
	}
	m.interrupt()
	if !m.goal.paused {
		t.Fatal("interrupt should pause the goal loop")
	}
	if cmd := m.maybeContinueGoal(); cmd != nil {
		t.Fatal("a paused goal must not restart after a canceled turn")
	}
}

func TestGoalNewClears(t *testing.T) {
	m := goalModel(goal.Fake{})
	m.goal = &goalState{condition: "c", startedAt: time.Now()}
	m.handleCommand("/new")
	if m.goal != nil {
		t.Fatal("/new should clear the active goal")
	}
}

func TestGoalVerdictIgnoredWhenCleared(t *testing.T) {
	m := goalModel(goal.Fake{})
	m.busy = true
	m.goal = nil // cleared while the evaluator was in flight
	directive := m.applyGoalVerdict(goalEvalMsg{met: false, reason: "late"})
	if directive != "" {
		t.Fatal("a verdict for a cleared goal must not start a turn")
	}
	if m.busy {
		t.Fatal("a stray verdict should not leave the UI stuck busy")
	}
}

func TestGoalStatusNoGoal(t *testing.T) {
	m := goalModel(goal.Fake{})
	status, _ := m.handleCommand("/goal")
	if !strings.Contains(status, "no goal set") {
		t.Fatalf("no-arg /goal without a goal should report none, got %q", status)
	}
}

func TestGoalStatusShowsDetails(t *testing.T) {
	m := goalModel(goal.Fake{})
	m.goal = &goalState{
		condition: "ship it", startedAt: time.Now().Add(-90 * time.Second),
		turns: 3, tokens: 4200, lastReason: "almost there",
	}
	status, _ := m.handleCommand("/goal")
	for _, want := range []string{"ship it", "turns", "3", "4.2K", "almost there"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status should contain %q, got:\n%s", want, status)
		}
	}
}

func TestGoalChipShownWhenActive(t *testing.T) {
	m := &model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), ""), goal: &goalState{condition: "c", startedAt: time.Now()}}
	if status := m.modelStatus(); !strings.Contains(status, "◎ goal") {
		t.Fatalf("status line should show the goal chip, got:\n%s", status)
	}
}

func TestGoalChipHiddenWhenAchieved(t *testing.T) {
	m := &model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), ""), goal: &goalState{condition: "c", startedAt: time.Now(), achieved: true}}
	if status := m.modelStatus(); strings.Contains(status, "◎ goal") {
		t.Fatalf("an achieved goal should not show a chip, got:\n%s", status)
	}
}

func TestGoalPersistsAndResumesWithResetCounters(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())

	m := multiModel(1)
	m.agent.Load([]llm.Message{{Role: llm.RoleUser, Content: "work on it"}})
	m.goal = &goalState{
		condition: "finish the migration", startedAt: time.Now().Add(-5 * time.Minute),
		turns: 9, tokens: 12345, lastReason: "not yet",
	}
	if s := m.save("goalwork"); s != "" {
		t.Fatalf("save failed: %s", s)
	}

	m2 := multiModel(1)
	m2.evaluator = goal.Fake{}
	m2.resume("goalwork")

	if m2.goal == nil {
		t.Fatal("resume should restore the active goal")
	}
	if m2.goal.condition != "finish the migration" {
		t.Fatalf("restored condition = %q", m2.goal.condition)
	}
	if m2.goal.turns != 0 || m2.goal.tokens != 0 || m2.goal.lastReason != "" {
		t.Fatalf("resume should reset counters, got %+v", m2.goal)
	}
	if m2.goal.paused || m2.goal.achieved {
		t.Fatal("a resumed goal should be active")
	}
	// The active workspace mirrors the restored goal too.
	if w := m2.workspaceAt(m2.active); w == nil || w.goal != m2.goal {
		t.Fatal("the active workspace should mirror the restored goal")
	}
}

func TestGoalAchievedNotRestored(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())

	m := multiModel(1)
	m.agent.Load([]llm.Message{{Role: llm.RoleUser, Content: "did it"}})
	m.goal = &goalState{condition: "already done", startedAt: time.Now(), achieved: true, achievedAt: time.Now()}
	if s := m.save("achievedwork"); s != "" {
		t.Fatalf("save failed: %s", s)
	}

	m2 := multiModel(1)
	m2.evaluator = goal.Fake{}
	m2.resume("achievedwork")

	if m2.goal != nil {
		t.Fatal("an achieved goal must not be restored on resume")
	}
}

func TestGoalDebugInstrumentation(t *testing.T) {
	read := enableTUILog(t, "info")
	m := goalModel(goal.Fake{})
	m.handleCommand("/goal make it pass")
	m.goal.turns = 1
	m.applyGoalVerdict(goalEvalMsg{met: false, reason: "still failing"})
	m.applyGoalVerdict(goalEvalMsg{met: true, reason: "done"})
	out := read()
	for _, want := range []string{"[goal] set", "[goal] continue", "[goal] achieved"} {
		if !strings.Contains(out, want) {
			t.Fatalf("goal debug log missing %q:\n%s", want, out)
		}
	}
}

// TestGoalNotMetStartsNextTurnViaUpdate drives a not-met verdict through Update
// and asserts the next turn is actually wired up (events + cancel set), guarding
// against a return-order regression that would drop the run's state.
func TestGoalNotMetStartsNextTurnViaUpdate(t *testing.T) {
	t.Setenv("PLUTO_AUTOSAVE", "off")
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var tm tea.Model = model{
		agent: ag, md: newRenderer(80), input: newInput(80), busy: true,
		evaluator: goal.Fake{},
		goal:      &goalState{condition: "keep going", startedAt: time.Now(), turns: 1},
	}
	tm, cmd := tm.Update(goalEvalMsg{met: false, reason: "not yet"})
	got := tm.(model)
	if !got.busy {
		t.Fatal("a not-met verdict should keep working")
	}
	if got.events == nil || got.cancel == nil {
		t.Fatal("a not-met verdict should start the next turn (events + cancel wired)")
	}
	if cmd == nil {
		t.Fatal("a not-met verdict should return the next turn's listen command")
	}
}

// TestGoalDoneMsgKeepsWorking drives a full doneMsg through Update with an active
// goal and asserts the loop keeps working and counts the turn.
func TestGoalDoneMsgKeepsWorking(t *testing.T) {
	t.Setenv("PLUTO_AUTOSAVE", "off")
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var tm tea.Model = model{
		agent: ag, md: newRenderer(80), input: newInput(80), busy: true,
		evaluator: goal.Fake{Verdict: goal.Verdict{Met: false, Reason: "keep going"}},
		goal:      &goalState{condition: "c", startedAt: time.Now()},
	}
	tm, cmd := tm.Update(doneMsg{})
	got := tm.(model)
	if !got.busy {
		t.Fatal("an active goal should keep the loop busy after a turn")
	}
	if got.goal.turns != 1 {
		t.Fatalf("turns = %d, want 1", got.goal.turns)
	}
	if cmd == nil {
		t.Fatal("doneMsg with an active goal should return the evaluate command")
	}
}
