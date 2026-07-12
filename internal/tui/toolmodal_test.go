package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestShowButtonForTruncatedResult(t *testing.T) {
	got := transcriptAfterTool(t, "bash", `{"command":"seq 20"}`, strings.Repeat("line\n", 20))
	if !strings.Contains(got, "Show") {
		t.Fatalf("truncated result should carry a Show button:\n%s", got)
	}
}

func TestNoShowButtonForShortResult(t *testing.T) {
	got := transcriptAfterTool(t, "bash", `{"command":"echo hi"}`, "a\nb\nc")
	if strings.Contains(got, "Show") {
		t.Fatalf("short result should not carry a Show button:\n%s", got)
	}
}

func TestWriteRetainsWrittenContent(t *testing.T) {
	var tm tea.Model = model{md: newRenderer(80)}
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
	var tm tea.Model = model{md: newRenderer(80)}
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

func transcriptAfterTool(t *testing.T, tool, args, result string) string {
	t.Helper()
	var tm tea.Model = model{md: newRenderer(80)}
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
