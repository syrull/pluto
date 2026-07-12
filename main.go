// Command harness is a minimal, modular AI-harness skeleton with a Bubbletea TUI.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pluto/harness/internal/agent"
	"github.com/pluto/harness/internal/auth"
	"github.com/pluto/harness/internal/debug"
	"github.com/pluto/harness/internal/judge"
	"github.com/pluto/harness/internal/llm"
	"github.com/pluto/harness/internal/llm/anthropic"
	"github.com/pluto/harness/internal/policy"
	"github.com/pluto/harness/internal/tool"
	"github.com/pluto/harness/internal/tools"
	"github.com/pluto/harness/internal/tui"
)

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
// system message on every conversation reset.
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
	return b.String()
}

func main() {
	if path, err := debug.Init(); err != nil {
		fmt.Fprintln(os.Stderr, "harness: debug logging disabled:", err)
	} else if path != "" {
		fmt.Fprintln(os.Stderr, "harness: debug logging to", path)
	}
	defer debug.Close()

	reg := tool.NewRegistry()
	reg.MustRegister(tools.Read{})
	reg.MustRegister(tools.Write{})
	reg.MustRegister(tools.Bash{})
	reg.MustRegister(tools.Edit{})
	reg.MustRegister(tools.Find{})

	provider := selectProvider()

	ag := agent.New(provider, reg, buildSystemPrompt(reg), agent.WithGate(buildGate()))
	if _, err := tui.New(ag, buildLoginHook(ag)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "harness:", err)
		os.Exit(1)
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
	if m := os.Getenv("HARNESS_MODEL"); m != "" {
		return m
	}
	return anthropic.DefaultModel
}

func judgeModel() string {
	if m := os.Getenv("HARNESS_JUDGE_MODEL"); m != "" {
		return m
	}
	return anthropic.DefaultJudgeModel
}

// buildGate constructs the auto-mode review gate. It returns nil (allow-all)
// when HARNESS_AUTO=off. When the judge provider can't authenticate, auto mode
// stays on in guard-only form so catastrophic commands are still blocked.
func buildGate() agent.Gate {
	cfg := policy.LoadConfig()
	if cfg.Mode == policy.ModeOff {
		return nil
	}
	model := judgeModel()
	jp, err := anthropic.New(model)
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: judge unavailable, auto mode running guard-only:", err)
		return policy.NewGate(cfg, nil)
	}
	jp.SetWebSearchMaxUses(0) // the judge never needs web search
	cfg.JudgeName = model
	return policy.NewGate(cfg, judge.NewLLM(jp))
}

// selectProvider returns the Anthropic provider if it can authenticate,
// otherwise the offline stub. HARNESS_MODEL overrides the default model.
func selectProvider() llm.Provider {
	if p, err := anthropic.New(defaultModel()); err == nil {
		return p
	} else {
		fmt.Fprintln(os.Stderr, "harness: falling back to stub provider:", err)
	}
	return llm.Stub{}
}
