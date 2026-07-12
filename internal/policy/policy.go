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
	"github.com/syrull/pluto/internal/guard"
	"github.com/syrull/pluto/internal/judge"
	"github.com/syrull/pluto/internal/llm"
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
	judge judge.Judge // may be nil (guard-only review)
}

var (
	_ agent.Gate           = (*ReviewGate)(nil)
	_ agent.AutoController = (*ReviewGate)(nil)
)

// NewReviewGate builds a gate from cfg and an optional judge (nil ⇒ guard-only).
func NewReviewGate(cfg Config, j judge.Judge) *ReviewGate {
	if cfg.OnJudgeError == "" {
		cfg.OnJudgeError = judge.DecisionBlock
	}
	if cfg.Mode == "" {
		cfg.Mode = DefaultMode
	}
	return &ReviewGate{cfg: cfg, judge: j}
}

// Review implements agent.Gate.
func (g *ReviewGate) Review(ctx context.Context, call llm.ToolCall) agent.ReviewResult {
	g.mu.RLock()
	cfg, j := g.cfg, g.judge
	g.mu.RUnlock()

	if cfg.Mode == ModeOff {
		return agent.ReviewResult{Allowed: true, Source: "off"}
	}
	if call.Name != "bash" {
		return agent.ReviewResult{Allowed: true, Source: "fast-path"}
	}

	cmd, intent, why := parseBash(call.Args)
	if strings.TrimSpace(cmd) == "" {
		return agent.ReviewResult{Allowed: true, Source: "fast-path"}
	}
	if cfg.FastPath && isSafeRead(cmd) {
		return agent.ReviewResult{Allowed: true, Source: "fast-path", Risk: "none"}
	}
	if v, ok := guard.Check(cmd); ok {
		return agent.ReviewResult{Allowed: false, Source: "guard", Risk: "critical", Reason: v.Reason}
	}
	if j == nil {
		return agent.ReviewResult{Allowed: true, Source: "guard-only"}
	}

	verdict, err := j.Assess(ctx, judge.Request{Command: cmd, Intent: intent, Why: why, Cwd: cwd()})
	if err != nil {
		allowed := cfg.OnJudgeError == judge.DecisionAllow
		reason := "judge unavailable — allowed by policy"
		if !allowed {
			reason = "judge unavailable — blocked by fail-safe policy"
		}
		return agent.ReviewResult{Allowed: allowed, Source: "judge-error", Reason: reason}
	}
	return agent.ReviewResult{
		Allowed: verdict.Decision != judge.DecisionBlock,
		Source:  "judge",
		Risk:    verdict.Risk,
		Reason:  verdict.Reason,
	}
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
