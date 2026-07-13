// Package widgets holds reusable TUI components free of domain behavior.
package widgets

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// ListStyle carries the styles a ListPicker renders with.
type ListStyle struct {
	Title    lipgloss.Style
	Selected lipgloss.Style
	Item     lipgloss.Style
	Box      lipgloss.Style
}

// ListPicker is a reusable keyboard-driven selection overlay rendered as a
// centered, scrollable box.
type ListPicker struct {
	title  string
	items  []string
	style  ListStyle
	cursor int
	offset int
	width  int
	height int
}

// NewListPicker builds a picker over items with the cursor starting on active.
func NewListPicker(title string, items []string, active string, style ListStyle) *ListPicker {
	cursor := 0
	for i, it := range items {
		if it == active {
			cursor = i
			break
		}
	}
	return &ListPicker{title: title, items: items, style: style, cursor: cursor}
}

// SetSize sets the outer terminal dimensions the picker centers within.
func (p *ListPicker) SetSize(w, h int) { p.width, p.height = w, h }

// Up moves the highlight one row toward the top, clamping at the first row.
func (p *ListPicker) Up() {
	if p.cursor > 0 {
		p.cursor--
	}
}

// Down moves the highlight one row toward the bottom, clamping at the last row.
func (p *ListPicker) Down() {
	if p.cursor < len(p.items)-1 {
		p.cursor++
	}
}

// Selected returns the currently-highlighted item.
func (p *ListPicker) Selected() string { return p.items[p.cursor] }

// View renders the picker as a titled, scrollable list inside a centered box.
func (p *ListPicker) View() string {
	inner := p.width - 8
	if inner < 20 {
		inner = 20
	}
	if inner > 72 {
		inner = 72
	}
	rows := len(p.items)
	if max := p.height - 6; max > 0 && rows > max {
		rows = max
	}
	if rows < 1 {
		rows = 1
	}
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	if p.offset < 0 {
		p.offset = 0
	}

	var b strings.Builder
	b.WriteString(p.style.Title.Render(p.title))
	for i := 0; i < rows; i++ {
		idx := p.offset + i
		b.WriteByte('\n')
		it := truncCells(p.items[idx], inner-2)
		if idx == p.cursor {
			b.WriteString(p.style.Selected.Render("▸ " + it))
		} else {
			b.WriteString(p.style.Item.Render("  " + it))
		}
	}
	if len(p.items) > rows {
		b.WriteByte('\n')
		b.WriteString(p.style.Item.Render(fmt.Sprintf("  %d/%d", p.cursor+1, len(p.items))))
	}

	box := p.style.Box.Width(inner).Render(b.String())
	if p.width > 0 && p.height > 0 {
		return lipgloss.Place(p.width, p.height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}
