package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

// captureToolDebug enables the debug logger scoped to the "tool" component and
// returns a reader for the captured output.
func captureToolDebug(t *testing.T) func() string {
	t.Helper()
	_ = debug.Close()
	path := filepath.Join(t.TempDir(), "pluto-debug.log")
	t.Setenv("PLUTO_DEBUG", "1")
	t.Setenv("PLUTO_DEBUG_FILE", path)
	t.Setenv("PLUTO_DEBUG_LEVEL", "debug")
	t.Setenv("PLUTO_DEBUG_COMPONENTS", "tool")
	t.Setenv("PLUTO_DEBUG_FRAMES", "")
	if _, err := debug.Init(); err != nil {
		t.Fatalf("debug.Init: %v", err)
	}
	t.Cleanup(func() { _ = debug.Close() })
	return func() string {
		_ = debug.Close()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return string(data)
	}
}

func TestServerToolUseNonStreamingLogged(t *testing.T) {
	read := captureToolDebug(t)
	p := fakeGenProvider{resp: llm.Response{
		Text: "answer",
		ServerToolUses: []llm.ServerToolUse{
			{Name: "web_search", Args: `{"query":"go"}`, Result: "Go (https://go.dev)"},
		},
	}}
	collect(t, New(p, tool.NewRegistry(), ""), "hi")

	out := read()
	if !strings.Contains(out, "server tool use") || !strings.Contains(out, "web_search") {
		t.Fatalf("server tool use not logged:\n%s", out)
	}
}

func TestServerToolUseStreamingLogged(t *testing.T) {
	read := captureToolDebug(t)
	p := fakeStreamProvider{
		deltas: []llm.StreamDelta{
			{Kind: llm.DeltaServerToolCall, Tool: "web_search", Text: `{"query":"go"}`},
			{Kind: llm.DeltaServerToolResult, Tool: "web_search", Text: "Go (https://go.dev)"},
			{Kind: llm.DeltaText, Text: "answer"},
		},
		resp: llm.Response{Text: "answer"},
	}
	collect(t, New(p, tool.NewRegistry(), ""), "hi")

	out := read()
	if !strings.Contains(out, "server tool call") || !strings.Contains(out, "server tool result") {
		t.Fatalf("streaming server tool activity not logged:\n%s", out)
	}
}
