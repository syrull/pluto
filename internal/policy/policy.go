// Package policy composes the offline guard denylist and the LLM judge into a
// single review gate the agent consults before running a tool call.
package policy

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/guard"
	"github.com/syrull/pluto/internal/judge"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/mcp"
	"github.com/syrull/pluto/internal/workdir"
)

// Mode selects how the gate reviews commands.
type Mode string

const (
	ModeAuto Mode = "auto" // guard + judge decide automatically (DEFAULT)
	ModeOff  Mode = "off"  // no review; every call passes through
)

// DefaultMode is what pluto runs when PLUTO_AUTO is unset.
const DefaultMode = ModeAuto

// Config controls a ReviewGate's behavior.
type Config struct {
	Mode         Mode
	OnJudgeError judge.Decision // allow|block when the judge fails
	FastPath     bool           // skip the judge for trivially safe read/search commands
	JudgeName    string         // display name of the judge model, for status
}

// ReviewGate reviews bash commands through the guard denylist and the judge. Other
// tools pass through. It is safe for concurrent use.
type ReviewGate struct {
	mu    sync.RWMutex
	cfg   Config
	judge judge.Judge     // may be nil (guard-only review)
	allow map[string]bool // session allowlist of human-approved patterns
	cache *verdictCache   // memoized judge verdicts, keyed by (command, cwd)
}

var (
	_ agent.Gate           = (*ReviewGate)(nil)
	_ agent.AutoController = (*ReviewGate)(nil)
	_ agent.Allowlister    = (*ReviewGate)(nil)
)

// NewReviewGate builds a gate from cfg and an optional judge (nil ⇒ guard-only).
func NewReviewGate(cfg Config, j judge.Judge) *ReviewGate {
	if cfg.OnJudgeError == "" {
		cfg.OnJudgeError = judge.DecisionBlock
	}
	if cfg.Mode == "" {
		cfg.Mode = DefaultMode
	}
	return &ReviewGate{cfg: cfg, judge: j, cache: newVerdictCache(defaultVerdictCacheCap)}
}

// Review implements agent.Gate.
func (g *ReviewGate) Review(ctx context.Context, call llm.ToolCall) agent.ReviewResult {
	g.mu.RLock()
	cfg, j := g.cfg, g.judge
	g.mu.RUnlock()

	if cfg.Mode == ModeOff {
		return agent.ReviewResult{Allowed: true, Source: "off"}
	}
	// External MCP tools are third-party code with no shell command, so they take a
	// dedicated path that has the judge assess the tool call and its arguments
	// rather than the bash review below. Built-in non-bash tools (read/write/edit/…)
	// stay on the fast path.
	if call.Name != "bash" {
		if mcp.IsToolName(call.Name) {
			return g.reviewMCP(ctx, cfg, j, call)
		}
		return agent.ReviewResult{Allowed: true, Source: "fast-path"}
	}

	cmd, intent, why := parseBash(call.Args)
	if strings.TrimSpace(cmd) == "" {
		return agent.ReviewResult{Allowed: true, Source: "fast-path"}
	}
	if cfg.FastPath && isSafeRead(cmd) {
		debug.Debug("policy", "fast-path allow", "cmd", truncate(cmd, 200))
		return agent.ReviewResult{Allowed: true, Source: "fast-path", Risk: "none"}
	}
	if v, ok := guard.Check(cmd); ok {
		debug.Warn("policy", "guard block", "rule", v.Rule, "cmd", truncate(cmd, 200))
		return agent.ReviewResult{Allowed: false, Source: "guard", Risk: "critical", Reason: v.Reason}
	}
	if j == nil {
		debug.Debug("policy", "guard-only allow", "cmd", truncate(cmd, 200))
		return agent.ReviewResult{Allowed: true, Source: "guard-only"}
	}

	// A pattern a human approved earlier this session skips the judge entirely so a
	// transient judge outage doesn't re-prompt for the same class of command. Guard
	// already ran above, so a catastrophic command can never reach the allowlist.
	pattern := allowPattern(cmd)
	if pattern != "" && g.allowed(pattern) {
		debug.Info("policy", "allowlist match", "pattern", truncate(pattern, 200), "cmd", truncate(cmd, 200))
		return agent.ReviewResult{Allowed: true, Source: "allowlist"}
	}

	// The agent's actual working directory (its worktree) rides in on the context;
	// fall back to the process cwd only when none was threaded through. Passing the
	// real dir keeps a worktree-scoped command from reading as out-of-scope.
	dir := workdir.From(ctx)
	if dir == "" {
		dir = cwd()
	}

	// Memoize the judge by (normalized command, cwd) — deliberately not
	// intent/why, which are model-supplied and must not bust or poison the cache.
	// guard.Check already ran above, so a cache hit can never wave through a
	// catastrophic command; only the LLM step is skipped.
	key := verdictKey(cmd, dir)
	if verdict, ok := g.cacheGet(key); ok {
		debug.Debug("policy", "judge cache hit", "cmd", truncate(cmd, 200), "cwd", dir,
			"decision", string(verdict.Decision), "risk", verdict.Risk)
		return verdictResult(verdict)
	}
	debug.Debug("policy", "judge cache miss", "cmd", truncate(cmd, 200), "cwd", dir)

	debug.Debug("policy", "judge review", "cmd", truncate(cmd, 200), "cwd", dir)
	timer := debug.NewTimer("policy", "judge verdict")
	verdict, err := j.Assess(ctx, judge.Request{Command: cmd, Intent: intent, Why: why, Cwd: dir})
	if err != nil {
		// Defer to a human when one can answer; the agent falls back to the
		// non-interactive OnJudgeError decision (carried in Allowed) when no
		// approver is wired (headless or background agent). Errors are never
		// cached — the next turn should retry.
		allowed := cfg.OnJudgeError == judge.DecisionAllow
		reason := "judge unavailable — approve to run"
		timer.Stop("outcome", "error", "fallback_allowed", allowed, "err", err)
		debug.Warn("policy", "judge error → needs approval", "cmd", truncate(cmd, 200),
			"pattern", truncate(pattern, 200), "fallback_allowed", allowed)
		return agent.ReviewResult{
			Allowed:       allowed,
			NeedsApproval: true,
			Source:        "judge-error",
			Reason:        reason,
			Pattern:       pattern,
		}
	}
	timer.Stop("decision", string(verdict.Decision), "risk", verdict.Risk)
	g.cachePut(key, verdict)
	return verdictResult(verdict)
}

// verdictResult maps a judge verdict onto a review result. A cached verdict
// reproduces the original decision exactly, including a block's reason.
func verdictResult(v judge.Verdict) agent.ReviewResult {
	return agent.ReviewResult{
		Allowed: v.Decision != judge.DecisionBlock,
		Source:  "judge",
		Risk:    v.Risk,
		Reason:  v.Reason,
	}
}

// reviewMCP gates a call to an external MCP server tool. An MCP tool has no shell
// command, so instead of prompting a human it hands the tool call and its
// arguments to the judge — the same automatic model that reviews bash — and
// enforces the verdict. Verdicts are memoized like bash. A tool the human already
// allowlisted this session skips the judge. Only two cases still defer to a human:
// the judge is absent (guard-only mode) or it errored, both handled as
// needs-approval so an unattended run with no approver blocks as a fail-safe.
func (g *ReviewGate) reviewMCP(ctx context.Context, cfg Config, j judge.Judge, call llm.ToolCall) agent.ReviewResult {
	if g.allowed(call.Name) {
		debug.Info("policy", "mcp allowlist match", "tool", call.Name)
		return agent.ReviewResult{Allowed: true, Source: "allowlist"}
	}
	if j == nil {
		debug.Info("policy", "mcp needs approval (guard-only)", "tool", call.Name)
		return agent.ReviewResult{
			Allowed:       false,
			NeedsApproval: true,
			Source:        "mcp",
			Reason:        "external MCP tool — approve to run it this session",
			Pattern:       call.Name,
		}
	}

	dir := workdir.From(ctx)
	if dir == "" {
		dir = cwd()
	}
	desc := mcpCommand(call)
	key := verdictKey(desc, dir)
	if verdict, ok := g.cacheGet(key); ok {
		debug.Debug("policy", "mcp judge cache hit", "tool", call.Name, "cwd", dir,
			"decision", string(verdict.Decision), "risk", verdict.Risk)
		return verdictResult(verdict)
	}
	debug.Debug("policy", "mcp judge review", "tool", call.Name, "cwd", dir)
	timer := debug.NewTimer("policy", "mcp judge verdict")
	verdict, err := j.Assess(ctx, judge.Request{Command: desc, Intent: mcpIntent(call), Cwd: dir})
	if err != nil {
		// Defer to a human when one can answer; a background/headless agent falls
		// back to the OnJudgeError decision (carried in Allowed). Never cache errors.
		allowed := cfg.OnJudgeError == judge.DecisionAllow
		timer.Stop("outcome", "error", "fallback_allowed", allowed, "err", err)
		debug.Warn("policy", "mcp judge error → needs approval", "tool", call.Name, "fallback_allowed", allowed)
		return agent.ReviewResult{
			Allowed:       allowed,
			NeedsApproval: true,
			Source:        "judge-error",
			Reason:        "judge unavailable — approve to run",
			Pattern:       call.Name,
		}
	}
	timer.Stop("decision", string(verdict.Decision), "risk", verdict.Risk)
	g.cachePut(key, verdict)
	debug.Info("policy", "mcp judge verdict", "tool", call.Name,
		"decision", string(verdict.Decision), "risk", verdict.Risk, "reason", verdict.Reason)
	return verdictResult(verdict)
}

// mcpCommand renders an external MCP tool call as a single line for the judge: the
// namespaced tool name plus its JSON arguments, which is the whole of what there
// is to review since an MCP tool carries no shell command.
func mcpCommand(call llm.ToolCall) string {
	args := strings.TrimSpace(string(call.Args))
	if args == "" || args == "{}" {
		return call.Name
	}
	return call.Name + " " + args
}

// mcpIntent tells the judge the reviewed action is an external MCP tool call, so
// it assesses the tool and its arguments rather than reading the line as a shell
// command.
func mcpIntent(call llm.ToolCall) string {
	return "External MCP tool call: " + call.Name
}

// cacheGet reads a memoized verdict under the gate's lock.
func (g *ReviewGate) cacheGet(key string) (judge.Verdict, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cache.get(key)
}

// cachePut stores a verdict under the gate's lock.
func (g *ReviewGate) cachePut(key string, v judge.Verdict) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cache.put(key, v)
}

// Allow implements agent.Allowlister: it records a human-approved pattern on the
// session allowlist so matching commands skip approval for the rest of the run.
func (g *ReviewGate) Allow(pattern string) {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return
	}
	g.mu.Lock()
	if g.allow == nil {
		g.allow = make(map[string]bool)
	}
	already := g.allow[p]
	g.allow[p] = true
	n := len(g.allow)
	g.mu.Unlock()
	if !already {
		debug.Info("policy", "allowlist add", "pattern", truncate(p, 200), "entries", n)
	}
}

// allowed reports whether pattern is on the session allowlist.
func (g *ReviewGate) allowed(pattern string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.allow[pattern]
}

// subcommandTools are programs whose first argument is a subcommand, so an
// "allow this pattern" entry generalizes to "program subcommand" (e.g. "git
// status", "go test") rather than the exact command line. Everything else is
// remembered verbatim so a pattern can never over-match into a different command.
var subcommandTools = map[string]bool{
	"git": true, "go": true, "make": true, "cargo": true,
	"npm": true, "pnpm": true, "yarn": true, "bun": true, "node": true,
	"deno": true, "pip": true, "pip3": true, "python": true, "python3": true,
	"docker": true, "kubectl": true, "terraform": true, "gradle": true, "mvn": true,
}

// allowPattern derives the session-allowlist entry for cmd. The rule is
// deliberately conservative so "allow this pattern" can't wave through a
// different command: a command with shell metacharacters, or one whose first
// argument is a flag/operand, is remembered verbatim (whitespace-collapsed);
// only a recognized subcommand tool generalizes to "program subcommand". It
// returns "" for a blank command.
func allowPattern(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	norm := strings.Join(fields, " ")
	if strings.ContainsAny(norm, "|&;<>()$`\"'*?[]{}\\!#~") {
		return norm
	}
	if subcommandTools[fields[0]] && len(fields) >= 2 && !strings.HasPrefix(fields[1], "-") {
		return fields[0] + " " + fields[1]
	}
	return norm
}

// truncate bounds a command string for a log field.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// AutoEnabled implements agent.AutoController.
func (g *ReviewGate) AutoEnabled() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cfg.Mode == ModeAuto
}

// SetAutoEnabled implements agent.AutoController.
func (g *ReviewGate) SetAutoEnabled(on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if on {
		g.cfg.Mode = ModeAuto
	} else {
		g.cfg.Mode = ModeOff
	}
	debug.Info("policy", "auto mode changed", "enabled", on)
}

// JudgeName implements agent.AutoController.
func (g *ReviewGate) JudgeName() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.judge == nil {
		return "guard-only"
	}
	if g.cfg.JudgeName != "" {
		return g.cfg.JudgeName
	}
	return "judge"
}

func parseBash(args json.RawMessage) (cmd, intent, why string) {
	var a struct {
		Command string `json:"command"`
		Intent  string `json:"intent"`
		Why     string `json:"why"`
	}
	_ = json.Unmarshal(args, &a)
	return a.Command, a.Intent, a.Why
}

func cwd() string {
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return ""
}

// safeReadCmds are commands that only read or inspect state; when invoked
// without shell metacharacters they skip the judge.
var safeReadCmds = map[string]bool{
	"ls": true, "pwd": true, "cat": true, "head": true, "tail": true, "wc": true,
	"grep": true, "rg": true, "find": true, "echo": true, "which": true,
	"file": true, "stat": true, "env": true, "date": true, "whoami": true, "tree": true,
}

// safeGitSubcmds are read-only git subcommands eligible for the fast-path.
var safeGitSubcmds = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "branch": true, "remote": true,
}

// isSafeRead reports whether cmd is a trivially safe read/search/status command.
// Any shell metacharacter disqualifies it, so it can never smuggle side effects.
func isSafeRead(cmd string) bool {
	c := strings.TrimSpace(cmd)
	if c == "" || strings.ContainsAny(c, "|&;<>()$`\"'*?[]{}\\!#~\n\t") {
		return false
	}
	fields := strings.Fields(c)
	if len(fields) == 0 {
		return false
	}
	if fields[0] == "git" {
		return len(fields) >= 2 && safeGitSubcmds[fields[1]]
	}
	return safeReadCmds[fields[0]]
}

// LoadConfig reads auto-mode configuration from the environment.
func LoadConfig() Config {
	cfg := Config{Mode: DefaultMode, OnJudgeError: judge.DecisionBlock, FastPath: true}
	switch env("PLUTO_AUTO") {
	case "off", "0", "false", "no":
		cfg.Mode = ModeOff
	}
	if env("PLUTO_AUTO_ON_JUDGE_ERR") == "allow" {
		cfg.OnJudgeError = judge.DecisionAllow
	}
	switch env("PLUTO_AUTO_FASTPATH") {
	case "off", "0", "false", "no":
		cfg.FastPath = false
	}
	return cfg
}

func env(k string) string { return strings.ToLower(strings.TrimSpace(os.Getenv(k))) }
