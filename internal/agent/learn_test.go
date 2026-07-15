package agent

import (
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/llm"
)

func TestSetLearnModeTogglesSystemOverlay(t *testing.T) {
	a := newTestAgent(t)

	if a.LearnMode() {
		t.Fatal("learn mode should start off")
	}
	if strings.Contains(a.transcript[0].Content, "Learn mode") {
		t.Fatal("base system message should not carry the overlay")
	}

	a.SetLearnMode(true)
	if !a.LearnMode() {
		t.Fatal("LearnMode() should report on after SetLearnMode(true)")
	}
	if !strings.Contains(a.transcript[0].Content, "Learn mode") {
		t.Fatalf("system message missing overlay after enabling:\n%s", a.transcript[0].Content)
	}
	if !strings.HasPrefix(a.transcript[0].Content, "test") {
		t.Fatal("overlay should append to the base prompt, not replace it")
	}

	a.SetLearnMode(false)
	if a.LearnMode() {
		t.Fatal("LearnMode() should report off after SetLearnMode(false)")
	}
	if a.transcript[0].Content != "test" {
		t.Fatalf("disabling should restore the base prompt, got %q", a.transcript[0].Content)
	}
}

func TestLearnModePersistsAcrossReset(t *testing.T) {
	a := newTestAgent(t)
	a.SetLearnMode(true)

	a.Reset()

	if !a.LearnMode() {
		t.Fatal("learn mode should survive Reset")
	}
	if !strings.Contains(a.transcript[0].Content, "Learn mode") {
		t.Fatal("reinstated system message should carry the overlay while learn mode is on")
	}
}

func TestLearnModeAppliedOnLoad(t *testing.T) {
	a := newTestAgent(t)
	a.SetLearnMode(true)

	a.Load([]llm.Message{{Role: llm.RoleUser, Content: "hi"}})

	if !strings.Contains(a.transcript[0].Content, "Learn mode") {
		t.Fatal("loaded transcript's system message should carry the overlay while learn mode is on")
	}
}
