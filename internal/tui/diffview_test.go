package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func plain(s string) string { return sgrRe.ReplaceAllString(s, "") }

const sampleDiff = `diff --git a/f.go b/f.go
index 111..222 100644
--- a/f.go
+++ b/f.go
@@ -10,3 +10,4 @@ func Foo() {
 ctx line
-old line
+new line
+added line
 trailing`

func TestParseUnifiedDiffTracksLineNumbers(t *testing.T) {
	rows := parseUnifiedDiff(sampleDiff)

	// File metadata (diff/index/---/+++) is dropped; only the hunk + body remain.
	if rows[0].kind != diffHunk {
		t.Fatalf("row 0 kind = %v, want hunk", rows[0].kind)
	}
	want := []struct {
		kind         diffKind
		oldNo, newNo int
		text         string
	}{
		{diffContext, 10, 10, "ctx line"},
		{diffDel, 11, 0, "old line"},
		{diffAdd, 0, 11, "new line"},
		{diffAdd, 0, 12, "added line"},
		{diffContext, 12, 13, "trailing"},
	}
	body := rows[1:]
	if len(body) != len(want) {
		t.Fatalf("body rows = %d, want %d", len(body), len(want))
	}
	for i, w := range want {
		got := body[i]
		if got.kind != w.kind || got.oldNo != w.oldNo || got.newNo != w.newNo || got.text != w.text {
			t.Fatalf("row %d = %+v, want %+v", i, got, w)
		}
	}
}

func TestAttachWordDiffMarksChangedSpanOnly(t *testing.T) {
	rows := parseUnifiedDiff(sampleDiff)
	attachWordDiff(rows)

	del := rows[2] // "old line"
	add := rows[3] // "new line"
	if len(del.hi) == 0 || len(add.hi) == 0 {
		t.Fatalf("paired lines should carry word spans: del=%v add=%v", del.hi, add.hi)
	}
	// Only the differing word is highlighted, not the shared " line" suffix.
	if got := del.text[del.hi[0][0]:del.hi[0][1]]; got != "old" {
		t.Fatalf("del highlight = %q, want %q", got, "old")
	}
	if got := add.text[add.hi[0][0]:add.hi[0][1]]; got != "new" {
		t.Fatalf("add highlight = %q, want %q", got, "new")
	}
	// The second added line has no delete to pair with, so no word spans.
	if len(rows[4].hi) != 0 {
		t.Fatalf("unpaired add should have no word spans, got %v", rows[4].hi)
	}
}

func TestWordSpansSkipsWholeRewrite(t *testing.T) {
	del, add := wordSpans("foo", "bar")
	if del != nil || add != nil {
		t.Fatalf("a wholesale rewrite should not be word-highlighted: del=%v add=%v", del, add)
	}
}

func TestRenderUnifiedDiffShowsGutterNumbers(t *testing.T) {
	out := plain(renderUnifiedDiff(sampleDiff, 80))
	if !strings.Contains(out, "10 10") {
		t.Fatalf("context line should show both line numbers:\n%s", out)
	}
	if !strings.Contains(out, "11    -") || !strings.Contains(out, "11 +") {
		t.Fatalf("changed lines should show one-sided numbers and markers:\n%s", out)
	}
}

func TestRenderUnifiedDiffHunkHeaderAndSection(t *testing.T) {
	out := plain(renderUnifiedDiff(sampleDiff, 80))
	if !strings.Contains(out, "@@ -10,3 +10,4 @@") {
		t.Fatalf("hunk range should be shown:\n%s", out)
	}
	if !strings.Contains(out, "func Foo() {") {
		t.Fatalf("hunk section context should be shown:\n%s", out)
	}
}

func TestWrapStyledHangsUnderGutter(t *testing.T) {
	nostyle := lipgloss.NewStyle()
	segs := wrapStyled("abcdefghij", nil, 4, nostyle, nostyle)
	if len(segs) != 3 {
		t.Fatalf("wrap of 10 runes at width 4 = %d segments, want 3", len(segs))
	}
	if strings.Join(segs, "") != "abcdefghij" {
		t.Fatalf("wrapped segments should reconstruct the text, got %q", segs)
	}
}

func TestRenderUnifiedDiffWrapsWithHangingIndent(t *testing.T) {
	long := "@@ -1 +1 @@\n context " + strings.Repeat("x", 200)
	lines := strings.Split(plain(renderUnifiedDiff(long, 40)), "\n")
	if len(lines) < 3 {
		t.Fatalf("a 200-char line at width 40 should wrap onto several rows:\n%v", lines)
	}
	// Continuation rows are indented under the gutter (no digit in column 0).
	cont := lines[len(lines)-1]
	if cont == "" || cont[0] != ' ' {
		t.Fatalf("continuation row should be indented under the gutter, got %q", cont)
	}
}
