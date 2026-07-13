package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/tui/widgets"
)

type eventMsg agent.Event

type doneMsg struct{}

type loginDoneMsg struct {
	status string
	err    error
}

// LoginHook wires the /login command to the OAuth flow.
//
// Authorize builds the browser authorization URL and returns a handle. Wait
// blocks (on a local callback server) until the redirect arrives, then
// exchanges the code and re-authenticates the live provider, returning a status
// line. Complete is the manual fallback for when the browser is on another
// machine: the user pastes the redirect URL or code.
type LoginHook struct {
	Authorize func() (url string, flow any, err error)
	Wait      func(flow any) (status string, err error)
	Complete  func(flow any, pastedInput string) (status string, err error)
}

// entry is one committed transcript block. An entry may be tied to a retained
// tool output (outputID > 0) so a click or ctrl+o reopens its full text in a
// modal, or to a retained code block (copyID > 0) so a click copies it.
type entry struct {
	text     string
	outputID int
	copyID   int
}

type model struct {
	agent     *agent.Agent
	login     *LoginHook
	loginFlow any            // pending OAuth flow awaiting manual code entry (paste fallback)
	lines     []entry        // committed transcript blocks
	input     textarea.Model // current input buffer, multi-line with word wrap
	busy      bool           // agent running; input disabled
	events    chan eventMsg  // agent → UI stream for the active Run
	width     int
	height    int

	vp    viewport.Model
	ready bool

	// mouse enables mouse capture (wheel scroll, click-to-open). Off by default so
	// the terminal keeps native text selection; PLUTO_MOUSE=on sets the initial
	// state and ctrl+t toggles it at runtime.
	mouse bool

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

	// codeBlocks retains fenced code blocks from assistant messages so they can
	// be copied to the clipboard; notice is the transient notifications-widget
	// message (raw text) shown above the status line, cleared on the next key or
	// mouse event.
	codeBlocks []codeBlock
	notice     string
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
	s := ta.Styles()
	s.Focused.CursorLine = lipgloss.NewStyle()
	s.Blurred.CursorLine = lipgloss.NewStyle()
	ta.SetStyles(s)
	ta.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
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
	m := model{agent: a, login: login, md: newRenderer(80), input: newInput(80), mouse: mouseEnabled()}
	return tea.NewProgram(m)
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
