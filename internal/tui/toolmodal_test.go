package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestShowButtonForTruncatedResultWithMouse(t *testing.T) {
	got := transcriptAfterTool(t, true, "bash", `{"command":"seq 20"}`, strings.Repeat("line\n", 20))
	if !strings.Contains(got, "Show") {
		t.Fatalf("with mouse on, a truncated result should carry a clickable Show button:\n%s", got)
	}
}

func TestShowHintForTruncatedResultWithoutMouse(t *testing.T) {
	got := transcriptAfterTool(t, false, "bash", `{"command":"seq 20"}`, strings.Repeat("line\n", 20))
	if !strings.Contains(got, "[ctrl+o]") {
		t.Fatalf("without mouse, a truncated result should carry the ctrl+o hint:\n%s", got)
	}
	if strings.Contains(got, "Show ▸") {
		t.Fatalf("without mouse, no clickable Show button should be shown:\n%s", got)
	}
}

func TestNoShowAffordanceForShortResult(t *testing.T) {
	got := transcriptAfterTool(t, true, "bash", `{"command":"echo hi"}`, "a\nb\nc")
	if strings.Contains(got, "Show") || strings.Contains(got, "[ctrl+o]") {
		t.Fatalf("short result should carry no view affordance:\n%s", got)
	}
}

func TestWriteRetainsWrittenContent(t *testing.T) {
	var tm tea.Model = model{md: newRenderer(80), mouse: true}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	tm, _ = tm.Update(eventMsg{Kind: "tool_call", Tool: "write", Text: `{"path":"foo.txt","content":"hello\nworld"}`})
	tm, _ = tm.Update(eventMsg{Kind: "tool_result", Tool: "write", Text: "wrote 11 bytes to foo.txt (+2 -0)"})

	got := tm.(model)
	if len(got.outputs) != 1 {
		t.Fatalf("write should retain its content, got %d outputs", len(got.outputs))
	}
	if got.outputs[0].full != "hello\nworld" {
		t.Fatalf("retained write content = %q, want %q", got.outputs[0].full, "hello\nworld")
	}
	if !strings.Contains(got.outputs[0].title, "foo.txt") {
		t.Fatalf("write modal title = %q, want it to contain the path", got.outputs[0].title)
	}
	if !strings.Contains(got.transcript(), "Show") {
		t.Fatal("write result should carry a Show button so its content is viewable")
	}
}

func TestClickOpensModalAndEscCloses(t *testing.T) {
	var tm tea.Model = model{md: newRenderer(80)}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	tm, _ = tm.Update(eventMsg{Kind: "tool_call", Tool: "bash", Text: `{"command":"ls -la"}`})
	tm, _ = tm.Update(eventMsg{Kind: "tool_result", Tool: "bash", Text: strings.Repeat("out\n", 30)})

	got := tm.(model)
	if len(got.outputs) != 1 {
		t.Fatalf("expected 1 retained output, got %d", len(got.outputs))
	}
	if got.vp.YOffset() != 0 {
		t.Fatalf("precondition: expected YOffset 0, got %d", got.vp.YOffset())
	}
	if !strings.Contains(got.outputs[0].title, "ls -la") {
		t.Fatalf("retained title = %q, want it to contain the command", got.outputs[0].title)
	}
	if !strings.Contains(got.outputs[0].full, "out") {
		t.Fatal("retained output should hold the full text")
	}

	y := clickableRow(got)
	if y < 0 {
		t.Fatal("no clickable output block found")
	}

	tm, _ = tm.Update(tea.MouseClickMsg{Button: tea.MouseLeft, Y: y})
	got = tm.(model)
	if got.modal == nil {
		t.Fatal("expected modal to open on click over the tool result")
	}

	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if tm.(model).modal != nil {
		t.Fatal("expected modal to close on esc")
	}
}

func TestCtrlOOpensLatestOutputModal(t *testing.T) {
	var tm tea.Model = model{md: newRenderer(80)}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	tm, _ = tm.Update(eventMsg{Kind: "tool_call", Tool: "bash", Text: `{"command":"seq 30"}`})
	tm, _ = tm.Update(eventMsg{Kind: "tool_result", Tool: "bash", Text: strings.Repeat("out\n", 30)})

	if tm.(model).modal != nil {
		t.Fatal("precondition: modal should be closed")
	}
	tm, _ = tm.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if tm.(model).modal == nil {
		t.Fatal("ctrl+o should open the latest tool output modal without a mouse")
	}
}

func TestCtrlONoopWithoutOutput(t *testing.T) {
	var tm tea.Model = model{md: newRenderer(80)}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	tm, _ = tm.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if tm.(model).modal != nil {
		t.Fatal("ctrl+o with no retained output should not open a modal")
	}
}

func TestModalCopyKeyCopiesContent(t *testing.T) {
	var tm tea.Model = model{md: newRenderer(80)}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	tm, _ = tm.Update(eventMsg{Kind: "tool_call", Tool: "bash", Text: `{"command":"seq 30"}`})
	tm, _ = tm.Update(eventMsg{Kind: "tool_result", Tool: "bash", Text: strings.Repeat("out\n", 30)})

	got := tm.(model)
	got.openModal(got.outputs[0])
	tm = got

	tm, cmd := tm.Update(tea.KeyPressMsg{Code: 'c'})
	if cmd == nil {
		t.Fatal("pressing c in a modal should return a SetClipboard command")
	}
	m := tm.(model)
	if m.modal == nil {
		t.Fatal("copying should not close the modal")
	}
	if !strings.Contains(m.modal.Content(), "out") {
		t.Fatalf("modal content to copy = %q, want the full output", m.modal.Content())
	}
	if !strings.Contains(m.modal.View(), "copied") {
		t.Fatal("modal should show a copied confirmation after c")
	}
}

func TestCtrlYCopiesLatestCodeBlock(t *testing.T) {
	m := model{md: newRenderer(80), width: 80}
	m.streamText = "```go\nx := 1\n```\n```sh\necho hi\n```"
	m.flushStream()

	var tm tea.Model = m
	tm, cmd := tm.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+y should return a SetClipboard command when a code block exists")
	}
	if got := tm.(model).notice; !strings.Contains(got, "copied") {
		t.Fatalf("ctrl+y notice = %q, want a copy confirmation", got)
	}
}

func TestCtrlYNoopWithoutCodeBlock(t *testing.T) {
	var tm tea.Model = model{md: newRenderer(80)}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	tm, cmd := tm.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("ctrl+y with no retained code block should be a no-op")
	}
	if tm.(model).notice != "" {
		t.Fatal("ctrl+y with no code block should not set a notice")
	}
}

func TestClickCopiesCodeBlock(t *testing.T) {
	var tm tea.Model = model{md: newRenderer(80)}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	gm := tm.(model)
	gm.streamText = "```go\nx := 1\n```"
	gm.flushStream()
	gm.syncViewport()
	tm = gm

	y := copyableRow(gm)
	if y < 0 {
		t.Fatal("no copyable code-block row found")
	}
	tm, cmd := tm.Update(tea.MouseClickMsg{Button: tea.MouseLeft, Y: y})
	if cmd == nil {
		t.Fatal("clicking a Copy affordance should return a SetClipboard command")
	}
	if !strings.Contains(tm.(model).notice, "copied") {
		t.Fatal("clicking a Copy affordance should set a copied notice")
	}
}

func TestModalWheelScrolls(t *testing.T) {
	var tm tea.Model = model{md: newRenderer(80)}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	tm, _ = tm.Update(eventMsg{Kind: "tool_call", Tool: "bash", Text: `{"command":"seq 100"}`})
	tm, _ = tm.Update(eventMsg{Kind: "tool_result", Tool: "bash", Text: strings.Repeat("line\n", 100)})

	got := tm.(model)
	got.openModal(got.outputs[0])
	if !got.modal.AtTop() {
		t.Fatal("modal should start scrolled to the top")
	}

	var m tea.Model = got
	m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.(model).modal.AtTop() {
		t.Fatal("wheel down should scroll the modal off the top")
	}
}

func TestReadResultShowsSummaryNotContent(t *testing.T) {
	var tm tea.Model = model{md: newRenderer(80), mouse: true}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	tm, _ = tm.Update(eventMsg{Kind: "tool_call", Tool: "read", Text: `{"path":"foo.go"}`})
	tm, _ = tm.Update(eventMsg{Kind: "tool_result", Tool: "read", Text: "1\tpackage foo\n2\t\n3\tfunc Bar() {}"})

	got := tm.(model)
	tr := got.transcript()
	if strings.Contains(tr, "package foo") {
		t.Fatalf("read result should not dump content inline:\n%s", tr)
	}
	if !strings.Contains(tr, "3 line(s)") {
		t.Fatalf("read result should show a line-count summary:\n%s", tr)
	}
	if !strings.Contains(tr, "Show") {
		t.Fatalf("read result should carry a Show button so its content is viewable:\n%s", tr)
	}
	if len(got.outputs) != 1 || !strings.Contains(got.outputs[0].full, "package foo") {
		t.Fatalf("read should retain its full content for the modal, got %+v", got.outputs)
	}
	if !strings.Contains(got.outputs[0].title, "foo.go") {
		t.Fatalf("read modal title = %q, want it to contain the path", got.outputs[0].title)
	}
}

func transcriptAfterTool(t *testing.T, mouse bool, tool, args, result string) string {
	t.Helper()
	var tm tea.Model = model{md: newRenderer(80), mouse: mouse}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	tm, _ = tm.Update(eventMsg{Kind: "tool_call", Tool: tool, Text: args})
	tm, _ = tm.Update(eventMsg{Kind: "tool_result", Tool: tool, Text: result})
	return tm.(model).transcript()
}

// clickableRow returns the first transcript content row carrying a retained
// output, assuming the viewport is at the top.
func clickableRow(m model) int {
	cur := 0
	for _, e := range m.lines {
		if e.outputID > 0 {
			return cur
		}
		cur += strings.Count(e.text, "\n") + 1
	}
	return -1
}

// copyableRow returns the first transcript content row carrying a retained code
// block, assuming the viewport is at the top.
func copyableRow(m model) int {
	cur := 0
	for _, e := range m.lines {
		if e.copyID > 0 {
			return cur
		}
		cur += strings.Count(e.text, "\n") + 1
	}
	return -1
}
