package widgets

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestPlanetDimensions(t *testing.T) {
	plain := lipgloss.NewStyle()
	lines := strings.Split(Planet(0, plain, plain), "\n")
	if len(lines) != planetH {
		t.Fatalf("planet has %d rows, want %d", len(lines), planetH)
	}
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w != planetW {
			t.Fatalf("row %d width %d, want %d", i, w, planetW)
		}
	}
}

func TestPlanetMoonStartsAtTop(t *testing.T) {
	plain := lipgloss.NewStyle()
	lines := strings.Split(Planet(0, plain, plain), "\n")
	top := []rune(lines[0])
	if top[int(planetCX)] != moonGlyph {
		t.Fatalf("frame 0 moon not at top center; row0 = %q", lines[0])
	}
	if strings.TrimSpace(lines[0]) != string(moonGlyph) {
		t.Fatalf("top row should hold only the moon, got %q", lines[0])
	}
}

func TestPlanetAnimates(t *testing.T) {
	plain := lipgloss.NewStyle()
	if Planet(0, plain, plain) == Planet(orbitSteps/4, plain, plain) {
		t.Fatal("moon position should change across frames")
	}
	if Planet(0, plain, plain) != Planet(orbitSteps, plain, plain) {
		t.Fatal("orbit should repeat every OrbitSteps frames")
	}
}

func TestPlanetColorsMoon(t *testing.T) {
	body := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	moon := lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	if !strings.Contains(Planet(0, body, moon), "\x1b[") {
		t.Fatal("styled planet should emit ANSI color sequences")
	}
}
