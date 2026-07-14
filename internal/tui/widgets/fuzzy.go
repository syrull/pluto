package widgets

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

// FuzzyPicker is a reusable keyboard-driven overlay that filters items with an
// fzf-style fuzzy match as the user types, rendered as a centered box with a
// query line above the ranked results.
type FuzzyPicker struct {
	title  string
	items  []string // all candidates
	match  []string // items that match the query, best-ranked first
	query  string
	style  ListStyle
	cursor int
	offset int
	width  int
	height int
}

// NewFuzzyPicker builds a picker over items with an empty query (all items
// shown).
func NewFuzzyPicker(title string, items []string, style ListStyle) *FuzzyPicker {
	p := &FuzzyPicker{title: title, items: items, style: style}
	p.filter()
	return p
}

// SetSize sets the outer terminal dimensions the picker centers within.
func (p *FuzzyPicker) SetSize(w, h int) { p.width, p.height = w, h }

// Query returns the current filter text.
func (p *FuzzyPicker) Query() string { return p.query }

// Insert appends typed text to the query and re-ranks.
func (p *FuzzyPicker) Insert(s string) {
	p.query += s
	p.filter()
}

// Backspace removes the last query rune and re-ranks.
func (p *FuzzyPicker) Backspace() {
	if p.query == "" {
		return
	}
	r := []rune(p.query)
	p.query = string(r[:len(r)-1])
	p.filter()
}

// Up moves the highlight one row toward the top, clamping at the first row.
func (p *FuzzyPicker) Up() {
	if p.cursor > 0 {
		p.cursor--
	}
}

// Down moves the highlight one row toward the bottom, clamping at the last row.
func (p *FuzzyPicker) Down() {
	if p.cursor < len(p.match)-1 {
		p.cursor++
	}
}

// Selected returns the highlighted match, or false when nothing matches.
func (p *FuzzyPicker) Selected() (string, bool) {
	if p.cursor < 0 || p.cursor >= len(p.match) {
		return "", false
	}
	return p.match[p.cursor], true
}

// filter re-ranks items against the query, resetting the cursor to the top.
func (p *FuzzyPicker) filter() {
	p.cursor, p.offset = 0, 0
	if p.query == "" {
		p.match = p.items
		return
	}
	type scored struct {
		s     string
		score int
		idx   int
	}
	var res []scored
	for i, it := range p.items {
		if sc, ok := fuzzyScore(p.query, it); ok {
			res = append(res, scored{it, sc, i})
		}
	}
	sort.SliceStable(res, func(a, b int) bool {
		if res[a].score != res[b].score {
			return res[a].score > res[b].score
		}
		return res[a].idx < res[b].idx
	})
	p.match = make([]string, len(res))
	for i, r := range res {
		p.match[i] = r.s
	}
}

// View renders the query line and the ranked matches inside a centered box.
func (p *FuzzyPicker) View() string {
	inner := p.width - 8
	if inner < 20 {
		inner = 20
	}
	if inner > 72 {
		inner = 72
	}
	rows := len(p.match)
	if max := p.height - 8; max > 0 && rows > max {
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
	b.WriteByte('\n')
	b.WriteString(p.style.Title.Render("› " + p.query))
	for i := 0; i < rows; i++ {
		idx := p.offset + i
		b.WriteByte('\n')
		if idx >= len(p.match) {
			continue
		}
		it := truncCells(p.match[idx], inner-2)
		if idx == p.cursor {
			b.WriteString(p.style.Selected.Render("▸ " + it))
		} else {
			b.WriteString(p.style.Item.Render("  " + it))
		}
	}
	if len(p.match) > rows {
		b.WriteByte('\n')
		b.WriteString(p.style.Item.Render(fmt.Sprintf("  %d/%d", p.cursor+1, len(p.match))))
	}

	box := p.style.Box.Width(inner).Render(b.String())
	if p.width > 0 && p.height > 0 {
		return lipgloss.Place(p.width, p.height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}

// fuzzyScore reports whether pattern matches text as a case-insensitive
// subsequence and, if so, a score that rewards consecutive hits and hits on
// word/path boundaries so tighter, better-aligned matches rank higher.
func fuzzyScore(pattern, text string) (int, bool) {
	if pattern == "" {
		return 0, true
	}
	p := []rune(strings.ToLower(pattern))
	t := []rune(text)
	lower := []rune(strings.ToLower(text))
	score, pi, prev := 0, 0, -2
	for ti := 0; ti < len(t) && pi < len(p); ti++ {
		if lower[ti] != p[pi] {
			continue
		}
		s := 1
		if ti == prev+1 {
			s += 5
		}
		if ti == 0 || isBoundary(t[ti-1], t[ti]) {
			s += 3
		}
		score += s
		prev = ti
		pi++
	}
	if pi < len(p) {
		return 0, false
	}
	return score - len(t)/20, true
}

// isBoundary reports whether cur begins a new word given the preceding rune: a
// separator start or a camelCase transition.
func isBoundary(prev, cur rune) bool {
	switch prev {
	case '/', '\\', '_', '-', '.', ' ':
		return true
	}
	return unicode.IsLower(prev) && unicode.IsUpper(cur)
}
