package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/session"
	"github.com/syrull/pluto/internal/skills"
)

// dbgCTF is the debug component tag for CTF mode transitions and RoE decisions.
const dbgCTF = "ctf"

// ctfBadge is the persistent status-line label shown while CTF mode is active.
const ctfBadge = " CTF "

// ctfController is the subset of the review gate the TUI toggles for the CTF
// scope-aware rules of engagement (implemented by policy.ReviewGate).
type ctfController interface{ SetCTFMode(on bool) }

// handleCTFCommand dispatches /ctf: no argument toggles the mode, on|off set it
// explicitly, and status shows the engagement state.
func (m *model) handleCTFCommand(line string) (string, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) > 1 {
		switch strings.ToLower(fields[1]) {
		case "on":
			return m.applyCTF(true), nil
		case "off":
			return m.applyCTF(false), nil
		case "status":
			return m.ctfStatus(), nil
		default:
			return styleErr.Render("✗ usage: /ctf [on|off|status]"), nil
		}
	}
	return m.applyCTF(!m.ctf), nil
}

// applyCTF switches CTF mode on or off across every surface the mode owns: the
// red theme, the embedded CTF skill set, each workspace agent's operator overlay,
// and the gate's scope-aware rules of engagement. It is idempotent — a no-op
// call just shows the current status.
func (m *model) applyCTF(on bool) string {
	if on == m.ctf {
		return m.ctfStatus()
	}
	m.ctf = on
	if on {
		setTheme(themeCTF)
	} else {
		setTheme(themeDefault)
	}
	skills.SetCTFMode(on)
	// Apply the operator overlay to every agent: the live one plus each
	// workspace's (deduped, since the active workspace mirrors m.agent).
	seen := make(map[*agent.Agent]bool)
	apply := func(a *agent.Agent) {
		if a == nil || seen[a] {
			return
		}
		seen[a] = true
		a.SetCTFMode(on)
	}
	apply(m.agent)
	for _, w := range m.workspaces {
		if w != nil {
			apply(w.agent)
		}
	}
	agents := len(seen)
	roe := m.setCTFRoE(on)
	debug.Info(dbgCTF, "mode toggled", "on", on, "agents", agents, "roe_wired", roe)
	if on {
		m.notice = "⚑ CTF mode on — red theme, CTF operator prompt, parallel fan-out default"
		return m.renderCTFBanner(true, roe)
	}
	m.notice = "✓ CTF mode off — back to the default harness"
	return m.renderCTFBanner(false, roe)
}

// setCTFRoE toggles the gate's scope-aware rules of engagement when the active
// agent's gate supports it. It returns whether an RoE gate was wired.
func (m *model) setCTFRoE(on bool) bool {
	if m.agent == nil {
		return false
	}
	ctrl, ok := m.agent.Auto()
	if !ok {
		return false
	}
	cc, ok := ctrl.(ctfController)
	if !ok {
		return false
	}
	cc.SetCTFMode(on)
	return true
}

// renderCTFBanner is the transcript banner shown when the mode flips.
func (m *model) renderCTFBanner(on, roe bool) string {
	if !on {
		return styleHint.Render("✓ CTF mode off — standard theme and coding persona restored")
	}
	var b strings.Builder
	b.WriteString(styleStatusCTF.Render(ctfBadge) + " " + styleWorking.Render("engagement mode active"))
	b.WriteByte('\n')
	b.WriteString(styleHint.Render("Recon -> foothold -> loot -> privesc -> flags. Fan out with workers; record every finding on the blackboard."))
	if roe {
		b.WriteByte('\n')
		b.WriteString(styleHint.Render("Rules of engagement: authorized in-scope actions fast-path; out-of-scope and destructive ones still escalate. Scope via PLUTO_CTF_SCOPE."))
	}
	return b.String()
}

// ctfStatus renders the /ctf status block: the mode state plus the engagement
// blackboard folded from the shared worker pool board (hosts/services/creds/
// footholds/vulns/flags), so the operator sees the whole engagement at a glance.
func (m *model) ctfStatus() string {
	var b strings.Builder
	if m.ctf {
		fmt.Fprintf(&b, "%s %s\n", styleStatusCTF.Render(ctfBadge), styleWorking.Render("CTF mode active"))
	} else {
		fmt.Fprintf(&b, "%s\n", styleHint.Render("CTF mode off — /ctf on to enter the engagement workflow"))
	}
	if m.pool == nil {
		b.WriteString(styleHint.Render("engagement blackboard unavailable (no worker pool in this build)"))
		return b.String()
	}
	st := m.pool.Board().State()
	fmt.Fprintf(&b, "%s hosts %d · services %d · creds %d · footholds %d · vulns %d · flags %d",
		styleHint.Render("blackboard:"),
		len(st.Hosts), len(st.Services), len(st.Creds), len(st.Footholds), len(st.Vulns), len(st.Flags))
	// Surface flags prominently, then a few footholds/creds for context.
	for _, f := range st.Flags {
		fmt.Fprintf(&b, "\n%s %s", styleDone.Render("⚑ flag"), truncCells(oneLine(f.Value), 200))
	}
	appendFacts(&b, "foothold", st.Footholds, 5)
	appendFacts(&b, "cred", st.Creds, 5)
	if total := len(st.Hosts) + len(st.Services) + len(st.Creds) + len(st.Footholds) + len(st.Vulns) + len(st.Flags); total == 0 {
		fmt.Fprintf(&b, "\n%s", styleHint.Render("no findings recorded yet — dispatch recon workers to populate it"))
	}
	return b.String()
}

// appendFacts writes up to max facts of one kind under a dim label.
func appendFacts(b *strings.Builder, label string, facts []session.Fact, max int) {
	for i, f := range facts {
		if i >= max {
			fmt.Fprintf(b, "\n%s", styleHint.Render(fmt.Sprintf("  … +%d more %s", len(facts)-max, label)))
			break
		}
		fmt.Fprintf(b, "\n%s %s", styleHint.Render(label+":"), truncCells(oneLine(f.Value), 180))
	}
}

// ctfChip renders the persistent status-line CTF badge, or empty when off.
func ctfChip(on bool) (string, string) {
	if !on {
		return "", ""
	}
	return styleStatusCTF.Render(ctfBadge), ctfBadge
}
