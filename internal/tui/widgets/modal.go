package widgets

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	title    string
	content  string // raw sanitized text; what Content() and copy return
	display  string // body rendered in the viewport, optionally colorized
	style    ModalStyle
	width    int
	height   int
	vp       viewport.Model
	copied   bool
	editable bool
}

// NewModal builds a modal over content; call SetSize before View.
func NewModal(title, content string, style ModalStyle) *Modal {
	s := Sanitize(content)
	m := &Modal{title: Sanitize(title), content: s, display: s, style: style, vp: viewport.New()}
	m.vp.KeyMap = modalKeyMap()
	return m
}

// Highlight replaces the rendered body with fn applied to the modal's
// already-sanitized content, leaving Content() and copy returning the raw text.
// fn runs on sanitized input, so it must only add trusted escape sequences. Call
// before SetSize.
func (m *Modal) Highlight(fn func(string) string) {
	if fn != nil {
		m.display = fn(m.content)
	}
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
	m.vp.SetWidth(w)
	m.vp.SetHeight(h)
	m.vp.SetContent(lipgloss.NewStyle().Width(w).Render(m.display))
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

// Content returns the modal's raw text, for copying to the clipboard.
func (m *Modal) Content() string { return m.content }

// MarkCopied flips the modal into its "copied" state so View reflects it.
func (m *Modal) MarkCopied() { m.copied = true }

// SetEditable toggles the "ctrl+g edit" affordance in the hint line.
func (m *Modal) SetEditable(v bool) { m.editable = v }

// View renders the modal centered over the terminal.
func (m *Modal) View() string {
	w, _ := m.inner()
	title := m.style.Title.MaxWidth(w).Render(m.title)
	hint := "↑/↓/wheel scroll · c copy"
	if m.editable {
		hint += " · ctrl+g edit"
	}
	hint += " · esc close"
	if m.copied {
		hint = "✓ copied to clipboard · esc close"
	}
	body := lipgloss.JoinVertical(lipgloss.Left, title, "", m.vp.View(), m.style.Hint.Render(hint))
	box := m.style.Box.Width(w).Render(body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
