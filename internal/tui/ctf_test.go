package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/mcp"
	"github.com/syrull/pluto/internal/mode"
	"github.com/syrull/pluto/internal/session"
	"github.com/syrull/pluto/internal/skills"
	"github.com/syrull/pluto/internal/tool"
	"github.com/syrull/pluto/internal/worker"
)

// resetTheme restores global theme/skill state so a CTF test never leaks into
// another (the theme and embedded-skill toggle are process-wide).
func resetTheme(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		setTheme(themeDefault)
		skills.SetCTFMode(false)
	})
}

func TestCTFCommandTogglesMode(t *testing.T) {
	resetTheme(t)
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "base")
	m := model{agent: ag, md: newRenderer(80), input: newInput(80)}

	m.handleCommand("/ctf")
	if !m.ctf {
		t.Fatal("/ctf should turn CTF mode on")
	}
	if activeTheme != themeCTF {
		t.Fatalf("theme = %q, want ctf", activeTheme)
	}
	if !ag.CTFMode() {
		t.Fatal("agent should carry the CTF overlay after /ctf")
	}
	if !skills.CTFMode() {
		t.Fatal("embedded CTF skills should be active after /ctf")
	}

	m.handleCommand("/ctf")
	if m.ctf {
		t.Fatal("second /ctf should turn CTF mode off")
	}
	if activeTheme != themeDefault {
		t.Fatalf("theme = %q, want default after toggle off", activeTheme)
	}
	if ag.CTFMode() {
		t.Fatal("agent overlay should be removed after toggling off")
	}
}

func TestCTFOnOffExplicit(t *testing.T) {
	resetTheme(t)
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "base")
	m := model{agent: ag, md: newRenderer(80), input: newInput(80)}

	m.handleCommand("/ctf on")
	if !m.ctf {
		t.Fatal("/ctf on should enable")
	}
	// Idempotent: /ctf on again just shows status, stays on.
	out, _ := m.handleCommand("/ctf on")
	if !m.ctf {
		t.Fatal("/ctf on while on should stay on")
	}
	if !strings.Contains(out, "CTF") {
		t.Fatalf("idempotent /ctf on should show status, got %q", out)
	}

	m.handleCommand("/ctf off")
	if m.ctf {
		t.Fatal("/ctf off should disable")
	}
}

func TestCTFBadgeInStatusLine(t *testing.T) {
	resetTheme(t)
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var tm tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80), ctf: true}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := tm.(model)
	if !strings.Contains(m.modelStatus(), "CTF") {
		t.Fatalf("status line should show the CTF badge when active:\n%s", m.modelStatus())
	}

	m.ctf = false
	if strings.Contains(m.modelStatus(), "CTF") {
		t.Fatal("status line should not show the CTF badge when inactive")
	}
}

func TestCTFStatusShowsBlackboard(t *testing.T) {
	resetTheme(t)
	pool := worker.NewPool(context.Background(), worker.Config{
		Provider: llm.Stub{},
		Registry: tool.NewRegistry(),
	})
	board := pool.Board()
	board.Append("w1", session.FactHost, "10.0.0.5", "")
	board.Append("w1", session.FactService, "10.0.0.5:22 ssh", "OpenSSH")
	board.Append("w1", session.FactCred, "root:toor", "reused")
	board.Append("w1", session.FactFlag, "flag{pwned}", "web root")

	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var tm tea.Model = model{agent: ag, pool: pool, md: newRenderer(80), input: newInput(80), ctf: true}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := tm.(model)

	out := m.ctfStatus()
	for _, want := range []string{"hosts 1", "services 1", "creds 1", "flags 1", "flag{pwned}"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ctf status missing %q:\n%s", want, out)
		}
	}
}

func TestCTFStartupModeSeedsTheme(t *testing.T) {
	resetTheme(t)
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	// New applies the theme when launched in CTF mode.
	_ = New(ag, func() *agent.Agent { return ag }, nil, nil, nil, nil, mcp.Summary{}, nil, mode.CTF)
	if activeTheme != themeCTF {
		t.Fatalf("launching in CTF mode should seed the red theme, got %q", activeTheme)
	}
}
