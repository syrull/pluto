package tui

import (
	"strings"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/tui/widgets"
)

// toolOutput is content retained from a tool result so a [Show] modal can
// display it in full: bash/find/read output that was truncated inline, or the
// content a write produced (which is never shown inline). path carries the file
// path for read/write so the modal can infer a language for highlighting.
type toolOutput struct {
	title string
	full  string
	path  string
}

func modalStyle() widgets.ModalStyle {
	return widgets.ModalStyle{Box: styleModalBox, Title: styleModalTitle, Hint: styleHint}
}

// appendToolResult renders a tool result inline and retains its full content
// when there is more to show than the inline preview, marking the block with a
// [Show] affordance.
func (m *model) appendToolResult(ev agent.Event) {
	text := renderToolResult(m.contentWidth(), ev.Tool, ev.Text)
	id := 0
	if o, ok := m.retainedOutput(ev); ok {
		m.outputs = append(m.outputs, o)
		id = len(m.outputs)
		text += "\n  " + m.showAffordance()
	}
	m.lines = append(m.lines, entry{text: text, outputID: id})
	m.pendingTool = ""
	m.pendingArgs = ""
}

// retainedOutput decides what full content, if any, to keep for the result ev.
func (m *model) retainedOutput(ev agent.Event) (toolOutput, bool) {
	if ev.Tool == "write" {
		content := writeContentArg(m.pendingArgs)
		if content == "" {
			return toolOutput{}, false
		}
		path := formatPathArg(m.pendingArgs)
		return toolOutput{title: "write: " + path, full: content, path: path}, true
	}
	full, ok := resultTruncated(ev.Tool, ev.Text)
	if !ok {
		return toolOutput{}, false
	}
	title := ev.Tool
	path := ""
	switch {
	case ev.Tool == "read":
		title = "read: " + formatReadArgs(m.pendingArgs)
		path = formatPathArg(m.pendingArgs)
	case bashCommandArg(m.pendingTool, m.pendingArgs) != "":
		title = ev.Tool + ": " + oneLine(bashCommandArg(m.pendingTool, m.pendingArgs))
	}
	return toolOutput{title: title, full: full, path: path}, true
}

// showAffordance renders the marker for a retained output: a clickable button
// when the mouse is captured, otherwise the keyboard hint that reaches it.
func (m *model) showAffordance() string {
	if m.mouse {
		return styleShowBtn.Render(" Show ▸ ")
	}
	return styleHint.Render("[ctrl+o] view")
}

func (m *model) openModal(o toolOutput) {
	m.modal = widgets.NewModal(o.title, o.full, modalStyle())
	if o.path != "" {
		m.modal.Highlight(func(s string) string { return highlightSource(s, o.path) })
	}
	m.modalPath = o.path
	m.modalIsFile = false
	if o.path != "" && editorAvailable() {
		m.modal.SetEditable(true)
	}
	m.modal.SetSize(m.width, m.height)
}

// lastOutput returns the most recently retained tool output, if any.
func (m model) lastOutput() (toolOutput, bool) {
	if len(m.outputs) == 0 {
		return toolOutput{}, false
	}
	return m.outputs[len(m.outputs)-1], true
}

func (m *model) resizeModal() {
	if m.modal != nil {
		m.modal.SetSize(m.width, m.height)
	}
}

// outputAtScreen maps a screen row to the retained output of the transcript
// block under it, if that block has one.
func (m model) outputAtScreen(y int) (toolOutput, bool) {
	y -= convContentTop
	if !m.ready || y < 0 || y >= m.vp.Height() {
		return toolOutput{}, false
	}
	return m.outputAtContentLine(m.vp.YOffset() + y)
}

func (m model) outputAtContentLine(target int) (toolOutput, bool) {
	if e, ok := m.entryAtContentLine(target); ok && e.outputID > 0 && e.outputID <= len(m.outputs) {
		return m.outputs[e.outputID-1], true
	}
	return toolOutput{}, false
}

// entryAtContentLine returns the transcript entry spanning content row target.
func (m model) entryAtContentLine(target int) (entry, bool) {
	cur := 0
	for _, e := range m.lines {
		h := strings.Count(e.text, "\n") + 1
		if target >= cur && target < cur+h {
			return e, true
		}
		cur += h
	}
	return entry{}, false
}
