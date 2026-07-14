package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestRenderThinkBoxSpansContentWidth(t *testing.T) {
	m := &model{width: 100}
	out := m.renderThinkBox("reasoning about the problem")
	if got, want := lipgloss.Width(out), m.contentWidth(); got != want {
		t.Fatalf("think box width = %d, want %d (full pane interior)", got, want)
	}
}
