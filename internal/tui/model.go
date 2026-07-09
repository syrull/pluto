package tui

import (
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

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

type model struct {
	agent  *agent.Agent
	login  *LoginHook
	lines  []string      // committed transcript lines
	input  string        // current input buffer
	busy   bool          // agent running; input disabled
	events chan eventMsg // agent → UI stream for the active Run
	width  int

	vp    viewport.Model
	ready bool

	// Streaming accumulators: strings because Bubbletea copies the model by value and Builder panics when copied.
	streamText  string
	streamThink string

	md *glamour.TermRenderer // markdown renderer, rebuilt on resize

	picker *widgets.ListPicker
}

const footerHeight = 3

// New builds the Bubbletea program.
func New(a *agent.Agent, login *LoginHook) *tea.Program {
	m := model{agent: a, login: login, md: newRenderer(80)}
	m.lines = append(m.lines,
		styleHint.Render("harness ready — commands: /login to authenticate · /model to pick a model (↑/↓, enter) · /think [off|low|medium|high|xhigh|max] to set thinking effort, model-dependent (bare /think cycles) · /new to start a fresh conversation · ctrl+c to quit"),
		styleHint.Render("scroll: pgup/pgdn · ctrl+u/ctrl+d"),
	)
	return tea.NewProgram(m, tea.WithAltScreen())
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

func (m model) transcript() string {
	body := strings.Join(m.lines, "\n")
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
