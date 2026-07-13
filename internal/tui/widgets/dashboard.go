package widgets

import (
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// maxDashboardTableWidth caps the info table so it doesn't get unwieldy on very
// wide terminals while still filling most of the pane.
const maxDashboardTableWidth = 100

// DashboardStyle carries the styles a Dashboard renders with.
type DashboardStyle struct {
	Banner lipgloss.Style
	Label  lipgloss.Style // first table column (field name)
	Value  lipgloss.Style // second table column (value)
	Border lipgloss.Style // table border
	Muted  lipgloss.Style // help lines and footer
}

// DashRow is one label/value row of the info table.
type DashRow struct {
	Label string
	Value string
}

// Dashboard renders the launch/home screen: a banner over an aligned info table,
// help lines, and a footer. It is domain-free; callers supply rows and styles.
type Dashboard struct {
	Banner string
	Rows   []DashRow
	Lines  []string
	Footer string
	Width  int
	Style  DashboardStyle
}

// View renders the dashboard, clipping each line to Width when set.
func (d Dashboard) View() string {
	var blocks []string
	if d.Banner != "" {
		blocks = append(blocks, d.Style.Banner.Render(d.Banner))
	}
	if len(d.Rows) > 0 {
		blocks = append(blocks, d.renderTable())
	}
	if len(d.Lines) > 0 {
		lines := make([]string, len(d.Lines))
		for i, ln := range d.Lines {
			lines[i] = d.Style.Muted.Render(ln)
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	if d.Footer != "" {
		blocks = append(blocks, d.Style.Muted.Render(d.Footer))
	}
	out := strings.Join(blocks, "\n\n")
	if d.Width > 0 {
		out = clipWidth(out, d.Width)
	}
	return out
}

func (d Dashboard) renderTable() string {
	build := func() *table.Table {
		t := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(d.Style.Border).
			BorderRow(true).
			BorderColumn(true).
			StyleFunc(func(_, col int) lipgloss.Style {
				if col == 0 {
					return d.Style.Label.Padding(0, 1)
				}
				return d.Style.Value.Padding(0, 1)
			})
		for _, r := range d.Rows {
			t.Row(r.Label, r.Value)
		}
		return t
	}
	if d.Width <= 0 {
		return build().Render()
	}
	limit := d.Width
	if limit > maxDashboardTableWidth {
		limit = maxDashboardTableWidth
	}
	// The lipgloss table drops its right border when the requested width lands in
	// a narrow band just below the content's natural width; step down until the
	// border renders intact.
	for target := limit; target >= 24; target-- {
		out := build().Width(target).Wrap(true).Render()
		if rightBorderIntact(out) {
			return out
		}
	}
	return build().Render()
}

// rightBorderIntact reports whether a rounded-border table kept its right edge,
// detected via the two right-hand corner glyphs (ANSI-agnostic).
func rightBorderIntact(s string) bool {
	return strings.Contains(s, "╮") && strings.Contains(s, "╯")
}

func clipWidth(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = lipgloss.NewStyle().MaxWidth(w).Render(ln)
	}
	return strings.Join(lines, "\n")
}
