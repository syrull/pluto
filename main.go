// Command harness is a minimal, modular AI-harness skeleton with a Bubbletea TUI.
package main

import (
	"fmt"
	"os"

	"github.com/pluto/harness/internal/agent"
	"github.com/pluto/harness/internal/auth"
	"github.com/pluto/harness/internal/debug"
	"github.com/pluto/harness/internal/llm"
	"github.com/pluto/harness/internal/llm/anthropic"
	"github.com/pluto/harness/internal/tool"
	"github.com/pluto/harness/internal/tools"
	"github.com/pluto/harness/internal/tui"
)

const systemPrompt = "You are a minimal file-editing agent with read, write, edit, find, and bash tools. " +
	"Always use the find tool (not grep/rg/ag inside bash) to search file contents — it returns " +
	"bounded, pre-truncated results that won't blow up the context window. Reserve bash for actions " +
	"find and the other tools can't do (running builds/tests, git, listing directories, etc.). " +
	"Before making any decision or editing code, always explore the relevant code first and clearly " +
	"understand what it does — read the surrounding context, trace callers and definitions, and " +
	"confirm your understanding rather than acting on assumptions."

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

	ag := agent.New(provider, reg, systemPrompt)
	if _, err := tui.New(ag, buildLoginHook(ag)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "harness:", err)
		os.Exit(1)
	}
}

// buildLoginHook wires /login: it runs `claude setup-token`, then captures the
// minted token and either reauths the running Anthropic provider or upgrades
// the agent from the stub to a fresh authenticated provider.
func buildLoginHook(ag *agent.Agent) *tui.LoginHook {
	return &tui.LoginHook{
		Command: auth.LoginCommand,
		After: func(procErr error) (string, error) {
			if procErr != nil {
				return "", fmt.Errorf("login process: %w", procErr)
			}
			if _, err := auth.CaptureAfterLogin(); err != nil {
				return "", err
			}
			// Upgrade or reauth the live provider.
			if sw, ok := ag.Switcher(); ok {
				if p, isAnthropic := sw.(*anthropic.Provider); isAnthropic {
					if err := p.Reauth(); err != nil {
						return "", err
					}
					return "logged in — " + ag.ProviderName(), nil
				}
			}
			// Current provider is the stub (or non-anthropic): build a fresh one.
			p, err := anthropic.New(defaultModel())
			if err != nil {
				return "", err
			}
			ag.SetProvider(p)
			return "logged in — upgraded to " + ag.ProviderName(), nil
		},
	}
}

func defaultModel() string {
	if m := os.Getenv("HARNESS_MODEL"); m != "" {
		return m
	}
	return anthropic.DefaultModel
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
