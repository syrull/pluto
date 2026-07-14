package widgets

import (
	"strings"
	"testing"
)

func TestFuzzyScoreSubsequence(t *testing.T) {
	if _, ok := fuzzyScore("abc", "xaxbxc"); !ok {
		t.Fatal("abc should match xaxbxc as a subsequence")
	}
	if _, ok := fuzzyScore("cb", "abc"); ok {
		t.Fatal("cb is not a subsequence of abc")
	}
	if _, ok := fuzzyScore("", "anything"); !ok {
		t.Fatal("empty pattern should match")
	}
}

func TestFuzzyScoreCaseInsensitive(t *testing.T) {
	if _, ok := fuzzyScore("MODEL", "internal/tui/model.go"); !ok {
		t.Fatal("match should be case-insensitive")
	}
}

func TestFuzzyScoreRanksConsecutiveHigher(t *testing.T) {
	consecutive, ok1 := fuzzyScore("abc", "abcxyz")
	scattered, ok2 := fuzzyScore("abc", "axbxc")
	if !ok1 || !ok2 {
		t.Fatal("both should match")
	}
	if consecutive <= scattered {
		t.Fatalf("consecutive (%d) should outrank scattered (%d)", consecutive, scattered)
	}
}

func TestFuzzyScoreRanksBoundaryHigher(t *testing.T) {
	boundary, ok1 := fuzzyScore("m", "a/model.go")
	mid, ok2 := fuzzyScore("m", "aXmodel.go")
	if !ok1 || !ok2 {
		t.Fatal("both should match")
	}
	if boundary <= mid {
		t.Fatalf("boundary hit (%d) should outrank mid-word hit (%d)", boundary, mid)
	}
}

func testFuzzy(items []string) *FuzzyPicker {
	return NewFuzzyPicker("find file", items, ListStyle{})
}

func TestFuzzyPickerEmptyQueryShowsAll(t *testing.T) {
	items := []string{"a.go", "b.go", "c.go"}
	p := testFuzzy(items)
	if len(p.match) != 3 {
		t.Fatalf("empty query should show all items, got %d", len(p.match))
	}
	if sel, ok := p.Selected(); !ok || sel != "a.go" {
		t.Fatalf("Selected() = %q,%v want a.go,true", sel, ok)
	}
}

func TestFuzzyPickerFiltersAndRanks(t *testing.T) {
	items := []string{"internal/tui/model.go", "cmd/main.go", "internal/tui/tree.go"}
	p := testFuzzy(items)
	p.Insert("model")
	if len(p.match) != 1 || p.match[0] != "internal/tui/model.go" {
		t.Fatalf("query %q matched %v, want [internal/tui/model.go]", p.Query(), p.match)
	}
}

func TestFuzzyPickerBackspaceRestores(t *testing.T) {
	items := []string{"alpha.go", "beta.go"}
	p := testFuzzy(items)
	p.Insert("alp")
	if len(p.match) != 1 {
		t.Fatalf("after typing 'alp' match = %d, want 1", len(p.match))
	}
	p.Backspace()
	p.Backspace()
	p.Backspace()
	if p.Query() != "" {
		t.Fatalf("Query() = %q, want empty after backspacing", p.Query())
	}
	if len(p.match) != 2 {
		t.Fatalf("empty query should show all, got %d", len(p.match))
	}
}

func TestFuzzyPickerInsertResetsCursor(t *testing.T) {
	items := []string{"a.go", "ab.go", "abc.go"}
	p := testFuzzy(items)
	p.Down()
	p.Down()
	if p.cursor != 2 {
		t.Fatalf("cursor = %d, want 2 after two Down()", p.cursor)
	}
	p.Insert("a")
	if p.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 after typing (results changed)", p.cursor)
	}
}

func TestFuzzyPickerNoMatchSelectedFalse(t *testing.T) {
	p := testFuzzy([]string{"a.go", "b.go"})
	p.Insert("zzz")
	if _, ok := p.Selected(); ok {
		t.Fatal("Selected() should be false when nothing matches")
	}
}

func TestFuzzyPickerViewShowsQueryAndMatches(t *testing.T) {
	p := testFuzzy([]string{"internal/model.go", "cmd/main.go"})
	p.SetSize(80, 24)
	p.Insert("main")
	view := p.View()
	if !strings.Contains(view, "find file") {
		t.Fatalf("view missing title:\n%s", view)
	}
	if !strings.Contains(view, "main") {
		t.Fatalf("view missing query/match text:\n%s", view)
	}
	if !strings.Contains(view, "▸") {
		t.Fatalf("view missing cursor marker:\n%s", view)
	}
}
