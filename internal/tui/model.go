package tui

import (
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/pluto/harness/internal/agent"
	"github.com/pluto/harness/internal/tui/widgets"
)

type eventMsg agent.Event

type doneMsg struct{}

type loginDoneMsg struct{ err error }

// LoginHook wires the /login command.
type LoginHook struct {
	Command func() *exec.Cmd
	After   func(procErr error) (status string, err error)
}

// entry is one committed transcript block, optionally tied to a retained tool
// output (outputID > 0) so a click can reopen its full text in a modal.
type entry struct {
	text     string
	outputID int
}

type model struct {
	agent  *agent.Agent
	login  *LoginHook
	lines  []entry        // committed transcript blocks
	input  textarea.Model // current input buffer, multi-line with word wrap
	busy   bool           // agent running; input disabled
	events chan eventMsg  // agent → UI stream for the active Run
	width  int
	height int

	vp    viewport.Model
	ready bool

	// Streaming accumulators: strings because Bubbletea copies the model by value and Builder panics when copied.
	streamText  string
	streamThink string

	md *glamour.TermRenderer // markdown renderer, rebuilt on resize

	picker     *widgets.ListPicker
	pickerKind pickerKind

	// outputs retains full tool results; pendingTool/pendingArgs carry the
	// in-flight tool call so its result can be titled and its content retained;
	// modal is the open full-output viewer, if any.
	outputs     []toolOutput
	pendingTool string
	pendingArgs string
	modal       *widgets.Modal
}

// pickerKind identifies which setting an open ListPicker edits.
type pickerKind int

const (
	pickerNone pickerKind = iota
	pickerModel
	pickerThink
)

// inputHeight is the fixed number of visible rows in the input box; longer
// input scrolls within it rather than growing the box.
const inputHeight = 3

const footerHeight = 2 + inputHeight

// newInput builds a word-wrapping, multi-line input box.
func newInput(width int) textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.Prompt = ""
	ta.Placeholder = ""
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return stylePrompt.Render("› ")
		}
		return "  "
	})
	ta.SetHeight(inputHeight)
	ta.SetWidth(width)
	ta.Focus()
	return ta
}

// New builds the Bubbletea program.
func New(a *agent.Agent, login *LoginHook) *tea.Program {
	m := model{agent: a, login: login, md: newRenderer(80), input: newInput(80)}
	return tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
}

func (m model) Init() tea.Cmd { return nil }

func scrollKeymap() viewport.KeyMap {
	return viewport.KeyMap{
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		PageDown:     key.NewBinding(key.WithKeys("pgdown")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
		Up:           key.NewBinding(key.WithKeys("up")),
		Down:         key.NewBinding(key.WithKeys("down")),
	}
}

// pushText appends a plain, non-clickable transcript block.
func (m *model) pushText(s string) {
	m.lines = append(m.lines, entry{text: s})
}

func (m model) transcript() string {
	parts := make([]string, len(m.lines))
	for i, e := range m.lines {
		parts[i] = e.text
	}
	body := strings.Join(parts, "\n")
	if live := m.liveRegion(); live != "" {
		body += "\n" + live
	}
	return body
}

func (m *model) syncViewport() {
	if !m.ready {
		return
	}
	atBottom := m.vp.AtBottom()
	m.vp.SetContent(m.transcript())
	if atBottom {
		m.vp.GotoBottom()
	}
}
