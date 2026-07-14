package widgets

import (
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// Planet banner canvas: a static planet centered in the grid with a dot tracing
// an elliptical orbit around it. The horizontal radii are ~3x the vertical ones
// so the circle and orbit read as round despite tall terminal cells.
const (
	planetW    = 27
	planetH    = 7
	planetCX   = 13.0
	planetCY   = 3.0
	orbitRX    = 12.0
	orbitRY    = 3.0
	orbitSteps = 48
	moonGlyph  = '●'
)

// planetArt is the static planet: a ringed sphere with a bright core, stamped
// centered in the canvas.
var planetArt = []string{
	" .-~-. ",
	"/     \\",
	"|  ●  |",
	"\\     /",
	" '-~-' ",
}

// OrbitSteps is the number of frames in one full orbit; frame counters wrap here.
const OrbitSteps = orbitSteps

// Planet renders one frame of the orbiting-planet banner as styled ASCII art.
// frame advances the orbit; body styles the planet, moon the orbiting dot.
func Planet(frame int, body, moon lipgloss.Style) string {
	grid := make([][]rune, planetH)
	for i := range grid {
		grid[i] = []rune(strings.Repeat(" ", planetW))
	}
	top := int(planetCY) - len(planetArt)/2
	for r, line := range planetArt {
		row := top + r
		if row < 0 || row >= planetH {
			continue
		}
		art := []rune(line)
		left := int(planetCX) - len(art)/2
		for c, ch := range art {
			col := left + c
			if col < 0 || col >= planetW || ch == ' ' {
				continue
			}
			grid[row][col] = ch
		}
	}
	// Start the dot at the top, like the logo, then sweep clockwise.
	theta := 2*math.Pi*float64(((frame%orbitSteps)+orbitSteps)%orbitSteps)/orbitSteps - math.Pi/2
	mx := int(math.Round(planetCX + orbitRX*math.Cos(theta)))
	my := int(math.Round(planetCY + orbitRY*math.Sin(theta)))

	var b strings.Builder
	for r := 0; r < planetH; r++ {
		if r > 0 {
			b.WriteByte('\n')
		}
		line := grid[r]
		if r == my && mx >= 0 && mx < planetW {
			b.WriteString(body.Render(string(line[:mx])))
			b.WriteString(moon.Render(string(moonGlyph)))
			b.WriteString(body.Render(string(line[mx+1:])))
			continue
		}
		b.WriteString(body.Render(string(line)))
	}
	return b.String()
}
