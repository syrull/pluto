package diff

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

func TestSplitLinesEmpty(t *testing.T) {
	result := SplitLines("")
	if result != nil {
		t.Fatalf("SplitLines(\"\") = %v, want nil", result)
	}
}

func TestSplitLinesTrailingNewlineDoesNotAddPhantomLine(t *testing.T) {
	withNewline := SplitLines("a\nb\n")
	withoutNewline := SplitLines("a\nb")

	if len(withNewline) != 2 || withNewline[0] != "a" || withNewline[1] != "b" {
		t.Fatalf("SplitLines(\"a\\nb\\n\") = %v, want [a b]", withNewline)
	}
	if len(withoutNewline) != 2 || withoutNewline[0] != "a" || withoutNewline[1] != "b" {
		t.Fatalf("SplitLines(\"a\\nb\") = %v, want [a b]", withoutNewline)
	}
	if len(withNewline) != len(withoutNewline) {
		t.Fatalf("SplitLines with and without trailing newline produce different lengths: %d vs %d",
			len(withNewline), len(withoutNewline))
	}
}

func TestSplitLinesSingleLine(t *testing.T) {
	result := SplitLines("hello")
	if len(result) != 1 || result[0] != "hello" {
		t.Fatalf("SplitLines(\"hello\") = %v, want [hello]", result)
	}
}

func TestComputeMiddleLineChange(t *testing.T) {
	lines := Compute("a\nb\nc", "a\nB\nc").Lines

	// Expected: context "a", "-b", "+B", context "c"
	if len(lines) != 4 {
		t.Fatalf("Compute with middle change = %d lines, want 4", len(lines))
	}
	if lines[0].Op != ' ' || lines[0].Text != "a" {
		t.Fatalf("line 0: Op=%c Text=%q, want Op=' ' Text='a'", lines[0].Op, lines[0].Text)
	}
	if lines[1].Op != '-' || lines[1].Text != "b" {
		t.Fatalf("line 1: Op=%c Text=%q, want Op='-' Text='b'", lines[1].Op, lines[1].Text)
	}
	if lines[2].Op != '+' || lines[2].Text != "B" {
		t.Fatalf("line 2: Op=%c Text=%q, want Op='+' Text='B'", lines[2].Op, lines[2].Text)
	}
	if lines[3].Op != ' ' || lines[3].Text != "c" {
		t.Fatalf("line 3: Op=%c Text=%q, want Op=' ' Text='c'", lines[3].Op, lines[3].Text)
	}
}

func TestComputePureAddition(t *testing.T) {
	lines := Compute("", "x\ny\nz").Lines

	if len(lines) != 3 {
		t.Fatalf("Compute pure addition = %d lines, want 3", len(lines))
	}
	for i, ln := range lines {
		if ln.Op != '+' {
			t.Fatalf("line %d: Op=%c, want Op='+'", i, ln.Op)
		}
	}
	if lines[0].Text != "x" || lines[1].Text != "y" || lines[2].Text != "z" {
		t.Fatalf("pure addition texts = %v, want [x y z]", []string{lines[0].Text, lines[1].Text, lines[2].Text})
	}
}

func TestComputePureDeletion(t *testing.T) {
	lines := Compute("x\ny\nz", "").Lines

	if len(lines) != 3 {
		t.Fatalf("Compute pure deletion = %d lines, want 3", len(lines))
	}
	for i, ln := range lines {
		if ln.Op != '-' {
			t.Fatalf("line %d: Op=%c, want Op='-'", i, ln.Op)
		}
	}
	if lines[0].Text != "x" || lines[1].Text != "y" || lines[2].Text != "z" {
		t.Fatalf("pure deletion texts = %v, want [x y z]", []string{lines[0].Text, lines[1].Text, lines[2].Text})
	}
}

func TestComputeNoChange(t *testing.T) {
	lines := Compute("a\nb\nc", "a\nb\nc").Lines

	if len(lines) != 3 {
		t.Fatalf("Compute with no change = %d lines, want 3", len(lines))
	}
	for i, ln := range lines {
		if ln.Op != ' ' {
			t.Fatalf("line %d: Op=%c, want Op=' '", i, ln.Op)
		}
	}
}

func TestStatsCorrectCounts(t *testing.T) {
	lines := []Line{
		{Op: ' ', Text: "context"},
		{Op: '-', Text: "removed1"},
		{Op: '-', Text: "removed2"},
		{Op: '+', Text: "added1"},
		{Op: ' ', Text: "context2"},
		{Op: '+', Text: "added2"},
		{Op: '+', Text: "added3"},
	}

	added, removed := Stats(lines)

	if added != 3 {
		t.Fatalf("added = %d, want 3", added)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
}

func TestStatsEmptyLines(t *testing.T) {
	added, removed := Stats([]Line{})

	if added != 0 || removed != 0 {
		t.Fatalf("Stats([]) = (%d, %d), want (0, 0)", added, removed)
	}
}

func TestFormatRendersWithOpPrefix(t *testing.T) {
	lines := []Line{
		{Op: ' ', Text: "context"},
		{Op: '-', Text: "oldline"},
		{Op: '+', Text: "newline"},
	}

	result := Format(lines)

	expected := " context\n-oldline\n+newline"
	if result != expected {
		t.Fatalf("Format result:\n%q\nwant:\n%q", result, expected)
	}
}

func TestFormatEmptyReturnsEmpty(t *testing.T) {
	result := Format([]Line{})

	if result != "" {
		t.Fatalf("Format([]) = %q, want \"\"", result)
	}
}

func TestFormatSingleLine(t *testing.T) {
	result := Format([]Line{{Op: '+', Text: "added"}})

	if result != "+added" {
		t.Fatalf("Format([+added]) = %q, want \"+added\"", result)
	}
}

func TestComputeReconstructsRandomized(t *testing.T) {
	const numIterations = 2500
	const alphabetSize = 4
	alphabet := []string{"a", "b", "c", "d"}

	// Use PCG with fixed seeds for determinism.
	source := rand.New(rand.NewPCG(1, 2))

	for iter := 0; iter < numIterations; iter++ {
		// Generate random old and new slices (0..10 lines each).
		oldLen := source.IntN(11)
		newLen := source.IntN(11)

		oldLines := make([]string, oldLen)
		for i := range oldLen {
			oldLines[i] = alphabet[source.IntN(alphabetSize)]
		}

		newLines := make([]string, newLen)
		for i := range newLen {
			newLines[i] = alphabet[source.IntN(alphabetSize)]
		}

		old := strings.Join(oldLines, "\n")
		new := strings.Join(newLines, "\n")

		result := Compute(old, new)

		if result.TooLarge {
			t.Fatalf("iter %d: Compute marked TooLarge for small inputs (old=%d lines, new=%d lines)", iter, oldLen, newLen)
		}

		// Reconstruct new: collect lines where Op is ' ' or '+'.
		var reconstructedNew []string
		for _, line := range result.Lines {
			if line.Op == ' ' || line.Op == '+' {
				reconstructedNew = append(reconstructedNew, line.Text)
			}
		}

		// Reconstruct old: collect lines where Op is ' ' or '-'.
		var reconstructedOld []string
		for _, line := range result.Lines {
			if line.Op == ' ' || line.Op == '-' {
				reconstructedOld = append(reconstructedOld, line.Text)
			}
		}

		// Compare reconstructions to originals.
		if !slicesEqual(reconstructedNew, newLines) {
			t.Fatalf("iter %d: new reconstruction mismatch\nold=%v\nnew=%v\nresult.Lines=%v\nreconstructedNew=%v",
				iter, oldLines, newLines, result.Lines, reconstructedNew)
		}
		if !slicesEqual(reconstructedOld, oldLines) {
			t.Fatalf("iter %d: old reconstruction mismatch\nold=%v\nnew=%v\nresult.Lines=%v\nreconstructedOld=%v",
				iter, oldLines, newLines, result.Lines, reconstructedOld)
		}
	}
}

func TestComputeMinimalEditCount(t *testing.T) {
	tests := []struct {
		name          string
		old           string
		new           string
		expectAdded   int
		expectRemoved int
	}{
		{
			name:          "identical inputs",
			old:           "a\nb\nc",
			new:           "a\nb\nc",
			expectAdded:   0,
			expectRemoved: 0,
		},
		{
			name:          "one line changed in middle",
			old:           "a\nb\nc\nd\ne",
			new:           "a\nb\nX\nd\ne",
			expectAdded:   1,
			expectRemoved: 1,
		},
		{
			name:          "pure append three lines",
			old:           "a\nb",
			new:           "a\nb\nx\ny\nz",
			expectAdded:   3,
			expectRemoved: 0,
		},
		{
			name:          "pure deletion three lines",
			old:           "a\nb\nx\ny\nz",
			new:           "a\nb",
			expectAdded:   0,
			expectRemoved: 3,
		},
		{
			name:          "repeated lines deletion",
			old:           "a\na\na",
			new:           "a\na",
			expectAdded:   0,
			expectRemoved: 1,
		},
	}

	for _, tc := range tests {
		result := Compute(tc.old, tc.new)
		if result.TooLarge {
			t.Fatalf("%s: Compute marked TooLarge unexpectedly", tc.name)
		}

		added, removed := Stats(result.Lines)
		if added != tc.expectAdded || removed != tc.expectRemoved {
			t.Fatalf("%s: Stats = (%d, %d), want (%d, %d)",
				tc.name, added, removed, tc.expectAdded, tc.expectRemoved)
		}
	}
}

func TestComputeLargeSimilar(t *testing.T) {
	const numLines = 1500
	const changeIndex = 750

	// Build old: all "line000", "line001", ..., "line1499".
	var oldLines []string
	for i := range numLines {
		d1 := '0' + rune(i/1000)%10
		d2 := '0' + rune(i/100)%10
		d3 := '0' + rune(i/10)%10
		d4 := '0' + rune(i%10)
		oldLines = append(oldLines, "line"+string(d1)+string(d2)+string(d3)+string(d4))
	}
	old := strings.Join(oldLines, "\n")

	// Build new: identical to old except one line changed at changeIndex.
	newLines := make([]string, len(oldLines))
	copy(newLines, oldLines)
	newLines[changeIndex] = "CHANGED"
	new := strings.Join(newLines, "\n")

	result := Compute(old, new)

	if result.TooLarge {
		t.Fatalf("Large similar inputs marked TooLarge unexpectedly")
	}

	added, removed := Stats(result.Lines)
	if added != 1 || removed != 1 {
		t.Fatalf("Large similar: Stats = (%d, %d), want (1, 1)", added, removed)
	}

	// Verify most lines are context (not all rewrites).
	contextCount := 0
	for _, line := range result.Lines {
		if line.Op == ' ' {
			contextCount++
		}
	}
	if contextCount < numLines-10 {
		t.Fatalf("Large similar: only %d context lines, want ~%d", contextCount, numLines)
	}
}

func TestComputeTooLarge(t *testing.T) {
	// Build a string with exactly MaxLines+1 lines.
	tooManyLines := make([]string, MaxLines+1)
	for i := range tooManyLines {
		tooManyLines[i] = "line"
	}
	big := strings.Join(tooManyLines, "\n")
	bigModified := big // any version of big; doesn't matter

	result := Compute(big, bigModified)
	if !result.TooLarge {
		t.Fatalf("Compute with %d lines: TooLarge = false, want true", MaxLines+1)
	}
	if result.Lines != nil {
		t.Fatalf("Compute with TooLarge=true: Lines != nil, want nil")
	}

	// Verify that exactly MaxLines is NOT TooLarge.
	justAtLimit := make([]string, MaxLines)
	for i := range justAtLimit {
		justAtLimit[i] = "line"
	}
	atLimit := strings.Join(justAtLimit, "\n")
	resultAtLimit := Compute(atLimit, atLimit)
	if resultAtLimit.TooLarge {
		t.Fatalf("Compute with exactly MaxLines=%d: TooLarge = true, want false", MaxLines)
	}
	if resultAtLimit.Lines == nil {
		t.Fatalf("Compute with TooLarge=false: Lines = nil, want non-nil")
	}
}

func TestComputeContextInterleaving(t *testing.T) {
	old := "a\nb\nc\nd\ne"
	new := "a\nX\nc\nY\ne"

	result := Compute(old, new)
	if result.TooLarge {
		t.Fatalf("Context interleaving test: Compute marked TooLarge unexpectedly")
	}

	// Expected sequence: ' a', '-b', '+X', ' c', '-d', '+Y', ' e'.
	expected := []struct {
		op   byte
		text string
	}{
		{' ', "a"},
		{'-', "b"},
		{'+', "X"},
		{' ', "c"},
		{'-', "d"},
		{'+', "Y"},
		{' ', "e"},
	}

	if len(result.Lines) != len(expected) {
		t.Fatalf("Context interleaving: %d lines emitted, want %d", len(result.Lines), len(expected))
	}

	for i, exp := range expected {
		line := result.Lines[i]
		if line.Op != exp.op || line.Text != exp.text {
			t.Fatalf("Context interleaving: line %d Op=%c Text=%q, want Op=%c Text=%q",
				i, line.Op, line.Text, exp.op, exp.text)
		}
	}
}

func TestHunksElidesDistantContext(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("line%02d", i))
	}
	old := strings.Join(lines, "\n")
	changed := append([]string(nil), lines...)
	changed[20] = "CHANGED"
	new := strings.Join(changed, "\n")

	hunks := Hunks(Compute(old, new).Lines, 3)

	added, removed := Stats(hunks)
	if added != 1 || removed != 1 {
		t.Fatalf("Hunks Stats = (%d, %d), want (1, 1)", added, removed)
	}
	var gaps, context int
	for _, l := range hunks {
		switch l.Op {
		case GapOp:
			gaps++
		case ' ':
			context++
		}
	}
	if gaps != 2 {
		t.Fatalf("expected a leading and trailing gap marker, got %d", gaps)
	}
	if context != 6 {
		t.Fatalf("expected 2*context unchanged lines kept, got %d", context)
	}
}

func TestHunksNoChangeReturnsNil(t *testing.T) {
	if got := Hunks(Compute("a\nb\nc", "a\nb\nc").Lines, 3); got != nil {
		t.Fatalf("Hunks with no change = %v, want nil", got)
	}
}

func TestHunksKeepsSmallDiffIntact(t *testing.T) {
	hunks := Hunks(Compute("a\nb\nc", "a\nB\nc").Lines, 3)
	for _, l := range hunks {
		if l.Op == GapOp {
			t.Fatalf("small diff should have no gap markers, got %v", hunks)
		}
	}
	if len(hunks) != 4 {
		t.Fatalf("small diff Hunks = %d lines, want 4", len(hunks))
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
