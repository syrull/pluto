package widgets

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Command is one selectable slash command: its Name (used for filtering and
// completion, e.g. "/model"), an optional Args hint (e.g. "[name]"), and a
// one-line Desc.
type Command struct {
	Name string
	Args string
	Desc string
}

// display renders the command's name column, appending the args hint when set.
func (c Command) display() string {
	if c.Args == "" {
		return c.Name
	}
	return c.Name + " " + c.Args
}

// CommandMenu is a slash-command autocomplete popup: it filters a fixed command
// set by a prefix query and renders as a bordered box meant to be anchored above
// the input rather than centered.
type CommandMenu struct {
	commands []Command
	match    []Command
	style    ListStyle
	cursor   int
	offset   int
}

// NewCommandMenu builds a menu over commands with an empty query (all shown).
func NewCommandMenu(commands []Command, style ListStyle) *CommandMenu {
	m := &CommandMenu{commands: commands, style: style}
	m.Filter("")
	return m
}

// Filter narrows the visible commands to those whose name has query as a
// case-insensitive prefix, resetting the highlight to the top. A leading '/' on
// query is optional.
func (m *CommandMenu) Filter(query string) {
	q := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(query), "/"))
	m.match = m.match[:0]
	for _, c := range m.commands {
		name := strings.ToLower(strings.TrimPrefix(c.Name, "/"))
		if strings.HasPrefix(name, q) {
			m.match = append(m.match, c)
		}
	}
	m.cursor, m.offset = 0, 0
}

// Up moves the highlight one row toward the top, clamping at the first row.
func (m *CommandMenu) Up() {
	if m.cursor > 0 {
		m.cursor--
	}
}

// Down moves the highlight one row toward the bottom, clamping at the last row.
func (m *CommandMenu) Down() {
	if m.cursor < len(m.match)-1 {
		m.cursor++
	}
}

// Cycle advances the highlight by one, wrapping past the last row to the first.
func (m *CommandMenu) Cycle() {
	if len(m.match) == 0 {
		return
	}
	m.cursor = (m.cursor + 1) % len(m.match)
}

// Len reports how many commands match the current query.
func (m *CommandMenu) Len() int { return len(m.match) }

// Selected returns the highlighted command, or false when nothing matches.
func (m *CommandMenu) Selected() (Command, bool) {
	if m.cursor < 0 || m.cursor >= len(m.match) {
		return Command{}, false
	}
	return m.match[m.cursor], true
}

// View renders the matches inside a bordered box of the given outer width,
// showing at most maxRows rows with a position counter when the list overflows.
// It returns "" when nothing matches so the caller can skip drawing it.
func (m *CommandMenu) View(width, maxRows int) string {
	if len(m.match) == 0 {
		return ""
	}
	inner := width - 2 // border
	if inner < 10 {
		inner = 10
	}
	rows := len(m.match)
	if maxRows > 0 && rows > maxRows {
		rows = maxRows
	}
	if rows < 1 {
		rows = 1
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}

	nameW := 0
	for _, c := range m.commands {
		if w := lipgloss.Width(c.display()); w > nameW {
			nameW = w
		}
	}

	var b strings.Builder
	for i := 0; i < rows; i++ {
		idx := m.offset + i
		if i > 0 {
			b.WriteByte('\n')
		}
		if idx >= len(m.match) {
			continue
		}
		c := m.match[idx]
		disp := c.display()
		pad := strings.Repeat(" ", nameW-lipgloss.Width(disp))
		if idx == m.cursor {
			row := truncCells(disp+pad+"  "+c.Desc, inner-2)
			b.WriteString(m.style.Selected.Render("▸ " + row))
			continue
		}
		line := "  " + m.style.Item.Render(disp+pad)
		if avail := inner - 2 - nameW - 2; avail > 0 {
			line += "  " + m.style.Title.Render(truncCells(c.Desc, avail))
		}
		b.WriteString(line)
	}
	if len(m.match) > rows {
		b.WriteByte('\n')
		b.WriteString(m.style.Title.Render(fmt.Sprintf("  %d/%d", m.cursor+1, len(m.match))))
	}

	return m.style.Box.Width(width).Render(b.String())
}
