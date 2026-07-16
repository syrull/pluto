// Command pluto is a minimal, modular AI-harness skeleton with a Bubbletea TUI.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/auth"
	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/judge"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/llm/anthropic"
	"github.com/syrull/pluto/internal/policy"
	"github.com/syrull/pluto/internal/reposcan"
	"github.com/syrull/pluto/internal/tool"
	"github.com/syrull/pluto/internal/tools"
	"github.com/syrull/pluto/internal/tui"
	"github.com/syrull/pluto/internal/update"
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
// hardcoded list that can drift as tools are added or removed. Any project
// context files (see contextFiles) present in the working directory are
// injected after the tool listing so their guidance rides along with the
// system message on every conversation reset. A one-shot repo snapshot
// (see internal/reposcan) is appended last so the model starts knowing the
// basic layout instead of rediscovering it turn-by-turn; it is built once at
// startup and stays inside the cached system prefix.
func buildSystemPrompt(reg *tool.Registry) string {
	var b strings.Builder
	b.WriteString(systemPromptBase)
	b.WriteString("\n\nAvailable tools:")
	for _, t := range reg.Tools() {
		fmt.Fprintf(&b, "\n- %s: %s", t.Name(), t.Description())
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

	provider := selectProvider()
	gate := buildGate()
	systemPrompt := buildSystemPrompt(reg)
	logConfig(provider, gate)

	// One summarizer, shared by the agents (context compaction) and the TUI
	// (agent auto-labeling); nil when it can't authenticate.
	summarizer := buildSummarizer()

	// newAgent builds a fresh agent for each workspace: same provider, tools, gate,
	// system prompt, and summarizer, but an independent transcript so agents run in
	// parallel.
	newAgent := func() *agent.Agent {
		return agent.New(provider, reg, systemPrompt,
			agent.WithGate(gate),
			agent.WithContextLimit(contextLimit()),
			agent.WithSummarizer(summarizer),
		)
	}
	ag := newAgent()
	debug.Info("lifecycle", "starting TUI")
	if _, err := tui.New(ag, newAgent, summarizer, buildLoginHook(ag)).Run(); err != nil {
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
	debug.Info("lifecycle", "effective config",
		"provider", provider.Name(),
		"model", defaultModel(),
		"judge_model", judgeModel(),
		"context_limit", contextLimit(),
		"auto", auto,
		"judge", judgeName,
		"repo_scan", os.Getenv("PLUTO_REPO_SCAN"),
		"mouse", os.Getenv("PLUTO_MOUSE"),
		"anthropic_api_key", redactedEnv("ANTHROPIC_API_KEY"),
		"anthropic_oauth_token", redactedEnv("ANTHROPIC_OAUTH_TOKEN"),
	)
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
// auto-labeling, or nil when it can't authenticate — callers fall back to plain
// eviction and first-message labels respectively.
func buildSummarizer() func(context.Context, string) (string, error) {
	p, err := anthropic.New(judgeModel())
	if err != nil {
		return nil
	}
	p.SetWebSearchMaxUses(0)
	p.SetThinkLevel(llm.ThinkNone)
	return func(ctx context.Context, prompt string) (string, error) {
		resp, err := p.Generate(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
		if err != nil {
			return "", err
		}
		return resp.Text, nil
	}
}

// buildLoginHook wires /login to the Anthropic OAuth flow: it builds the PKCE
// authorization URL, waits on a local callback server (with a manual paste
// fallback), exchanges/persists the token, and re-authenticates the live
// provider (upgrading from the offline stub if needed).
func buildLoginHook(ag *agent.Agent) *tui.LoginHook {
	reauth := func() (string, error) {
		if sw, ok := ag.Switcher(); ok {
			if p, isAnthropic := sw.(*anthropic.Provider); isAnthropic {
				if err := p.Reauth(); err != nil {
					return "", err
				}
				return "logged in — " + ag.ProviderName(), nil
			}
		}
		p, err := anthropic.New(defaultModel())
		if err != nil {
			return "", err
		}
		ag.SetProvider(p)
		return "logged in — upgraded to " + ag.ProviderName(), nil
	}
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

// buildGate constructs the auto-mode review gate. It returns nil (allow-all)
// when PLUTO_AUTO=off. When the judge provider can't authenticate, auto mode
// stays on in guard-only form so catastrophic commands are still blocked.
func buildGate() agent.Gate {
	cfg := policy.LoadConfig()
	if cfg.Mode == policy.ModeOff {
		return nil
	}
	model := judgeModel()
	jp, err := anthropic.New(model)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pluto: judge unavailable, auto mode running guard-only:", err)
		return policy.NewReviewGate(cfg, nil)
	}
	jp.SetWebSearchMaxUses(0) // the judge never needs web search
	cfg.JudgeName = model
	return policy.NewReviewGate(cfg, judge.NewLLM(jp))
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
