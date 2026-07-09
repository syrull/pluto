package agent

import (
	"testing"

	"github.com/pluto/harness/internal/llm"
	"github.com/pluto/harness/internal/llm/anthropic"
	"github.com/pluto/harness/internal/tool"
)

func TestSwitcherExposesAnthropicModelPicking(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key") // let anthropic.New authenticate

	p, err := anthropic.New(anthropic.DefaultModel)
	if err != nil {
		t.Fatalf("anthropic.New: %v", err)
	}
	ag := New(p, tool.NewRegistry(), "")

	sw, ok := ag.Switcher()
	if !ok {
		t.Fatal("anthropic-backed agent should expose a Switcher")
	}
	if len(sw.Available()) == 0 {
		t.Fatal("Available() returned no models")
	}
	if sw.Model() != anthropic.DefaultModel {
		t.Fatalf("Model() = %q, want default %q", sw.Model(), anthropic.DefaultModel)
	}

	target := pickOther(sw.Available(), sw.Model())
	sw.SetModel(target)
	if sw.Model() != target {
		t.Fatalf("after SetModel(%q), Model() = %q", target, sw.Model())
	}
	if want := "anthropic/" + target; ag.ProviderName() != want {
		t.Fatalf("ProviderName() = %q, want %q", ag.ProviderName(), want)
	}
}

func TestStubIsNotSwitchable(t *testing.T) {
	ag := New(llm.Stub{}, tool.NewRegistry(), "")
	if _, ok := ag.Switcher(); ok {
		t.Fatal("stub provider must not be Switchable")
	}
}

func TestThinkerExposesAnthropicLevels(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	p, err := anthropic.New(anthropic.DefaultModel)
	if err != nil {
		t.Fatalf("anthropic.New: %v", err)
	}
	ag := New(p, tool.NewRegistry(), "")

	th, ok := ag.Thinker()
	if !ok {
		t.Fatal("anthropic-backed agent should expose a Thinker")
	}
	if !th.ThinkLevel().Thinking() {
		t.Fatal("expected thinking enabled by default")
	}
	if len(th.ThinkLevels()) == 0 || th.ThinkLevels()[0] != llm.ThinkNone {
		t.Fatalf("ThinkLevels() = %v, want non-empty leading with none", th.ThinkLevels())
	}
	th.SetThinkLevel(llm.ThinkNone)
	if th.ThinkLevel().Thinking() {
		t.Fatal("expected thinking disabled after SetThinkLevel(off)")
	}
	th.SetThinkLevel(llm.ThinkHigh)
	if th.ThinkLevel() != llm.ThinkHigh {
		t.Fatalf("ThinkLevel = %q, want high", th.ThinkLevel())
	}
}

func TestStubIsNotThinkable(t *testing.T) {
	ag := New(llm.Stub{}, tool.NewRegistry(), "")
	if _, ok := ag.Thinker(); ok {
		t.Fatal("stub provider must not be Thinkable")
	}
}

func pickOther(models []string, current string) string {
	for _, m := range models {
		if m != current {
			return m
		}
	}
	return current
}
