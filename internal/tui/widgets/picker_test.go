package widgets

import (
	"strings"
	"testing"
)

func testPicker(items []string, active string) *ListPicker {
	return NewListPicker("select model — pick one", items, active, ListStyle{})
}

func TestNewListPickerCursorOnActive(t *testing.T) {
	items := []string{"gpt-4", "gpt-3.5", "claude"}
	p := testPicker(items, "gpt-3.5")

	if p.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (index of gpt-3.5)", p.cursor)
	}
	if p.Selected() != "gpt-3.5" {
		t.Fatalf("Selected() = %q, want %q", p.Selected(), "gpt-3.5")
	}
}

func TestNewListPickerCursorDefaultsToZero(t *testing.T) {
	items := []string{"a", "b", "c"}

	p := testPicker(items, "z")
	if p.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (active not found)", p.cursor)
	}

	p = testPicker(items, "")
	if p.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (empty active)", p.cursor)
	}
}

func TestUpClampAtZero(t *testing.T) {
	p := testPicker([]string{"a", "b", "c"}, "c") // cursor = 2

	p.Up()
	if p.cursor != 1 {
		t.Fatalf("after first Up(): cursor = %d, want 1", p.cursor)
	}
	p.Up()
	if p.cursor != 0 {
		t.Fatalf("after second Up(): cursor = %d, want 0", p.cursor)
	}
	p.Up()
	if p.cursor != 0 {
		t.Fatalf("after third Up(): cursor = %d, want 0 (clamped)", p.cursor)
	}
}

func TestDownClampAtLast(t *testing.T) {
	p := testPicker([]string{"a", "b", "c"}, "a") // cursor = 0

	p.Down()
	if p.cursor != 1 {
		t.Fatalf("after first Down(): cursor = %d, want 1", p.cursor)
	}
	p.Down()
	if p.cursor != 2 {
		t.Fatalf("after second Down(): cursor = %d, want 2", p.cursor)
	}
	p.Down()
	if p.cursor != 2 {
		t.Fatalf("after third Down(): cursor = %d, want 2 (clamped)", p.cursor)
	}
}

func TestSelectedReturnsActiveRow(t *testing.T) {
	p := testPicker([]string{"gpt-4", "gpt-3.5", "claude-opus"}, "gpt-4")

	if p.Selected() != "gpt-4" {
		t.Fatalf("Selected() = %q, want %q", p.Selected(), "gpt-4")
	}
	p.Down()
	if p.Selected() != "gpt-3.5" {
		t.Fatalf("after Down(): Selected() = %q, want %q", p.Selected(), "gpt-3.5")
	}
	p.Down()
	if p.Selected() != "claude-opus" {
		t.Fatalf("after Down(): Selected() = %q, want %q", p.Selected(), "claude-opus")
	}
	p.Up()
	if p.Selected() != "gpt-3.5" {
		t.Fatalf("after Up(): Selected() = %q, want %q", p.Selected(), "gpt-3.5")
	}
}

func TestViewIncludesTitleAndMarkerOnCursorRow(t *testing.T) {
	p := testPicker([]string{"model-a", "model-b", "model-c"}, "model-b") // cursor = 1

	view := p.View()

	if !strings.Contains(view, "select model") {
		t.Fatalf("view missing title:\n%s", view)
	}
	if !strings.Contains(view, "▸") || !strings.Contains(view, "model-b") {
		t.Fatalf("view missing cursor marker or cursor item:\n%s", view)
	}
	if !strings.Contains(view, "model-a") || !strings.Contains(view, "model-c") {
		t.Fatalf("view missing non-cursor rows:\n%s", view)
	}
	if n := strings.Count(view, "▸"); n != 1 {
		t.Fatalf("view has %d cursor markers, want 1:\n%s", n, view)
	}
}
