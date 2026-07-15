// Command pluto is a minimal, modular AI-harness skeleton with a Bubbletea TUI.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

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

// systemPromptBase is the static guidance prepended to the dynamically
// generated tool listing built from the registry (see buildSystemPrompt).
const systemPromptBase = "You are a minimal file-editing agent. " +
	"Always use the read tool (not cat/head/tail/sed/less inside bash) to view file contents, and " +
	"the find tool (not grep/rg/ag/find inside bash) to search them — both return bounded, " +
	"pre-truncated output that won't blow up the context window, and read/find accept offset/limit " +
	"and path/glob to page through or scope large results. Reserve bash for actions those tools " +
	"can't do (running builds/tests, git, listing directories, etc.). " +
	"Before making any decision or editing code, always explore the relevant code first and clearly " +
	"understand what it does — read the surrounding context, trace callers and definitions, and " +
	"confirm your understanding rather than acting on assumptions. " +
	"When you run bash, always fill its intent/why arguments: commands are reviewed by auto mode and " +
	"may be refused if destructive or malicious. If a command is refused, adapt with a safer approach."

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

	reg := tool.NewRegistry()
	reg.MustRegister(tools.Read{})
	reg.MustRegister(tools.Write{})
	reg.MustRegister(tools.Bash{})
	reg.MustRegister(tools.Edit{})
	reg.MustRegister(tools.Find{})

	provider := selectProvider()
	gate := buildGate()
	systemPrompt := buildSystemPrompt(reg)

	// newAgent builds a fresh agent for each workspace: same provider, tools, gate,
	// and system prompt, but an independent transcript so agents run in parallel.
	newAgent := func() *agent.Agent {
		return agent.New(provider, reg, systemPrompt,
			agent.WithGate(gate),
			agent.WithContextLimit(contextLimit()),
		)
	}
	ag := newAgent()
	if _, err := tui.New(ag, newAgent, buildSummarizer(), buildLoginHook(ag)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "pluto:", err)
		os.Exit(1)
	}
}

// buildSummarizer returns a cheap one-shot summarizer (used to auto-label agents)
// backed by the judge model, or nil when it can't authenticate so the TUI falls
// back to deriving labels from the first message.
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
