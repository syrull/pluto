// Package widgets holds reusable TUI components free of domain behavior.
package widgets

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ListStyle carries the styles a ListPicker renders with.
type ListStyle struct {
	Title    lipgloss.Style
	Selected lipgloss.Style
	Item     lipgloss.Style
}

// ListPicker is a reusable keyboard-driven selection overlay.
type ListPicker struct {
	title  string
	items  []string
	style  ListStyle
	cursor int
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

// View renders the picker as a titled list with the cursor row highlighted.
func (p *ListPicker) View() string {
	var b strings.Builder
	b.WriteString(p.style.Title.Render(p.title))
	for i, it := range p.items {
		b.WriteByte('\n')
		if i == p.cursor {
			b.WriteString(p.style.Selected.Render("▸ " + it))
		} else {
			b.WriteString(p.style.Item.Render("  " + it))
		}
	}
	return b.String()
}
