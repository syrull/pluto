package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/llm/anthropic"
	"github.com/syrull/pluto/internal/tool"
)

func TestFlushStreamCommitsThinkingAndText(t *testing.T) {
	m := &model{md: newRenderer(80)}
	m.streamThink = "step one\nstep two"
	m.streamText = "# Title\n\nBody text."

	m.flushStream()

	joined := m.transcript()
	if !strings.Contains(joined, "Thinking") {
		t.Fatalf("expected a thinking header in output:\n%s", joined)
	}
	if !strings.Contains(joined, "step one") || !strings.Contains(joined, "step two") {
		t.Fatalf("thinking content missing:\n%s", joined)
	}
	if !strings.Contains(joined, "Title") || !strings.Contains(joined, "Body text") {
		t.Fatalf("rendered markdown content missing:\n%s", joined)
	}

	if m.streamThink != "" || m.streamText != "" {
		t.Fatal("stream buffers not reset after flush")
	}

	before := len(m.lines)
	m.flushStream()
	if len(m.lines) != before {
		t.Fatal("empty flush should not append lines")
	}
}

func TestRenderMarkdownFallback(t *testing.T) {
	m := &model{md: nil}
	if got := m.renderMarkdown("hello\n\n"); got != "hello" {
		t.Fatalf("fallback render = %q, want %q", got, "hello")
	}
}

func TestUpdateStreamingDeltasNoCopyPanic(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}

	m, _ = m.Update(eventMsg{Kind: "text_delta", Text: "Hi "})
	m, _ = m.Update(eventMsg{Kind: "thinking_delta", Text: "hmm"})
	m, _ = m.Update(eventMsg{Kind: "text_delta", Text: "there"})

	got := m.(model)
	if got.streamText != "Hi there" {
		t.Fatalf("streamText = %q, want %q", got.streamText, "Hi there")
	}
	if got.streamThink != "hmm" {
		t.Fatalf("streamThink = %q, want %q", got.streamThink, "hmm")
	}
}

func TestRenderDiffLineEmpty(t *testing.T) {
	got := renderDiffLine(80, "")
	if got != "" {
		t.Fatalf("renderDiffLine(\"\") = %q, want \"\"", got)
	}
}

func TestRenderDiffLineAdded(t *testing.T) {
	ln := "+alpha"
	got := renderDiffLine(80, ln)
	if !strings.Contains(got, "alpha") {
		t.Fatalf("renderDiffLine(%q) = %q, content \"alpha\" missing", ln, got)
	}
	if !strings.Contains(got, "+") {
		t.Fatalf("renderDiffLine(%q) = %q, '+' operator missing", ln, got)
	}
}

func TestRenderDiffLineRemoved(t *testing.T) {
	ln := "-beta"
	got := renderDiffLine(80, ln)
	if !strings.Contains(got, "beta") {
		t.Fatalf("renderDiffLine(%q) = %q, content \"beta\" missing", ln, got)
	}
	if !strings.Contains(got, "-") {
		t.Fatalf("renderDiffLine(%q) = %q, '-' operator missing", ln, got)
	}
}

func TestRenderDiffLineContext(t *testing.T) {
	ln := " context"
	got := renderDiffLine(80, ln)
	if !strings.Contains(got, "context") {
		t.Fatalf("renderDiffLine(%q) = %q, content \"context\" missing", ln, got)
	}
}

func TestRenderDiffLineWraps(t *testing.T) {
	ln := "+" + strings.Repeat("x", 100)
	got := renderDiffLine(40, ln)
	for _, l := range strings.Split(got, "\n") {
		if w := lipgloss.Width(l); w > 40 {
			t.Fatalf("renderDiffLine line width = %d, want <= 40:\n%q", w, l)
		}
	}
}

func TestRenderWriteResultHeaderOnly(t *testing.T) {
	header := "wrote 5 bytes to /tmp/file (no change)"
	got := renderWriteResult(80, header)

	if !strings.Contains(got, "← write:") {
		t.Fatalf("renderWriteResult(%q) = %q, prefix \"← write:\" missing", header, got)
	}

	if !strings.Contains(got, "wrote 5 bytes") {
		t.Fatalf("renderWriteResult(%q) = %q, header text \"wrote 5 bytes\" missing", header, got)
	}
	if !strings.Contains(got, "/tmp/file") {
		t.Fatalf("renderWriteResult(%q) = %q, path \"/tmp/file\" missing", header, got)
	}
	if !strings.Contains(got, "no change") {
		t.Fatalf("renderWriteResult(%q) = %q, text \"no change\" missing", header, got)
	}
}

func TestRenderWriteResultWithDiff(t *testing.T) {
	result := "wrote 10 bytes to /tmp/test (+2 -1)\nalpha\n-beta\n+BETA"

	got := renderWriteResult(80, result)

	if !strings.Contains(got, "← write:") {
		t.Fatalf("renderWriteResult: prefix \"← write:\" missing from:\n%q", got)
	}
	if !strings.Contains(got, "wrote 10 bytes") {
		t.Fatalf("renderWriteResult: header text \"wrote 10 bytes\" missing from:\n%q", got)
	}
	if !strings.Contains(got, "/tmp/test") {
		t.Fatalf("renderWriteResult: path \"/tmp/test\" missing from:\n%q", got)
	}
	if !strings.Contains(got, "+2 -1") {
		t.Fatalf("renderWriteResult: diff stats \"+2 -1\" missing from:\n%q", got)
	}

	if !strings.Contains(got, "alpha") {
		t.Fatalf("renderWriteResult: context line \"alpha\" missing from:\n%q", got)
	}
	if !strings.Contains(got, "-beta") {
		t.Fatalf("renderWriteResult: removed line \"-beta\" missing from:\n%q", got)
	}
	if !strings.Contains(got, "+BETA") {
		t.Fatalf("renderWriteResult: added line \"+BETA\" missing from:\n%q", got)
	}

	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("renderWriteResult: expected multi-line output, got %d line(s):\n%q", len(lines), got)
	}
}

func TestWindowSizeFirstSetReady(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	got := m.(model)

	if !got.ready {
		t.Fatalf("expected ready=true after first WindowSizeMsg, got false")
	}
	// The viewport now sizes to the conversation pane's interior, not the full screen.
	if wantHeight := got.convBodyHeight(); got.vp.Height() != wantHeight {
		t.Fatalf("vp.Height = %d, want %d", got.vp.Height(), wantHeight)
	}
	if wantWidth := got.contentWidth(); got.vp.Width() != wantWidth {
		t.Fatalf("vp.Width = %d, want %d", got.vp.Width(), wantWidth)
	}
}

func TestWindowSizeFloorMinHeight(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 2})
	got := m.(model)

	if got.vp.Height() != 1 {
		t.Fatalf("vp.Height = %d, want 1 (floored)", got.vp.Height())
	}

	var m2 tea.Model = model{md: newRenderer(80)}
	m2, _ = m2.Update(tea.WindowSizeMsg{Width: 40, Height: 1})
	got2 := m2.(model)

	if got2.vp.Height() != 1 {
		t.Fatalf("vp.Height = %d, want 1 (floored)", got2.vp.Height())
	}
}

func TestWindowSizeResize(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	first := m.(model)
	if !first.ready {
		t.Fatalf("expected ready=true after first WindowSizeMsg")
	}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 50, Height: 10})
	second := m.(model)

	if !second.ready {
		t.Fatalf("ready should stay true after second WindowSizeMsg, got false")
	}
	if wantHeight := second.convBodyHeight(); second.vp.Height() != wantHeight {
		t.Fatalf("vp.Height = %d, want %d", second.vp.Height(), wantHeight)
	}
	if wantWidth := second.contentWidth(); second.vp.Width() != wantWidth {
		t.Fatalf("vp.Width = %d, want %d", second.vp.Width(), wantWidth)
	}
}

func TestAutoFollowAtBottom(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	got := m.(model)
	if !got.ready {
		t.Fatalf("model not ready")
	}

	for range 50 {
		m, _ = m.Update(eventMsg{Kind: "text", Text: "line of output"})
	}
	got = m.(model)

	if !got.vp.AtBottom() {
		t.Fatalf("expected vp.AtBottom()=true after many events at bottom, got false")
	}
}

func TestScrollKeysChangePosition(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	got := m.(model)

	for range 30 {
		m, _ = m.Update(eventMsg{Kind: "text", Text: "line " + fmt.Sprintf("%d", len(got.lines))})
	}
	got = m.(model)
	inputBefore := got.input.Value()

	// Apply each scroll key.
	for _, kp := range []tea.KeyPressMsg{
		{Code: tea.KeyPgUp},
		{Code: tea.KeyPgDown},
		{Code: 'u', Mod: tea.ModCtrl},
		{Code: 'd', Mod: tea.ModCtrl},
		{Code: tea.KeyUp},
		{Code: tea.KeyDown},
	} {
		m, _ = m.Update(kp)
		got = m.(model)
		if got.input.Value() != inputBefore {
			t.Fatalf("scroll key changed input buffer from %q to %q", inputBefore, got.input.Value())
		}
	}
	// vp.YOffset should have changed (we're not at bottom anymore).
	if got.vp.YOffset() == 0 {
		t.Fatalf("expected vp.YOffset > 0 after scrolling, got 0")
	}
}

func TestPrintableKeyRegression(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	got := m.(model)

	for range 30 {
		m, _ = m.Update(eventMsg{Kind: "text", Text: "filler"})
	}
	got = m.(model)
	offsetBefore := got.vp.YOffset()

	m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	got = m.(model)

	if got.input.Value() != "jk" {
		t.Fatalf("input = %q, want %q", got.input.Value(), "jk")
	}
	if got.vp.YOffset() != offsetBefore {
		t.Fatalf("vp.YOffset changed from %d to %d (j/k should not scroll)", offsetBefore, got.vp.YOffset())
	}
}

func TestPasteInsertsIntoInput(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})

	m, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m, _ = m.Update(tea.PasteMsg{Content: "pasted text"})
	got := m.(model)

	if got.input.Value() != "xpasted text" {
		t.Fatalf("input = %q, want %q", got.input.Value(), "xpasted text")
	}
}

func TestPasteIgnoredWhilePickerOpen(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})

	got := m.(model)
	got.picker = newThinkPicker([]llm.ThinkLevel{llm.ThinkNone}, llm.ThinkNone)
	m = got

	m, _ = m.Update(tea.PasteMsg{Content: "nope"})
	got = m.(model)

	if got.input.Value() != "" {
		t.Fatalf("input = %q, want empty (paste should be ignored while picker open)", got.input.Value())
	}
}

func TestNoYankWhenScrolledUp(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	got := m.(model)

	for range 30 {
		m, _ = m.Update(eventMsg{Kind: "text", Text: "line"})
	}
	got = m.(model)

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	got = m.(model)
	if got.vp.AtBottom() {
		t.Fatalf("expected AtBottom()=false after PageUp, got true")
	}

	m, _ = m.Update(eventMsg{Kind: "text", Text: "new line"})
	got = m.(model)
	if got.vp.AtBottom() {
		t.Fatalf("expected AtBottom()=false after new event while scrolled up, got true (yanked)")
	}
}

func TestAutoFollowWhenAtBottom(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	got := m.(model)

	for range 30 {
		m, _ = m.Update(eventMsg{Kind: "text", Text: "line"})
	}
	got = m.(model)

	if !got.vp.AtBottom() {
		t.Fatalf("expected AtBottom()=true after initial content, got false")
	}

	m, _ = m.Update(eventMsg{Kind: "text", Text: "more"})
	got = m.(model)
	if !got.vp.AtBottom() {
		t.Fatalf("expected AtBottom()=true after new event while at bottom, got false")
	}
}

func TestViewFooterBusy(t *testing.T) {
	in := newInput(80)
	in.SetValue("hello")
	m := &model{busy: true, input: in}
	view := m.View().Content
	if !strings.Contains(view, "working") {
		t.Fatalf("busy status should contain \"working\", got:\n%s", view)
	}
	if !strings.Contains(view, "hello") {
		t.Fatalf("busy view should keep the input live for steering, got:\n%s", view)
	}
}

func TestSteerWhileBusyQueuesMessage(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var m tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80), busy: true}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	for _, r := range "steer me" {
		m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := m.(model)

	if !got.busy {
		t.Fatalf("submitting while busy should stay busy, not start a fresh run")
	}
	if got.input.Value() != "" {
		t.Fatalf("input should reset after a steering submit, got %q", got.input.Value())
	}
	pending := ag.TakeSteering()
	if len(pending) != 1 || pending[0] != "steer me" {
		t.Fatalf("steering queue = %v, want [\"steer me\"]", pending)
	}
}

func TestCommandWhileBusyRejected(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var m tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80), busy: true}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	for _, r := range "/new" {
		m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if pending := ag.TakeSteering(); len(pending) != 0 {
		t.Fatalf("commands must not be queued as steering, got %v", pending)
	}
}

func TestDoneFoldsLeftoverSteering(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	ag.Steer("continue please")
	m := model{agent: ag, md: newRenderer(80), input: newInput(80), busy: true}

	updated, cmd := m.Update(doneMsg{})
	got := updated.(model)

	if !got.busy {
		t.Fatalf("busy should stay true to run the steered follow-up")
	}
	if cmd == nil {
		t.Fatalf("expected a command to run the steered follow-up")
	}
	if leftover := ag.TakeSteering(); len(leftover) != 0 {
		t.Fatalf("steering queue should be drained on done, got %v", leftover)
	}
}

func TestViewFooterNotReady(t *testing.T) {
	in := newInput(80)
	in.SetValue("hello")
	m := &model{
		md:    newRenderer(80),
		ready: false,
		input: in,
	}
	view := m.View().Content
	if !strings.Contains(view, "hello") {
		t.Fatalf("not-ready view should contain input \"hello\", got:\n%s", view)
	}
}

func TestHandleCommandNewClearsTranscript(t *testing.T) {
	ag := &agent.Agent{}
	m := &model{agent: ag, lines: []entry{{text: "a"}, {text: "b"}, {text: "c"}}, streamText: "x", streamThink: "y"}

	status, cmd := m.handleCommand("/new")

	if cmd != nil {
		t.Fatalf("handleCommand(/new) should return nil cmd, got %v", cmd)
	}
	if status != "" {
		t.Fatalf("handleCommand(/new) should not push to transcript, got %q", status)
	}
	if !strings.Contains(m.notice, "new conversation") {
		t.Fatalf("notice = %q, should contain 'new conversation'", m.notice)
	}
	if len(m.lines) != 0 {
		t.Fatalf("lines not cleared, got %d lines", len(m.lines))
	}
	if m.streamText != "" || m.streamThink != "" {
		t.Fatalf("stream buffers not cleared")
	}
}

func TestModelStatusPersistent(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	providerName := ag.ProviderName()
	m := &model{agent: ag, busy: false, ready: true}

	status := m.modelStatus()
	if !strings.Contains(status, providerName) {
		t.Fatalf("modelStatus should contain provider name %q, got:\n%s", providerName, status)
	}

	m.busy = true
	status = m.modelStatus()
	if !strings.Contains(status, providerName) {
		t.Fatalf("modelStatus (busy) should contain provider name, got:\n%s", status)
	}

	m.ready = false
	status = m.modelStatus()
	if !strings.Contains(status, providerName) {
		t.Fatalf("modelStatus (not ready) should contain provider name, got:\n%s", status)
	}
}

func TestNotificationsWidgetAboveStatusLine(t *testing.T) {
	m := &model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), ""), width: 80, input: newInput(80)}
	m.notice = "✓ drag to select text; ctrl+t to re-enable"

	if got := m.notifications(); !strings.Contains(got, "drag to select") {
		t.Fatalf("notifications widget should carry the notice, got %q", got)
	}
	if got := m.modelStatus(); strings.Contains(got, "drag to select") {
		t.Fatalf("status line should no longer carry the notice, got %q", got)
	}

	c := m.content()
	notif := strings.Index(c, "drag to select")
	status := strings.Index(c, m.agent.ProviderName())
	if notif < 0 || status < 0 || notif > status {
		t.Fatalf("notice should render above the status line (notif=%d status=%d):\n%s", notif, status, c)
	}
}

func TestNotificationsBlankWithoutNotice(t *testing.T) {
	m := &model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), ""), width: 80}
	if got := m.notifications(); got != "" {
		t.Fatalf("notifications widget should be blank without a notice, got %q", got)
	}
}

func TestModelStatusNoAgent(t *testing.T) {
	m := &model{agent: nil}
	status := m.modelStatus()
	if !strings.Contains(status, "no provider") {
		t.Fatalf("modelStatus(nil agent) should contain 'no provider', got:\n%s", status)
	}
}

func TestModelStatusContextWindow(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	ap, err := anthropic.New("claude-sonnet-5")
	if err != nil {
		t.Fatalf("anthropic.New: %v", err)
	}
	m := &model{agent: agent.New(ap, tool.NewRegistry(), "")}

	status := m.modelStatus()
	if !strings.Contains(status, "context: 0% / 1M") {
		t.Fatalf("modelStatus should show context 0%% / 1M before any turn, got:\n%s", status)
	}
}

func TestFormatTokens(t *testing.T) {
	cases := map[int]string{
		0:         "0",
		999:       "999",
		1_000:     "1K",
		200_000:   "200K",
		1_500:     "1.5K",
		1_000_000: "1M",
		1_500_000: "1.5M",
	}
	for n, want := range cases {
		if got := formatTokens(n); got != want {
			t.Fatalf("formatTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestModelStatusStubNoContext(t *testing.T) {
	m := &model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), "")}
	if status := m.modelStatus(); strings.Contains(status, "context:") {
		t.Fatalf("stub provider has no context window; status should omit it, got:\n%s", status)
	}
}

func TestModelStatusShowsCwd(t *testing.T) {
	m := &model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), "")}
	status := m.modelStatus()
	want := shortCwd()
	if want == "" {
		t.Skip("cwd unavailable")
	}
	if !strings.Contains(status, want) {
		t.Fatalf("modelStatus should contain cwd %q, got:\n%s", want, status)
	}
}

func TestModelStatusShowsBranch(t *testing.T) {
	m := &model{
		agent: agent.New(llm.Stub{}, tool.NewRegistry(), ""),
		git:   gitInfo{isRepo: true, branch: "feature-x", hasUpstream: true, ahead: 2, behind: 1},
	}
	status := m.modelStatus()
	if !strings.Contains(status, "feature-x") {
		t.Fatalf("modelStatus should show the active branch, got:\n%s", status)
	}
	if !strings.Contains(status, "↑2") || !strings.Contains(status, "↓1") {
		t.Fatalf("modelStatus should carry ahead/behind markers, got:\n%s", status)
	}
}

func TestModelStatusDetachedHead(t *testing.T) {
	m := &model{
		agent: agent.New(llm.Stub{}, tool.NewRegistry(), ""),
		git:   gitInfo{isRepo: true},
	}
	if status := m.modelStatus(); !strings.Contains(status, "(detached)") {
		t.Fatalf("detached HEAD should read as (detached), got:\n%s", status)
	}
}

func TestModelStatusNoBranchOutsideRepo(t *testing.T) {
	m := &model{
		agent: agent.New(llm.Stub{}, tool.NewRegistry(), ""),
		git:   gitInfo{isRepo: false, branch: "should-not-show"},
	}
	if status := m.modelStatus(); strings.Contains(status, "should-not-show") || strings.Contains(status, "⎇") {
		t.Fatalf("non-repo status should omit the branch, got:\n%s", status)
	}
}

func TestModelStatusCwdBaseOnNarrowWidth(t *testing.T) {
	cwd := shortCwd()
	if cwd == "" || filepath.Base(cwd) == cwd {
		t.Skip("cwd has no distinct base to abbreviate")
	}
	m := &model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), ""), width: 20}
	status := m.modelStatus()
	base := filepath.Base(cwd)
	if !strings.Contains(status, " · "+base) {
		t.Fatalf("narrow width should abbreviate cwd to base %q, got:\n%s", base, status)
	}
	if strings.Contains(status, cwd) {
		t.Fatalf("narrow width should not show full cwd %q, got:\n%s", cwd, status)
	}
}

func TestShortCwdHomeAbbreviation(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("home dir unavailable")
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	got := shortCwd()
	if strings.HasPrefix(dir, home) && !strings.HasPrefix(got, "~") {
		t.Fatalf("shortCwd under home should start with ~, got %q (cwd %q, home %q)", got, dir, home)
	}
}

func TestHandleCommandThinkUnsupported(t *testing.T) {
	m := &model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), "")}
	status, cmd := m.handleCommand("/think")

	if cmd != nil {
		t.Fatalf("handleCommand(/think unsupported) should return nil cmd")
	}
	if !strings.Contains(status, "does not support") {
		t.Fatalf("status should report unsupported, got: %s", status)
	}
}

func TestHandleCommandThinkLevelsAnthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	ap, err := anthropic.New(anthropic.DefaultModel)
	if err != nil {
		t.Fatalf("anthropic.New: %v", err)
	}
	m := &model{agent: agent.New(ap, tool.NewRegistry(), "")}

	// Bare /think opens the level picker.
	status, _ := m.handleCommand("/think")
	if status != "" || m.picker == nil || m.pickerKind != pickerThink {
		t.Fatalf("bare /think should open the think picker, got status %q picker %v kind %v", status, m.picker, m.pickerKind)
	}
	m.picker = nil
	m.pickerKind = pickerNone

	// /think off disables.
	_, _ = m.handleCommand("/think off")
	status = m.modelStatus()
	if !strings.Contains(status, "thinking: off") {
		t.Fatalf("after /think off, modelStatus should show off, got: %s", status)
	}

	// /think on enables max level; the picker preselects the active level.
	_, _ = m.handleCommand("/think on")
	th, _ := m.agent.Thinker()
	active := th.ThinkLevel()
	_, _ = m.handleCommand("/think")
	if m.picker == nil || m.picker.Selected() != string(active) {
		t.Fatalf("bare /think should preselect active level %q, got picker %v", active, m.picker)
	}
	m.picker = nil
	m.pickerKind = pickerNone

	// /think with invalid level rejects.
	status, _ = m.handleCommand("/think badlevel")
	if !strings.Contains(status, "usage:") {
		t.Fatalf("invalid /think should show usage, got: %s", status)
	}

	// Valid explicit level surfaces a transient notice.
	status, _ = m.handleCommand("/think high")
	if status != "" {
		t.Fatalf("/think high should not push to transcript, got: %s", status)
	}
	if !strings.Contains(m.notice, "high") {
		t.Fatalf("after /think high, notice should reflect high level, got: %s", m.notice)
	}
}

func TestViewFooterReady(t *testing.T) {
	in := newInput(80)
	in.SetValue("test")
	m := &model{
		md:    newRenderer(80),
		ready: true,
		vp:    viewport.New(viewport.WithWidth(40), viewport.WithHeight(6)),
		input: in,
	}
	m.syncViewport()

	view := m.View().Content
	// Should use vp.View() output.
	if !strings.Contains(view, "\n") {
		t.Fatalf("ready view should be multi-line (viewport + footer), got:\n%s", view)
	}
	if !strings.Contains(view, "test") {
		t.Fatalf("ready view should contain input, got:\n%s", view)
	}
}

// TestProgramRunsHeadlessAndQuits drives a real v2 *tea.Program headless to
// prove the v1→v2 migration works end-to-end: the initial WindowSizeMsg sets
// up the viewport (ready=true), View() renders a tea.View with AltScreen and
// cell-motion mouse enabled, and a ctrl+c key press routes through Update to
// tea.Quit for a clean shutdown — not a context-kill timeout.
func TestProgramRunsHeadlessAndQuits(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	m := model{agent: ag, md: newRenderer(80), input: newInput(80), mouse: true}

	var in, out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	p := tea.NewProgram(m,
		tea.WithInput(&in),
		tea.WithOutput(&out),
		tea.WithoutSignalHandler(),
		tea.WithWindowSize(80, 24),
		tea.WithContext(ctx),
	)

	go func() {
		// Let the event loop process the initial WindowSizeMsg and render a frame.
		time.Sleep(100 * time.Millisecond)
		p.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	}()

	final, err := p.Run()
	if err != nil {
		t.Fatalf("program.Run error: %v\nrendered output:\n%s", err, out.String())
	}

	got, ok := final.(model)
	if !ok {
		t.Fatalf("final model is %T, want model", final)
	}
	if !got.ready {
		t.Fatal("model not ready: initial WindowSizeMsg was not processed")
	}
	if got.width != 80 || got.height != 24 {
		t.Fatalf("model dims = %dx%d, want 80x24", got.width, got.height)
	}
	v := got.View()
	if !v.AltScreen {
		t.Error("View().AltScreen should be true")
	}
	if v.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("View().MouseMode = %v, want MouseModeCellMotion", v.MouseMode)
	}
	if strings.TrimSpace(v.Content) == "" {
		t.Errorf("View().Content empty; rendered output was:\n%s", out.String())
	}
}

func TestMouseModeDefaultsOffAndOptsIn(t *testing.T) {
	mouseModeFor := func(mouse bool) tea.MouseMode {
		var tm tea.Model = model{md: newRenderer(80), input: newInput(80), mouse: mouse}
		tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		return tm.(model).View().MouseMode
	}
	if got := mouseModeFor(false); got != tea.MouseModeNone {
		t.Fatalf("default View().MouseMode = %v, want MouseModeNone so selection works", got)
	}
	if got := mouseModeFor(true); got != tea.MouseModeCellMotion {
		t.Fatalf("mouse-enabled View().MouseMode = %v, want MouseModeCellMotion", got)
	}
}

func TestCtrlTTogglesMouseMode(t *testing.T) {
	var tm tea.Model = model{md: newRenderer(80), input: newInput(80), mouse: false}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	press := tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	tm, _ = tm.Update(press)
	if got := tm.(model).View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("after ctrl+t, MouseMode = %v, want MouseModeCellMotion", got)
	}
	tm, _ = tm.Update(press)
	if got := tm.(model).View().MouseMode; got != tea.MouseModeNone {
		t.Fatalf("after second ctrl+t, MouseMode = %v, want MouseModeNone", got)
	}
}

func TestMouseEnabledEnv(t *testing.T) {
	cases := map[string]bool{"": false, "off": false, "0": false, "false": false, "NO": false, "on": true, "1": true, "true": true, "YES": true}
	for val, want := range cases {
		t.Setenv("PLUTO_MOUSE", val)
		if got := mouseEnabled(); got != want {
			t.Errorf("mouseEnabled() with PLUTO_MOUSE=%q = %v, want %v", val, got, want)
		}
	}
}
