package tui

import (
	"strings"

	"github.com/pluto/harness/internal/agent"
	"github.com/pluto/harness/internal/tui/widgets"
)

// toolOutput is content retained from a tool result so a [Show] modal can
// display it in full: bash/find/read output that was truncated inline, or the
// content a write produced (which is never shown inline).
type toolOutput struct {
	title string
	full  string
}

func modalStyle() widgets.ModalStyle {
	return widgets.ModalStyle{Box: styleModalBox, Title: styleModalTitle, Hint: styleHint}
}

// appendToolResult renders a tool result inline and retains its full content
// when there is more to show than the inline preview, marking the block with a
// [Show] affordance.
func (m *model) appendToolResult(ev agent.Event) {
	text := renderToolResult(m.width, ev.Tool, ev.Text)
	id := 0
	if o, ok := m.retainedOutput(ev); ok {
		m.outputs = append(m.outputs, o)
		id = len(m.outputs)
		text += "\n  " + styleShowBtn.Render(" Show ▸ ")
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
		return toolOutput{title: "write: " + formatPathArg(m.pendingArgs), full: content}, true
	}
	full, ok := resultTruncated(ev.Tool, ev.Text)
	if !ok {
		return toolOutput{}, false
	}
	title := ev.Tool
	if cmd := bashCommandArg(m.pendingTool, m.pendingArgs); cmd != "" {
		title = ev.Tool + ": " + oneLine(cmd)
	}
	return toolOutput{title: title, full: full}, true
}

func (m *model) openModal(o toolOutput) {
	m.modal = widgets.NewModal(o.title, o.full, modalStyle())
	m.modal.SetSize(m.width, m.height)
}

func (m *model) resizeModal() {
	if m.modal != nil {
		m.modal.SetSize(m.width, m.height)
	}
}

// outputAtScreen maps a screen row to the retained output of the transcript
// block under it, if that block has one.
func (m model) outputAtScreen(y int) (toolOutput, bool) {
	if !m.ready || y < 0 || y >= m.vp.Height {
		return toolOutput{}, false
	}
	return m.outputAtContentLine(m.vp.YOffset + y)
}

func (m model) outputAtContentLine(target int) (toolOutput, bool) {
	cur := 0
	for _, e := range m.lines {
		h := strings.Count(e.text, "\n") + 1
		if target >= cur && target < cur+h {
			if e.outputID > 0 && e.outputID <= len(m.outputs) {
				return m.outputs[e.outputID-1], true
			}
			return toolOutput{}, false
		}
		cur += h
	}
	return toolOutput{}, false
}
