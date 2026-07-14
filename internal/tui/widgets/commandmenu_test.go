package widgets

import (
	"strings"
	"testing"
)

func testCommandMenu() *CommandMenu {
	return NewCommandMenu([]Command{
		{Name: "/new", Desc: "start a new conversation"},
		{Name: "/model", Args: "[name]", Desc: "switch the active model"},
		{Name: "/think", Args: "[level]", Desc: "set the thinking level"},
	}, ListStyle{})
}

func TestCommandMenuEmptyQueryShowsAll(t *testing.T) {
	m := testCommandMenu()
	if m.Len() != 3 {
		t.Fatalf("Len() = %d, want 3 (all commands with empty query)", m.Len())
	}
}

func TestCommandMenuPrefixFilter(t *testing.T) {
	m := testCommandMenu()

	m.Filter("/mo")
	if m.Len() != 1 {
		t.Fatalf("Len() after /mo = %d, want 1", m.Len())
	}
	if c, ok := m.Selected(); !ok || c.Name != "/model" {
		t.Fatalf("Selected() after /mo = %q,%v want /model,true", c.Name, ok)
	}

	// A leading slash is optional and matching is case-insensitive.
	m.Filter("TH")
	if c, ok := m.Selected(); !ok || c.Name != "/think" {
		t.Fatalf("Selected() after TH = %q,%v want /think,true", c.Name, ok)
	}

	m.Filter("/zzz")
	if m.Len() != 0 {
		t.Fatalf("Len() after /zzz = %d, want 0", m.Len())
	}
	if _, ok := m.Selected(); ok {
		t.Fatal("Selected() should be false when nothing matches")
	}
}

func TestCommandMenuFilterResetsCursor(t *testing.T) {
	m := testCommandMenu()
	m.Down()
	m.Down()
	m.Filter("/")
	if c, _ := m.Selected(); c.Name != "/new" {
		t.Fatalf("filter should reset the highlight to the top, got %q", c.Name)
	}
}

func TestCommandMenuNavigation(t *testing.T) {
	m := testCommandMenu()

	m.Down()
	if c, _ := m.Selected(); c.Name != "/model" {
		t.Fatalf("after Down: %q, want /model", c.Name)
	}
	m.Up()
	if c, _ := m.Selected(); c.Name != "/new" {
		t.Fatalf("after Up: %q, want /new", c.Name)
	}
	m.Up() // clamps at top
	if c, _ := m.Selected(); c.Name != "/new" {
		t.Fatalf("Up should clamp at the first row, got %q", c.Name)
	}
}

func TestCommandMenuCycleWraps(t *testing.T) {
	m := testCommandMenu()
	m.Cycle()
	m.Cycle()
	if c, _ := m.Selected(); c.Name != "/think" {
		t.Fatalf("after two Cycle: %q, want /think", c.Name)
	}
	m.Cycle() // wraps back to the top
	if c, _ := m.Selected(); c.Name != "/new" {
		t.Fatalf("Cycle should wrap to the first row, got %q", c.Name)
	}
}

func TestCommandMenuViewShowsNameArgsAndDesc(t *testing.T) {
	m := testCommandMenu()
	view := m.View(60, 10)

	for _, want := range []string{"/model", "[name]", "switch the active model"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "▸") {
		t.Fatalf("view missing highlight marker:\n%s", view)
	}
	if n := strings.Count(view, "▸"); n != 1 {
		t.Fatalf("view has %d highlight markers, want 1:\n%s", n, view)
	}
}

func TestCommandMenuViewEmptyWhenNoMatch(t *testing.T) {
	m := testCommandMenu()
	m.Filter("/nope")
	if got := m.View(60, 10); got != "" {
		t.Fatalf("View with no matches = %q, want empty", got)
	}
}
