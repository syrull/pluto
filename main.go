// Command pluto is a minimal, modular AI-harness skeleton with a Bubbletea TUI.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/auth"
	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/goal"
	"github.com/syrull/pluto/internal/judge"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/llm/anthropic"
	"github.com/syrull/pluto/internal/mcp"
	"github.com/syrull/pluto/internal/policy"
	"github.com/syrull/pluto/internal/reposcan"
	"github.com/syrull/pluto/internal/session"
	"github.com/syrull/pluto/internal/skills"
	"github.com/syrull/pluto/internal/tool"
	"github.com/syrull/pluto/internal/tools"
	"github.com/syrull/pluto/internal/tui"
	"github.com/syrull/pluto/internal/update"
	"github.com/syrull/pluto/internal/worker"
)

// version is the build version, injected via -ldflags at release time and
// left as "dev" for local builds.
var version = "dev"

// systemPromptBase is the static, always-true framing prepended to the
// dynamically generated tool listing built from the registry (see
// buildSystemPrompt). Per-tool rules (read/find over cat/grep, bash intent/why,
// bounded output) live in each tool's Description so they aren't paid for twice.
const systemPromptBase = "You are a minimal file-editing agent. " +
	"Before making any decision or editing code, always explore the relevant code first and clearly " +
	"understand what it does — read the surrounding context, trace callers and definitions, and " +
	"confirm your understanding rather than acting on assumptions."

// contextFiles are project instruction files auto-injected into the system
// prompt when present in the working directory, in this order.
var contextFiles = []string{"CLAUDE.md", "AGENTS.md"}

// buildSystemPrompt appends a listing of the registered tools to the static
// base so the prompt always reflects the actual registry rather than a
// hardcoded list that can drift as tools are added or removed. A compact skills
// index (name + description, see internal/skills) follows so the model knows
// which on-demand skills exist without paying for their full bodies — those are
// loaded lazily via the skill tool. Any project context files (see contextFiles)
// present in the working directory are injected next so their guidance rides
// along with the system message on every conversation reset. A one-shot repo
// snapshot (see internal/reposcan) is appended last so the model starts knowing
// the basic layout instead of rediscovering it turn-by-turn; it is built once at
// startup and stays inside the cached system prefix.
func buildSystemPrompt(reg *tool.Registry) string {
	var b strings.Builder
	b.WriteString(systemPromptBase)
	b.WriteString("\n\nAvailable tools:")
	for _, t := range reg.Tools() {
		fmt.Fprintf(&b, "\n- %s: %s", t.Name(), t.Description())
	}
	skillsTimer := debug.NewTimer("lifecycle", "skills scanned")
	list := skills.List(skills.DirName)
	skillsTimer.Stop("dir", skills.DirName, "count", len(list))
	if len(list) > 0 {
		fmt.Fprintf(&b, "\n\n--- Skills (load a skill's full instructions on demand with the skill tool) ---\n%s", skills.Render(list))
		debug.Info("lifecycle", "skills indexed", "count", len(list))
	}
	for _, name := range contextFiles {
		data, err := os.ReadFile(name)
		if err != nil {
			// Missing/unreadable context files are expected; skip silently.
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		fmt.Fprintf(&b, "\n\n--- Project context from %s ---\n%s", name, content)
	}
	if overview := reposcan.Overview(); overview != "" {
		fmt.Fprintf(&b, "\n\n%s", overview)
	}
	return b.String()
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "update":
			if err := update.Run(version); err != nil {
				fmt.Fprintln(os.Stderr, "pluto:", err)
				os.Exit(1)
			}
			return
		case "version", "-v", "--version":
			fmt.Println("pluto", version)
			return
		}
	}

	if path, err := debug.Init(); err != nil {
		fmt.Fprintln(os.Stderr, "pluto: debug logging disabled:", err)
	} else if path != "" {
		fmt.Fprintln(os.Stderr, "pluto: debug logging to", path)
	}
	defer debug.Close()
	defer debug.LogPanic()
	logInvocation()

	reg := tool.NewRegistry()
	reg.MustRegister(tools.Read{})
	reg.MustRegister(tools.Write{})
	reg.MustRegister(tools.Bash{})
	reg.MustRegister(tools.Edit{})
	reg.MustRegister(tools.Find{})
	reg.MustRegister(tools.Skill{})

	// Load MCP servers declared in mcp.json (see internal/mcp): each connects,
	// its tools are registered alongside the built-ins, and the connections stay
	// open for the session. Best-effort — a missing config or an unreachable
	// server is logged and skipped so pluto still starts.
	mcpMgr := mcp.New(version).WithProgress(os.Stderr)
	mcpSummary := mcpMgr.LoadWithDeadline(reg)
	defer mcpMgr.Close()
	logMCP(mcpSummary)

	provider := selectProvider()
	gate, judgeProvider := buildGate()

	// One summarizer, shared by the agents (context compaction), the TUI (agent
	// auto-labeling), and the worker pool (worker context compaction); nil when it
	// can't authenticate.
	summarizer, summarizerProvider := buildSummarizer()

	// Parallel worker sub-agents (see internal/worker): the pool scopes each worker
	// from a snapshot of the current tools (built-ins + MCP) so a worker can be
	// granted any of them except the dispatch tool itself, and shares the review
	// gate so scope/judge context propagates to children. Registering the workers
	// tool last keeps it out of that snapshot. The blackboard is the shared,
	// append-only coordination substrate.
	board := session.NewBlackboard()
	pool := worker.NewPool(context.Background(), worker.Config{
		Provider:  provider,
		Registry:  cloneRegistry(reg),
		Gate:      gate,
		Summarize: summarizer,
		SkillsDir: skills.DirName,
		Board:     board,
		Limits:    workerLimits(),
	})
	reg.MustRegister(worker.NewDispatchTool(pool))

	systemPrompt := buildSystemPrompt(reg)
	logConfig(provider, gate)

	// The /goal completion evaluator: a small, fast, transcript-only model that
	// judges the user's condition after each turn. nil when disabled (PLUTO_GOAL=off)
	// or it can't authenticate — /goal then degrades with a clear message.
	evaluator, evaluatorProvider := buildEvaluator()

	// approver is the shared human-in-the-loop hook: when the judge errors, the
	// gate defers to it instead of silently applying OnJudgeError. It bridges the
	// blocking agent goroutine to the TUI prompt (see tui.Approver).
	approver := tui.NewApprover()

	// newAgent builds a fresh agent for each workspace: same provider, tools, gate,
	// approver, system prompt, and summarizer, but an independent transcript so
	// agents run in parallel.
	newAgent := func() *agent.Agent {
		return agent.New(provider, reg, systemPrompt,
			agent.WithGate(gate),
			agent.WithApprover(approver),
			agent.WithContextLimit(contextLimit()),
			agent.WithSummarizer(summarizer),
		)
	}
	ag := newAgent()
	debug.Info("lifecycle", "starting TUI")
	// /login must re-authenticate every Anthropic provider, not just the main
	// model: the judge and summarizer cache their own token, so without this a
	// re-login leaves the judge on an expired token and the fail-safe policy
	// blocks every command for the rest of the session.
	loginHook := buildLoginHook(ag, pool, auxReauthers(judgeProvider, summarizerProvider, evaluatorProvider)...)
	if _, err := tui.New(ag, newAgent, summarizer, loginHook, approver, evaluator, mcpSummary, pool).Run(); err != nil {
		debug.Error("lifecycle", "TUI exited with error", "err", err)
		fmt.Fprintln(os.Stderr, "pluto:", err)
		os.Exit(1)
	}
	debug.Info("lifecycle", "clean shutdown")
}

// logInvocation records how pluto was started: where, with what, and in what
// environment, so a session log leads with the full context needed to reproduce it.
func logInvocation() {
	if !debug.Enabled() {
		return
	}
	cwd, _ := os.Getwd()
	debug.Info("lifecycle", "invoked pluto from directory "+cwd,
		"cwd", cwd,
		"argv", strings.Join(os.Args, " "),
		"version", version,
		"go", runtime.Version(),
		"os", runtime.GOOS,
		"arch", runtime.GOARCH,
		"term", os.Getenv("TERM"),
		"colorterm", os.Getenv("COLORTERM"),
	)
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		debug.Info("lifecycle", "terminal size", "cols", w, "rows", h)
	}
}

// logConfig records the effective, env-derived configuration with secrets
// redacted, so the log states exactly which model/judge/auto settings were in play.
func logConfig(provider llm.Provider, gate agent.Gate) {
	if !debug.Enabled() {
		return
	}
	auto := "off"
	judgeName := ""
	if gate != nil {
		auto = "on"
		if c, ok := gate.(agent.AutoController); ok {
			judgeName = c.JudgeName()
		}
	}
	goalState := "on"
	if !goalEnabled() {
		goalState = "off"
	}
	debug.Info("lifecycle", "effective config",
		"provider", provider.Name(),
		"model", defaultModel(),
		"judge_model", judgeModel(),
		"goal", goalState,
		"goal_model", goalModel(),
		"goal_max_turns", os.Getenv("PLUTO_GOAL_MAX_TURNS"),
		"context_limit", contextLimit(),
		"auto", auto,
		"judge", judgeName,
		"repo_scan", os.Getenv("PLUTO_REPO_SCAN"),
		"mouse", os.Getenv("PLUTO_MOUSE"),
		"anthropic_api_key", redactedEnv("ANTHROPIC_API_KEY"),
		"anthropic_oauth_token", redactedEnv("ANTHROPIC_OAUTH_TOKEN"),
	)
}

// logMCP records the outcome of loading MCP servers and surfaces a one-line
// notice on stderr so the user sees which servers loaded (or failed) at startup.
func logMCP(s mcp.Summary) {
	if s.ConfigPath == "" && s.Servers == 0 && len(s.Failed) == 0 {
		return // no config present; stay quiet
	}
	debug.Info("mcp", "startup summary",
		"config", s.ConfigPath,
		"servers", s.Servers,
		"tools", s.Tools,
		"failed", len(s.Failed),
	)
	if s.Servers > 0 {
		fmt.Fprintf(os.Stderr, "pluto: loaded %d MCP server(s), %d tool(s)\n", s.Servers, s.Tools)
	}
	if len(s.Failed) > 0 {
		fmt.Fprintf(os.Stderr, "pluto: %d MCP server(s) failed to load: %s (see debug log)\n",
			len(s.Failed), strings.Join(s.Failed, ", "))
	}
}

// redactedEnv reports the presence of a secret env var without revealing it.
func redactedEnv(key string) string {
	if v := os.Getenv(key); v != "" {
		return debug.Redact(v)
	}
	return "<unset>"
}

// buildSummarizer returns a cheap one-shot summarizer backed by the judge model,
// shared by context compaction (summarizing evicted exchanges) and TUI agent
// auto-labeling, along with its Anthropic provider so /login can re-authenticate
// it. Both are nil when it can't authenticate — callers fall back to plain
// eviction and first-message labels respectively.
func buildSummarizer() (func(context.Context, string) (string, error), *anthropic.Provider) {
	p, err := anthropic.New(judgeModel())
	if err != nil {
		return nil, nil
	}
	p.SetWebSearchMaxUses(0)
	p.SetThinkLevel(llm.ThinkNone)
	fn := func(ctx context.Context, prompt string) (string, error) {
		resp, err := p.Generate(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
		if err != nil {
			return "", err
		}
		return resp.Text, nil
	}
	return fn, p
}

// reauther is any Anthropic provider whose cached credentials can be refreshed
// from the auth store after a /login. *anthropic.Provider satisfies it.
type reauther interface {
	Reauth() error
	Name() string
}

// auxReauthers collects the non-nil auxiliary Anthropic providers (judge,
// summarizer) so /login can refresh their credentials too.
func auxReauthers(ps ...*anthropic.Provider) []reauther {
	var out []reauther
	for _, p := range ps {
		if p != nil {
			out = append(out, p)
		}
	}
	return out
}

// reauthProviders refreshes credentials for the live agent provider and every
// auxiliary Anthropic provider (judge, summarizer) after a successful /login.
// Each provider caches its token at construction, so without this a re-login
// would restore only the main model while the judge kept its expired token —
// leaving the fail-safe policy to block every command. An auxiliary failure is
// logged and surfaced in the status line but does not fail the login, since the
// main model is the user-facing outcome.
func reauthProviders(ag *agent.Agent, pool *worker.Pool, aux ...reauther) (string, error) {
	status, upgraded, err := reauthMain(ag)
	if err != nil {
		return "", err
	}
	// A stub→Anthropic upgrade produces a brand-new provider; propagate it to the
	// worker pool so workers dispatched after /login run on the real backend too.
	// An in-place reauth mutates the shared provider, so the pool already sees it.
	if upgraded != nil && pool != nil {
		pool.SetProvider(upgraded)
	}
	var failed int
	for _, p := range aux {
		if p == nil {
			continue
		}
		timer := debug.NewTimer("auth", "aux provider reauth")
		if rerr := p.Reauth(); rerr != nil {
			failed++
			timer.Stop("provider", p.Name(), "outcome", "error", "err", rerr)
			debug.Warn("auth", "aux provider reauth failed", "provider", p.Name(), "err", rerr)
			continue
		}
		timer.Stop("provider", p.Name(), "outcome", "ok")
		debug.Info("auth", "aux provider reauthenticated", "provider", p.Name())
	}
	if failed > 0 {
		status += fmt.Sprintf(" (%d auxiliary provider(s) still unauthenticated — see debug log)", failed)
	}
	return status, nil
}

// reauthMain re-authenticates the live agent provider, upgrading from the
// offline stub when the session started without credentials. It returns a
// non-nil upgraded provider only when a brand-new one replaced the stub, so the
// caller can propagate it to the worker pool; an in-place reauth returns nil.
func reauthMain(ag *agent.Agent) (string, llm.Provider, error) {
	if sw, ok := ag.Switcher(); ok {
		if p, isAnthropic := sw.(*anthropic.Provider); isAnthropic {
			timer := debug.NewTimer("auth", "provider reauth")
			if err := p.Reauth(); err != nil {
				timer.Stop("provider", p.Name(), "outcome", "error", "err", err)
				debug.Warn("auth", "provider reauth failed", "provider", p.Name(), "err", err)
				return "", nil, err
			}
			timer.Stop("provider", ag.ProviderName(), "outcome", "ok")
			debug.Info("auth", "provider reauthenticated", "provider", ag.ProviderName())
			return "logged in — " + ag.ProviderName(), nil, nil
		}
	}
	p, err := anthropic.New(defaultModel())
	if err != nil {
		debug.Warn("auth", "provider upgrade failed", "err", err)
		return "", nil, err
	}
	ag.SetProvider(p)
	debug.Info("auth", "provider upgraded from stub", "provider", ag.ProviderName())
	return "logged in — upgraded to " + ag.ProviderName(), p, nil
}

// buildLoginHook wires /login to the Anthropic OAuth flow: it builds the PKCE
// authorization URL, waits on a local callback server (with a manual paste
// fallback), exchanges/persists the token, and re-authenticates the live
// provider (upgrading from the offline stub if needed) plus every auxiliary
// Anthropic provider (judge, summarizer).
func buildLoginHook(ag *agent.Agent, pool *worker.Pool, aux ...reauther) *tui.LoginHook {
	reauth := func() (string, error) { return reauthProviders(ag, pool, aux...) }
	return &tui.LoginHook{
		Authorize: func() (string, any, error) {
			url, flow, err := auth.AuthorizeURL()
			return url, flow, err
		},
		Wait: func(flow any) (string, error) {
			f, ok := flow.(*auth.Flow)
			if !ok {
				return "", fmt.Errorf("login: invalid flow handle")
			}
			if _, err := f.WaitForCallback(context.Background()); err != nil {
				return "", err
			}
			return reauth()
		},
		Complete: func(flow any, pasted string) (string, error) {
			f, ok := flow.(*auth.Flow)
			if !ok {
				return "", fmt.Errorf("login: invalid flow handle")
			}
			if _, err := f.Complete(context.Background(), pasted); err != nil {
				return "", err
			}
			return reauth()
		},
	}
}

func defaultModel() string {
	if m := os.Getenv("PLUTO_MODEL"); m != "" {
		return m
	}
	return anthropic.DefaultModel
}

func judgeModel() string {
	if m := os.Getenv("PLUTO_JUDGE_MODEL"); m != "" {
		return m
	}
	return anthropic.DefaultJudgeModel
}

// goalModel picks the /goal completion evaluator's model: PLUTO_GOAL_MODEL, else
// the judge model (which itself defaults to the small, fast DefaultJudgeModel).
func goalModel() string {
	if m := os.Getenv("PLUTO_GOAL_MODEL"); m != "" {
		return m
	}
	return judgeModel()
}

// goalEnabled reports whether /goal is active. PLUTO_GOAL=off disables it
// (mirroring PLUTO_AUTO), otherwise it is on.
func goalEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PLUTO_GOAL"))) {
	case "off", "0", "false", "no":
		return false
	}
	return true
}

// buildEvaluator builds the cheap, transcript-only /goal completion evaluator and
// returns its Anthropic provider so /login can re-authenticate it alongside the
// main model. Both are nil when PLUTO_GOAL=off or the evaluator model can't
// authenticate — /goal then reports that it is unavailable rather than silently
// doing nothing. Web search and extended thinking are disabled to keep it fast.
func buildEvaluator() (goal.Evaluator, *anthropic.Provider) {
	if !goalEnabled() {
		return nil, nil
	}
	model := goalModel()
	p, err := anthropic.New(model)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pluto: /goal unavailable, evaluator could not authenticate:", err)
		return nil, nil
	}
	p.SetWebSearchMaxUses(0)
	p.SetThinkLevel(llm.ThinkNone)
	return goal.NewLLM(p), p
}

// contextLimit reads PLUTO_CONTEXT_LIMIT (approx tokens of transcript to re-send
// per turn). 0 (unset/invalid) lets the agent derive a budget from the model window.
func contextLimit() int {
	if v := strings.TrimSpace(os.Getenv("PLUTO_CONTEXT_LIMIT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// cloneRegistry returns a new registry holding the same tools as r, so the
// worker pool can scope workers from the tools available now (built-ins + MCP)
// without ever exposing the dispatch tool that is registered into r afterward.
func cloneRegistry(r *tool.Registry) *tool.Registry {
	clone := tool.NewRegistry()
	for _, t := range r.Tools() {
		clone.MustRegister(t)
	}
	return clone
}

// workerLimits reads the fan-out safety budget from the environment so parallel
// workers stay paced rather than portscan-shaped: PLUTO_WORKERS_MAX caps total
// concurrency (default 8), PLUTO_WORKERS_PER_TARGET caps concurrency against one
// scope (default 4), and PLUTO_WORKERS_RATE_MS spaces starts against one scope
// (default 0 — no forced spacing).
func workerLimits() worker.Limits {
	return worker.Limits{
		MaxWorkers: envInt("PLUTO_WORKERS_MAX", 8),
		PerTarget:  envInt("PLUTO_WORKERS_PER_TARGET", 4),
		Interval:   time.Duration(envInt("PLUTO_WORKERS_RATE_MS", 0)) * time.Millisecond,
	}
}

// envInt reads a non-negative integer env var, falling back to def when unset or
// invalid.
func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

// buildGate constructs the auto-mode review gate and returns the judge's
// Anthropic provider so /login can re-authenticate it alongside the main model.
// The gate is nil (allow-all) when PLUTO_AUTO=off; the judge provider is nil
// when auto mode is off or the judge can't authenticate — in which case auto
// mode stays on in guard-only form so catastrophic commands are still blocked.
func buildGate() (agent.Gate, *anthropic.Provider) {
	cfg := policy.LoadConfig()
	if cfg.Mode == policy.ModeOff {
		return nil, nil
	}
	model := judgeModel()
	jp, err := anthropic.New(model)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pluto: judge unavailable, auto mode running guard-only:", err)
		return policy.NewReviewGate(cfg, nil), nil
	}
	jp.SetWebSearchMaxUses(0) // the judge never needs web search
	cfg.JudgeName = model
	return policy.NewReviewGate(cfg, judge.NewLLM(jp)), jp
}

// selectProvider returns the Anthropic provider if it can authenticate,
// otherwise the offline stub. PLUTO_MODEL overrides the default model.
func selectProvider() llm.Provider {
	if p, err := anthropic.New(defaultModel()); err == nil {
		return p
	} else {
		fmt.Fprintln(os.Stderr, "pluto: falling back to stub provider:", err)
	}
	return llm.Stub{}
}
