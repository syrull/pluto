package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

// raceNoopTool is a trivial tool so a scripted run produces tool_use/tool_result
// appends turn after turn.
type raceNoopTool struct{}

func (raceNoopTool) Name() string            { return "noop" }
func (raceNoopTool) Description() string     { return "noop" }
func (raceNoopTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (raceNoopTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "ok", nil
}

// raceLoopProvider requests a tool call for maxCalls turns (each turn a short
// pause widens the window), then finishes, reporting usage so lastUsage is
// written. It is a ContextWindower so ContextUsage reads lastUsage.
type raceLoopProvider struct {
	calls    int
	maxCalls int
}

func (p *raceLoopProvider) Name() string       { return "loop" }
func (p *raceLoopProvider) ContextWindow() int { return 200_000 }

func (p *raceLoopProvider) Generate(context.Context, []llm.Message, []llm.ToolSpec) (llm.Response, error) {
	time.Sleep(50 * time.Microsecond)
	turn := p.calls
	p.calls++
	usage := llm.Usage{InputTokens: 10, OutputTokens: 5}
	if turn >= p.maxCalls {
		return llm.Response{Text: "done", Usage: usage}, nil
	}
	return llm.Response{
		ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("c%d", turn), Name: "noop", Args: json.RawMessage(`{}`)}},
		Usage:     usage,
	}, nil
}

// TestRunSnapshotNoRace mimics autosave snapshotting a workspace while another
// agent is mid-Run: Snapshot/ContextUsage must not race Run's transcript writes.
// Must be race-free under `go test -race`.
func TestRunSnapshotNoRace(t *testing.T) {
	reg := tool.NewRegistry()
	reg.MustRegister(raceNoopTool{})
	a := New(&raceLoopProvider{maxCalls: 200}, reg, "sys")

	done := make(chan struct{})
	go func() {
		_, _ = a.Run(context.Background(), "go", nil, func(Event) {})
		close(done)
	}()
	for {
		select {
		case <-done:
			return
		default:
			_ = a.Snapshot()
			_, _, _ = a.ContextUsage()
		}
	}
}
