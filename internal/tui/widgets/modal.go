package widgets

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ModalStyle carries the styles a Modal renders with.
type ModalStyle struct {
	Box   lipgloss.Style
	Title lipgloss.Style
	Hint  lipgloss.Style
}

// Modal is a centered, full-screen, scrollable text viewer. It is domain-free:
// callers supply a title and content and drive it with SetSize/Update/View.
type Modal struct {
	title   string
	content string
	style   ModalStyle
	width   int
	height  int
	vp      viewport.Model
}

// NewModal builds a modal over content; call SetSize before View.
func NewModal(title, content string, style ModalStyle) *Modal {
	m := &Modal{title: Sanitize(title), content: Sanitize(content), style: style, vp: viewport.New(0, 0)}
	m.vp.KeyMap = modalKeyMap()
	return m
}

func modalKeyMap() viewport.KeyMap {
	return viewport.KeyMap{
		Up:           key.NewBinding(key.WithKeys("up", "k")),
		Down:         key.NewBinding(key.WithKeys("down", "j")),
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		PageDown:     key.NewBinding(key.WithKeys("pgdown")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
	}
}

// SetSize fits the modal to the outer terminal dimensions and rewraps content.
func (m *Modal) SetSize(width, height int) {
	m.width, m.height = width, height
	w, h := m.inner()
	m.vp.Width = w
	m.vp.Height = h
	m.vp.SetContent(lipgloss.NewStyle().Width(w).Render(m.content))
}

// inner returns the width and height available for the scrollable body.
func (m *Modal) inner() (int, int) {
	w := m.width - 8
	if w < 20 {
		w = 20
	}
	h := m.height - 8
	if h < 3 {
		h = 3
	}
	return w, h
}

// Update forwards scroll keys and wheel events to the body viewport.
func (m *Modal) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return cmd
}

// AtTop reports whether the body is scrolled to the top.
func (m *Modal) AtTop() bool { return m.vp.AtTop() }

// View renders the modal centered over the terminal.
func (m *Modal) View() string {
	w, _ := m.inner()
	title := m.style.Title.MaxWidth(w).Render(m.title)
	hint := m.style.Hint.Render("↑/↓/wheel scroll · esc close")
	body := lipgloss.JoinVertical(lipgloss.Left, title, "", m.vp.View(), hint)
	box := m.style.Box.Width(w).Render(body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
