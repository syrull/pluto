package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pluto/harness/internal/agent"
	"github.com/pluto/harness/internal/llm"
	"github.com/pluto/harness/internal/llm/anthropic"
	"github.com/pluto/harness/internal/tool"
)

func TestFlushStreamCommitsThinkingAndText(t *testing.T) {
	m := &model{md: newRenderer(80)}
	m.streamThink = "step one\nstep two"
	m.streamText = "# Title\n\nBody text."

	m.flushStream()

	joined := strings.Join(m.lines, "\n")
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

func TestRenderToolCallWrapsLongCommand(t *testing.T) {
	args := `{"command":"` + strings.Repeat("echo hello world; ", 10) + `"}`
	got := renderToolCall(40, "bash", args)
	for _, l := range strings.Split(got, "\n") {
		if w := lipgloss.Width(l); w > 40 {
			t.Fatalf("renderToolCall line width = %d, want <= 40:\n%q", w, l)
		}
	}
}

func TestRenderToolResultWrapsLongLine(t *testing.T) {
	got := renderToolResult(40, "bash", strings.Repeat("a", 200))
	for _, l := range strings.Split(got, "\n") {
		if w := lipgloss.Width(l); w > 40 {
			t.Fatalf("renderToolResult line width = %d, want <= 40:\n%q", w, l)
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
	wantHeight := 6 - footerHeight
	if got.vp.Height != wantHeight {
		t.Fatalf("vp.Height = %d, want %d", got.vp.Height, wantHeight)
	}
	if got.vp.Width != 40 {
		t.Fatalf("vp.Width = %d, want 40", got.vp.Width)
	}
}

func TestWindowSizeFloorMinHeight(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80)}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 2})
	got := m.(model)

	if got.vp.Height != 1 {
		t.Fatalf("vp.Height = %d, want 1 (floored)", got.vp.Height)
	}

	var m2 tea.Model = model{md: newRenderer(80)}
	m2, _ = m2.Update(tea.WindowSizeMsg{Width: 40, Height: 1})
	got2 := m2.(model)

	if got2.vp.Height != 1 {
		t.Fatalf("vp.Height = %d, want 1 (floored)", got2.vp.Height)
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
	wantHeight := 10 - footerHeight
	if second.vp.Height != wantHeight {
		t.Fatalf("vp.Height = %d, want %d", second.vp.Height, wantHeight)
	}
	if second.vp.Width != 50 {
		t.Fatalf("vp.Width = %d, want 50", second.vp.Width)
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
	for _, msgType := range []tea.KeyType{tea.KeyPgUp, tea.KeyPgDown, tea.KeyCtrlU, tea.KeyCtrlD, tea.KeyUp, tea.KeyDown} {
		m, _ = m.Update(tea.KeyMsg{Type: msgType})
		got = m.(model)
		if got.input.Value() != inputBefore {
			t.Fatalf("scroll key changed input buffer from %q to %q", inputBefore, got.input.Value())
		}
	}
	// vp.YOffset should have changed (we're not at bottom anymore).
	if got.vp.YOffset == 0 {
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
	offsetBefore := got.vp.YOffset

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	got = m.(model)

	if got.input.Value() != "jk" {
		t.Fatalf("input = %q, want %q", got.input.Value(), "jk")
	}
	if got.vp.YOffset != offsetBefore {
		t.Fatalf("vp.YOffset changed from %d to %d (j/k should not scroll)", offsetBefore, got.vp.YOffset)
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

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
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
	view := m.View()
	if !strings.Contains(view, "working") {
		t.Fatalf("busy footer should contain \"working\", got:\n%s", view)
	}
	if strings.Contains(view, "hello") {
		t.Fatalf("busy footer should not show input, got:\n%s", view)
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
	view := m.View()
	if !strings.Contains(view, "hello") {
		t.Fatalf("not-ready view should contain input \"hello\", got:\n%s", view)
	}
}

func TestHandleCommandNewClearsTranscript(t *testing.T) {
	ag := &agent.Agent{}
	m := &model{agent: ag, lines: []string{"a", "b", "c"}, streamText: "x", streamThink: "y"}

	status, cmd := m.handleCommand("/new")

	if cmd != nil {
		t.Fatalf("handleCommand(/new) should return nil cmd, got %v", cmd)
	}
	if !strings.Contains(status, "new conversation") {
		t.Fatalf("status = %q, should contain 'new conversation'", status)
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

func TestModelStatusNoAgent(t *testing.T) {
	m := &model{agent: nil}
	status := m.modelStatus()
	if !strings.Contains(status, "no provider") {
		t.Fatalf("modelStatus(nil agent) should contain 'no provider', got:\n%s", status)
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

	// Bare /think cycles levels.
	status, _ := m.handleCommand("/think")
	if !strings.Contains(status, "extended thinking") {
		t.Fatalf("bare /think should return thinking status, got: %s", status)
	}

	// /think off disables.
	_, _ = m.handleCommand("/think off")
	status = m.modelStatus()
	if !strings.Contains(status, "thinking: off") {
		t.Fatalf("after /think off, modelStatus should show off, got: %s", status)
	}

	// /think on enables max level.
	_, _ = m.handleCommand("/think on")
	status, _ = m.handleCommand("/think")
	if !strings.Contains(status, "extended thinking") {
		t.Fatalf("after /think on, /think should show thinking, got: %s", status)
	}

	// /think with invalid level rejects.
	status, _ = m.handleCommand("/think badlevel")
	if !strings.Contains(status, "usage:") {
		t.Fatalf("invalid /think should show usage, got: %s", status)
	}

	// Valid explicit level.
	status, _ = m.handleCommand("/think high")
	if !strings.Contains(status, "high") {
		t.Fatalf("after /think high, should reflect high level, got: %s", status)
	}
}

func TestViewFooterReady(t *testing.T) {
	in := newInput(80)
	in.SetValue("test")
	m := &model{
		md:    newRenderer(80),
		ready: true,
		vp:    viewport.Model{Width: 40, Height: 6},
		input: in,
	}
	m.syncViewport()

	view := m.View()
	// Should use vp.View() output.
	if !strings.Contains(view, "\n") {
		t.Fatalf("ready view should be multi-line (viewport + footer), got:\n%s", view)
	}
	if !strings.Contains(view, "test") {
		t.Fatalf("ready view should contain input, got:\n%s", view)
	}
}
