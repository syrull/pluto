package policy

import (
	"net/netip"
	"os"
	"regexp"
	"strings"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/debug"
)

// Engagement is the CTF rules-of-engagement manifest the scope-aware fast-path
// consults. Scope is the set of authorized network prefixes; an empty scope
// means no CIDR restriction (a lab CTF), in which case the action-class
// allowlist alone gates what is fast-pathed. It is loaded from PLUTO_CTF_SCOPE
// (comma-separated CIDRs or bare IPs).
type Engagement struct {
	Scope []netip.Prefix
}

// LoadEngagement parses the CTF scope manifest from PLUTO_CTF_SCOPE. Malformed
// entries are skipped with a debug note so a typo degrades to "unscoped" rather
// than failing startup.
func LoadEngagement() Engagement {
	var e Engagement
	raw := strings.TrimSpace(os.Getenv("PLUTO_CTF_SCOPE"))
	if raw == "" {
		return e
	}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			e.Scope = append(e.Scope, p)
			continue
		}
		if a, err := netip.ParseAddr(part); err == nil {
			e.Scope = append(e.Scope, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		debug.Warn("ctf", "engagement scope entry ignored", "entry", part)
	}
	debug.Info("ctf", "engagement scope loaded", "prefixes", len(e.Scope))
	return e
}

// inScope reports whether addr falls inside the engagement scope. An empty scope
// is treated as unrestricted (everything in scope).
func (e Engagement) inScope(addr netip.Addr) bool {
	if len(e.Scope) == 0 {
		return true
	}
	for _, p := range e.Scope {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// authorizedActionRe matches the offensive / post-exploitation action classes
// that CTF mode treats as authorized engagement work: recon, exploitation,
// credential looting and spraying, footholds, and in-scope exfil. These are the
// classes the default judge would slow down action-by-action; fast-pathing them
// keeps an authorized engagement moving. Destructive/irreversible patterns are
// deliberately absent, so they fall through to guard and the judge.
var authorizedActionRe = regexp.MustCompile(`(?i)\b(` +
	`nmap|masscan|rustscan|gobuster|feroxbuster|ffuf|dirb|nikto|whatweb|wpscan|` + // recon
	`hydra|medusa|crackmapexec|nxc|netexec|sshpass|kerbrute|patator|` + // credential spray
	`john|hashcat|` + // cracking
	`msfconsole|msfvenom|searchsploit|sqlmap|` + // exploitation
	`kubectl|` + // k8s
	`enum4linux|smbclient|smbmap|rpcclient|ldapsearch|evil-winrm` + // service enum
	`)\b`)

// footholdRe matches authorized post-exploitation shell/persistence classes:
// reverse shells, listeners, and authorized_keys writes.
var footholdRe = regexp.MustCompile(`(?i)(` +
	`/dev/tcp/|` + // bash reverse shell
	`\bnc\b[^|]*-[a-z]*e|\bncat\b|\bsocat\b|` + // netcat/socat shells
	`mkfifo\b.*\|\s*(sh|bash)|` + // named-pipe reverse shell
	`authorized_keys` + // key-based persistence
	`)`)

// ipRe extracts IPv4 literals from a command so the fast-path can scope-check the
// hosts an action touches.
var ipRe = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)

// ctfRoE decides the CTF scope-aware fast-path for a bash command. It returns a
// result and ok=true only when the command is an authorized action class whose
// referenced hosts are all in scope; otherwise ok=false so the caller falls
// through to the normal guard-only/judge flow (out-of-scope or unrecognized
// actions still escalate). guard.Check has already run, so a catastrophic
// command can never reach here.
func (g *ReviewGate) ctfRoE(cmd string, e Engagement) (agent.ReviewResult, bool) {
	authorized := authorizedActionRe.MatchString(cmd) || footholdRe.MatchString(cmd)
	if !authorized {
		return agent.ReviewResult{}, false
	}
	// Scope-check every IPv4 the command mentions. Any out-of-scope host means
	// the action escalates to the judge rather than fast-pathing.
	for _, m := range ipRe.FindAllString(cmd, -1) {
		addr, err := netip.ParseAddr(m)
		if err != nil {
			continue
		}
		if !e.inScope(addr) {
			debug.Info("ctf", "roe escalate: out-of-scope host", "host", m, "cmd", truncate(cmd, 200))
			return agent.ReviewResult{}, false
		}
	}
	debug.Info("ctf", "roe fast-path allow", "cmd", truncate(cmd, 200))
	return agent.ReviewResult{Allowed: true, Source: "ctf-roe", Risk: "authorized"}, true
}

// SetCTFMode toggles the CTF scope-aware rules of engagement on the gate. When
// on, authorized in-scope offensive actions fast-path past the judge while
// out-of-scope and destructive actions still escalate.
func (g *ReviewGate) SetCTFMode(on bool) {
	g.mu.Lock()
	changed := g.cfg.CTF != on
	g.cfg.CTF = on
	if on && len(g.engagement.Scope) == 0 {
		g.engagement = LoadEngagement()
	}
	scope := len(g.engagement.Scope)
	g.mu.Unlock()
	if changed {
		debug.Info("ctf", "roe mode changed", "enabled", on, "scope_prefixes", scope)
	}
}

// CTFMode reports whether the CTF rules of engagement are active.
func (g *ReviewGate) CTFMode() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cfg.CTF
}
