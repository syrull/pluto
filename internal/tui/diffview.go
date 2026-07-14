package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/syrull/pluto/internal/diff"
)

// tabWidth is how many spaces a tab expands to when a diff line is rendered, so
// indentation aligns predictably under the gutter and survives wrapping.
const tabWidth = 4

// diffKind classifies a parsed diff row.
type diffKind int

const (
	diffContext diffKind = iota
	diffAdd
	diffDel
	diffHunk
	diffMeta
)

// diffRow is one parsed line of a unified diff: its kind, the old/new line
// numbers (0 when absent) and, for changed lines, the byte ranges within text
// that differ from the paired line.
type diffRow struct {
	kind  diffKind
	oldNo int
	newNo int
	text  string
	hi    [][2]int
}

// renderUnifiedDiff turns a git unified diff into a guttered, per-hunk view with
// old/new line numbers and word-level highlighting of changed spans, hard-wrapped
// to width with a hanging indent so wrapped lines stay aligned under the gutter.
func renderUnifiedDiff(s string, width int) string {
	rows := parseUnifiedDiff(s)
	if len(rows) == 0 {
		return s
	}
	attachWordDiff(rows)
	numW := maxNumWidth(rows)
	if width < 1 {
		width = defaultWrapWidth
	}
	var out []string
	for i, r := range rows {
		if r.kind == diffHunk && i > 0 {
			out = append(out, "")
		}
		out = append(out, r.render(numW, width)...)
	}
	return strings.Join(out, "\n")
}

// parseUnifiedDiff walks a git diff, tracking line numbers from each hunk header
// and dropping the noisy file metadata (diff/index/---/+++) that the modal title
// already conveys.
func parseUnifiedDiff(s string) []diffRow {
	var rows []diffRow
	var oldNo, newNo int
	inHunk := false
	for _, ln := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(ln, "@@"):
			oldNo, newNo = parseHunkNums(ln)
			rows = append(rows, diffRow{kind: diffHunk, text: ln})
			inHunk = true
		case !inHunk:
			if r, ok := metaRow(ln); ok {
				rows = append(rows, r)
			}
		case strings.HasPrefix(ln, "+"):
			rows = append(rows, diffRow{kind: diffAdd, newNo: newNo, text: expandTabs(ln[1:])})
			newNo++
		case strings.HasPrefix(ln, "-"):
			rows = append(rows, diffRow{kind: diffDel, oldNo: oldNo, text: expandTabs(ln[1:])})
			oldNo++
		case strings.HasPrefix(ln, "\\"):
			rows = append(rows, diffRow{kind: diffMeta, text: strings.TrimSpace(ln)})
		default:
			rows = append(rows, diffRow{kind: diffContext, oldNo: oldNo, newNo: newNo, text: expandTabs(strings.TrimPrefix(ln, " "))})
			oldNo++
			newNo++
		}
	}
	return rows
}

// metaRow keeps only the header lines that carry meaning (new/deleted/renamed or
// binary files), rendered dim; everything else in the header is dropped.
func metaRow(ln string) (diffRow, bool) {
	switch {
	case strings.HasPrefix(ln, "new file"),
		strings.HasPrefix(ln, "deleted file"),
		strings.HasPrefix(ln, "rename "),
		strings.HasPrefix(ln, "copy "),
		strings.HasPrefix(ln, "Binary files"):
		return diffRow{kind: diffMeta, text: ln}, true
	}
	return diffRow{}, false
}

// attachWordDiff pairs each run of removed lines with the following run of added
// lines and records, on each paired row, the spans that changed.
func attachWordDiff(rows []diffRow) {
	for i := 0; i < len(rows); {
		if rows[i].kind != diffDel {
			i++
			continue
		}
		delStart := i
		for i < len(rows) && rows[i].kind == diffDel {
			i++
		}
		addStart := i
		for i < len(rows) && rows[i].kind == diffAdd {
			i++
		}
		paired := min(addStart-delStart, i-addStart)
		for k := 0; k < paired; k++ {
			d := &rows[delStart+k]
			a := &rows[addStart+k]
			d.hi, a.hi = wordSpans(d.text, a.text)
		}
	}
}

// wordSpans returns the changed byte ranges within old and new. It returns no
// spans when the lines share no non-space token, since highlighting every word
// of a wholesale rewrite adds only noise.
func wordSpans(old, new string) (del, add [][2]int) {
	toks := diff.Words(old, new)
	if toks == nil {
		return nil, nil
	}
	shared := false
	for _, t := range toks {
		if t.Op == ' ' && strings.TrimSpace(t.Text) != "" {
			shared = true
			break
		}
	}
	if !shared {
		return nil, nil
	}
	var op, np int
	for _, t := range toks {
		n := len(t.Text)
		switch t.Op {
		case ' ':
			op += n
			np += n
		case '-':
			del = append(del, [2]int{op, op + n})
			op += n
		case '+':
			add = append(add, [2]int{np, np + n})
			np += n
		}
	}
	return del, add
}

func (r diffRow) render(numW, width int) []string {
	switch r.kind {
	case diffHunk:
		return []string{renderHunkHeader(r.text)}
	case diffMeta:
		return []string{styleHint.Render(r.text)}
	}
	gw := gutterWidth(numW)
	textW := width - gw
	if textW < 8 {
		textW = 8
	}
	base, hi := r.textStyles()
	gutter := r.gutter(numW)
	cont := strings.Repeat(" ", gw)
	segs := wrapStyled(r.text, r.hi, textW, base, hi)
	out := make([]string, len(segs))
	for i, seg := range segs {
		if i == 0 {
			out[i] = gutter + seg
		} else {
			out[i] = cont + seg
		}
	}
	return out
}

func (r diffRow) textStyles() (base, hi lipgloss.Style) {
	switch r.kind {
	case diffAdd:
		return styleDiffAdd, styleDiffAddHi
	case diffDel:
		return styleDiffDel, styleDiffDelHi
	default:
		return styleDiffCtx, styleDiffCtx
	}
}

// gutter renders the "old new M " prefix: right-aligned line numbers dim, then a
// colored +/-/space marker.
func (r diffRow) gutter(numW int) string {
	oldStr, newStr := "", ""
	if r.oldNo > 0 {
		oldStr = strconv.Itoa(r.oldNo)
	}
	if r.newNo > 0 {
		newStr = strconv.Itoa(r.newNo)
	}
	nums := styleDiffCtx.Render(fmt.Sprintf("%*s %*s ", numW, oldStr, numW, newStr))
	marker, style := " ", styleDiffCtx
	switch r.kind {
	case diffAdd:
		marker, style = "+", styleDiffAdd
	case diffDel:
		marker, style = "-", styleDiffDel
	}
	return nums + style.Render(marker+" ")
}

func gutterWidth(numW int) int { return 2*numW + 4 }

func maxNumWidth(rows []diffRow) int {
	mx := 0
	for _, r := range rows {
		mx = max(mx, r.oldNo, r.newNo)
	}
	if w := len(strconv.Itoa(mx)); w > 1 {
		return w
	}
	return 1
}

func renderHunkHeader(ln string) string {
	rangePart, section := splitHunk(ln)
	out := stylePrompt.Render(rangePart)
	if section != "" {
		out += " " + styleHint.Render(section)
	}
	return out
}

// splitHunk separates the "@@ -a,b +c,d @@" range from the trailing section
// context git appends (the enclosing function, usually).
func splitHunk(ln string) (rangePart, section string) {
	first := strings.Index(ln, "@@")
	if first < 0 {
		return strings.TrimSpace(ln), ""
	}
	rest := ln[first+2:]
	second := strings.Index(rest, "@@")
	if second < 0 {
		return strings.TrimSpace(ln), ""
	}
	end := first + 2 + second + 2
	return strings.TrimSpace(ln[:end]), strings.TrimSpace(ln[end:])
}

func parseHunkNums(ln string) (oldNo, newNo int) {
	oldNo, newNo = 1, 1
	rangePart, _ := splitHunk(ln)
	for _, f := range strings.Fields(rangePart) {
		switch {
		case strings.HasPrefix(f, "-"):
			oldNo = parseStart(f[1:])
		case strings.HasPrefix(f, "+"):
			newNo = parseStart(f[1:])
		}
	}
	return oldNo, newNo
}

func parseStart(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return 1
}

func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	return strings.ReplaceAll(s, "\t", strings.Repeat(" ", tabWidth))
}

// wrapStyled hard-wraps text to width runes per line, styling the highlighted
// byte ranges with hi and the rest with base. It always returns at least one
// (possibly empty) segment.
func wrapStyled(text string, ranges [][2]int, width int, base, hi lipgloss.Style) []string {
	if width < 1 {
		width = 1
	}
	runes := []rune(text)
	flags := highlightFlags(runes, ranges)
	var out []string
	for start := 0; start < len(runes); start += width {
		end := min(start+width, len(runes))
		out = append(out, styleRunes(runes, flags, start, end, base, hi))
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func highlightFlags(runes []rune, ranges [][2]int) []bool {
	flags := make([]bool, len(runes))
	if len(ranges) == 0 {
		return flags
	}
	pos := 0
	for i, r := range runes {
		for _, rg := range ranges {
			if pos >= rg[0] && pos < rg[1] {
				flags[i] = true
				break
			}
		}
		pos += utf8.RuneLen(r)
	}
	return flags
}

func styleRunes(runes []rune, flags []bool, start, end int, base, hi lipgloss.Style) string {
	var b strings.Builder
	for i := start; i < end; {
		j, on := i, flags[i]
		for j < end && flags[j] == on {
			j++
		}
		seg := string(runes[i:j])
		if on {
			b.WriteString(hi.Render(seg))
		} else {
			b.WriteString(base.Render(seg))
		}
		i = j
	}
	return b.String()
}
