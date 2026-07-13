package widgets

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestDashboardViewRendersBannerRowsLinesAndFooter(t *testing.T) {
	d := Dashboard{
		Banner: "PLUTO",
		Rows: []DashRow{
			{Label: "dir", Value: "/tmp/x"},
			{Label: "branch", Value: "main"},
		},
		Lines:  []string{"/new /dash"},
		Footer: "tip: be nice",
	}

	got := d.View()
	for _, want := range []string{"PLUTO", "dir", "/tmp/x", "branch", "main", "/new /dash", "tip: be nice"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dashboard view missing %q:\n%s", want, got)
		}
	}
}

func TestDashboardTableKeepsRightBorder(t *testing.T) {
	rows := []DashRow{
		{Label: "last commit", Value: "e8f784d docs: remove sessions section (2 hours ago)"},
		{Label: "provider", Value: "anthropic"},
	}
	for w := 40; w <= 100; w++ {
		out := Dashboard{Width: w, Rows: rows}.View()
		if !strings.Contains(out, "╮") || !strings.Contains(out, "╯") {
			t.Fatalf("width %d dropped the table right border:\n%s", w, out)
		}
	}
}

func TestDashboardViewClipsToWidth(t *testing.T) {
	d := Dashboard{
		Width: 20,
		Lines: []string{strings.Repeat("x", 100)},
	}
	for _, ln := range strings.Split(d.View(), "\n") {
		if w := lipgloss.Width(ln); w > 20 {
			t.Fatalf("line width %d exceeds 20:\n%q", w, ln)
		}
	}
}

func TestDashboardViewOmitsEmptyBannerAndFooter(t *testing.T) {
	d := Dashboard{Rows: []DashRow{{Label: "k", Value: "v"}}}
	got := d.View()
	if strings.HasPrefix(got, "\n") || strings.HasSuffix(got, "\n") {
		t.Fatalf("view should be trimmed, got %q", got)
	}
	if !strings.Contains(got, "k") {
		t.Fatalf("view missing row:\n%s", got)
	}
}
