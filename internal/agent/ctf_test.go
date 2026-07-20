package agent

import (
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/llm"
)

func TestSetCTFModeTogglesSystemOverlay(t *testing.T) {
	a := newTestAgent(t)

	if a.CTFMode() {
		t.Fatal("CTF mode should start off")
	}
	if strings.Contains(a.transcript[0].Content, "CTF mode") {
		t.Fatal("base system message should not carry the CTF overlay")
	}

	a.SetCTFMode(true)
	if !a.CTFMode() {
		t.Fatal("CTFMode() should report on after SetCTFMode(true)")
	}
	if !strings.Contains(a.transcript[0].Content, "CTF mode") {
		t.Fatalf("system message missing CTF overlay after enabling:\n%s", a.transcript[0].Content)
	}
	if !strings.HasPrefix(a.transcript[0].Content, "test") {
		t.Fatal("overlay should append to the base prompt, not replace it")
	}

	a.SetCTFMode(false)
	if a.CTFMode() {
		t.Fatal("CTFMode() should report off after SetCTFMode(false)")
	}
	if a.transcript[0].Content != "test" {
		t.Fatalf("disabling should restore the base prompt, got %q", a.transcript[0].Content)
	}
}

func TestCTFOverlayComposesWithLearn(t *testing.T) {
	a := newTestAgent(t)
	a.SetLearnMode(true)
	a.SetCTFMode(true)
	got := a.transcript[0].Content
	if !strings.Contains(got, "Learn mode") || !strings.Contains(got, "CTF mode") {
		t.Fatalf("both overlays should be present, got:\n%s", got)
	}
}

func TestCTFOverlayCoversEngagementLoop(t *testing.T) {
	for _, want := range []string{"blackboard", "parallel", "credential", "scope", "flag"} {
		if !strings.Contains(strings.ToLower(ctfOverlay), want) {
			t.Fatalf("CTF overlay missing %q, got:\n%s", want, ctfOverlay)
		}
	}
}

func TestCTFModePersistsAcrossReset(t *testing.T) {
	a := newTestAgent(t)
	a.SetCTFMode(true)

	a.Reset()

	if !a.CTFMode() {
		t.Fatal("CTF mode should survive Reset")
	}
	if !strings.Contains(a.transcript[0].Content, "CTF mode") {
		t.Fatal("reinstated system message should carry the CTF overlay while CTF mode is on")
	}
}

func TestCTFModeAppliedOnLoad(t *testing.T) {
	a := newTestAgent(t)
	a.SetCTFMode(true)

	a.Load([]llm.Message{{Role: llm.RoleUser, Content: "hi"}})

	if !strings.Contains(a.transcript[0].Content, "CTF mode") {
		t.Fatal("loaded transcript's system message should carry the CTF overlay while CTF mode is on")
	}
}
