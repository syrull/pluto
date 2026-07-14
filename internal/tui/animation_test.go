package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestOrbitTickAdvancesWhileHome(t *testing.T) {
	var tm tea.Model = newDashModel()
	tm, cmd := tm.Update(orbitTickMsg{epoch: 0})
	got := tm.(model)
	if got.orbitFrame != 1 {
		t.Fatalf("orbitFrame = %d, want 1", got.orbitFrame)
	}
	if cmd == nil {
		t.Fatal("tick should reschedule the next frame while home")
	}
}

func TestOrbitTickStopsWhenNotHome(t *testing.T) {
	m := newDashModel()
	m.showHome = false
	var tm tea.Model = m
	tm, cmd := tm.Update(orbitTickMsg{epoch: 0})
	if got := tm.(model); got.orbitFrame != 0 {
		t.Fatalf("orbitFrame = %d, want 0 (paused off home)", got.orbitFrame)
	}
	if cmd != nil {
		t.Fatal("tick should not reschedule when not home")
	}
}

func TestOrbitTickIgnoresStaleEpoch(t *testing.T) {
	m := newDashModel()
	m.orbitEpoch = 2
	var tm tea.Model = m
	tm, cmd := tm.Update(orbitTickMsg{epoch: 1})
	if got := tm.(model); got.orbitFrame != 0 {
		t.Fatalf("orbitFrame = %d, want 0 (stale loop ignored)", got.orbitFrame)
	}
	if cmd != nil {
		t.Fatal("stale tick should not reschedule")
	}
}
