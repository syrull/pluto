package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/tool"
)

// DispatchTool is the orchestrator's non-blocking fan-out tool. It lives in the
// worker package (not internal/tools) because it wraps a *Pool, and the tools
// package must stay free of a dependency on agent to avoid an import cycle. It
// dispatches worker sub-agents that run concurrently in their own agent loops,
// then polls for concise structured results on demand — worker transcripts never
// enter the orchestrator's context.
type DispatchTool struct {
	pool *Pool
}

var _ tool.Tool = (*DispatchTool)(nil)

// NewDispatchTool wraps a pool as the workers tool. A nil pool yields a tool that
// reports the feature is unavailable, so wiring order can register it safely.
func NewDispatchTool(pool *Pool) *DispatchTool { return &DispatchTool{pool: pool} }

func (*DispatchTool) Name() string { return "workers" }

func (*DispatchTool) Description() string {
	return "Fan out work to parallel worker sub-agents and gather their results " +
		"WITHOUT blocking. Each worker runs concurrently in its own agent loop with " +
		"its own context, a least-privilege tool subset, preloaded skills, and a hard " +
		"budget. Use action=dispatch to launch workers (returns their ids immediately " +
		"— you keep working); action=poll to pull current status and concise " +
		"structured results for some or all workers (non-blocking; call it on later " +
		"turns as work lands); action=cancel to stop workers. Prefer this over running " +
		"independent branches of work serially yourself. Give each worker the smallest " +
		"tool set it needs and a budget (turns/tokens/wall_s) so a stuck worker is reaped."
}

func (*DispatchTool) Schema() json.RawMessage {
	budget := tool.Property{
		Type:        "object",
		Description: "Hard per-worker budget; the worker is reaped when any limit is hit.",
		Properties: map[string]tool.Property{
			"turns":  {Type: "integer", Description: "Max model turns."},
			"tokens": {Type: "integer", Description: "Max cumulative input+output tokens."},
			"wall_s": {Type: "integer", Description: "Max wall-clock seconds."},
		},
	}
	workerSpec := tool.Property{
		Type: "object",
		Properties: map[string]tool.Property{
			"id":     {Type: "string", Description: "Optional stable id; auto-assigned when omitted."},
			"task":   {Type: "string", Description: "The scoped task for this worker (required)."},
			"tools":  {Type: "array", Description: "Allowed built-in tool names (least privilege).", Items: &tool.Property{Type: "string"}},
			"skills": {Type: "array", Description: "Skill names to preload so the worker is an instant specialist.", Items: &tool.Property{Type: "string"}},
			"budget": budget,
			"scope":  {Type: "string", Description: "Rules of engagement / target; also the per-target rate-limit key."},
		},
		Required: []string{"task"},
	}
	return tool.ObjectSchema(map[string]tool.Property{
		"action": {
			Type:        "string",
			Description: "dispatch, poll, or cancel.",
			Enum:        []string{"dispatch", "poll", "cancel"},
		},
		"workers": {
			Type:        "array",
			Description: "For dispatch: the workers to launch.",
			Items:       &workerSpec,
		},
		"ids": {
			Type:        "array",
			Description: "For poll (optional; omit for all) and cancel (required): worker ids.",
			Items:       &tool.Property{Type: "string"},
		},
	}, "action").MustJSON()
}

// dispatchArgs is the parsed tool input across all three actions.
type dispatchArgs struct {
	Action  string            `json:"action"`
	Workers []dispatchSpecArg `json:"workers"`
	IDs     []string          `json:"ids"`
}

type dispatchSpecArg struct {
	ID     string    `json:"id"`
	Task   string    `json:"task"`
	Tools  []string  `json:"tools"`
	Skills []string  `json:"skills"`
	Budget budgetArg `json:"budget"`
	Scope  string    `json:"scope"`
}

type budgetArg struct {
	Turns  int `json:"turns"`
	Tokens int `json:"tokens"`
	WallS  int `json:"wall_s"`
}

func (b budgetArg) toBudget() agent.Budget {
	return agent.Budget{
		Turns:  b.Turns,
		Tokens: b.Tokens,
		Wall:   time.Duration(b.WallS) * time.Second,
	}
}

func (d *DispatchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if d.pool == nil {
		return "", fmt.Errorf("workers: parallel workers are not available in this build")
	}
	var a dispatchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("workers: invalid arguments: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(a.Action)) {
	case "dispatch":
		return d.dispatch(ctx, a.Workers)
	case "poll":
		return d.poll(a.IDs)
	case "cancel":
		return d.cancel(a.IDs)
	default:
		return "", fmt.Errorf("workers: unknown action %q (want dispatch, poll, or cancel)", a.Action)
	}
}

func (d *DispatchTool) dispatch(ctx context.Context, specs []dispatchSpecArg) (string, error) {
	if len(specs) == 0 {
		return "", fmt.Errorf("workers: dispatch needs at least one worker")
	}
	out := make([]Spec, 0, len(specs))
	for _, s := range specs {
		out = append(out, Spec{
			ID:     strings.TrimSpace(s.ID),
			Task:   s.Task,
			Tools:  s.Tools,
			Skills: s.Skills,
			Budget: s.Budget.toBudget(),
			Scope:  s.Scope,
		})
	}
	timer := debug.NewTimer("orchestrator", "dispatch tool")
	ids := d.pool.Dispatch(ctx, out)
	timer.Stop("requested", len(out), "launched", len(ids))
	if len(ids) == 0 {
		return "", fmt.Errorf("workers: no workers launched (every task was blank)")
	}
	return marshalResult(map[string]any{
		"ids":  ids,
		"note": "workers launched and running in parallel — keep working; call action=poll on a later turn to gather results.",
	}), nil
}

func (d *DispatchTool) poll(ids []string) (string, error) {
	statuses := d.pool.Poll(ids)
	views := make([]pollView, 0, len(statuses))
	for _, s := range statuses {
		views = append(views, toPollView(s))
	}
	debug.Debug("orchestrator", "poll tool", "requested", len(ids), "returned", len(views))
	return marshalResult(map[string]any{"workers": views}), nil
}

func (d *DispatchTool) cancel(ids []string) (string, error) {
	if len(ids) == 0 {
		return "", fmt.Errorf("workers: cancel needs one or more ids")
	}
	canceled := d.pool.Cancel(ids)
	debug.Info("orchestrator", "cancel tool", "requested", len(ids), "canceled", len(canceled))
	return marshalResult(map[string]any{"cancelled": canceled}), nil
}

// pollView is the concise, orchestrator-facing shape of a worker's status: never
// the transcript, only its state, budget burn, and structured results.
type pollView struct {
	ID          string  `json:"id"`
	State       string  `json:"state"`
	Scope       string  `json:"scope,omitempty"`
	CurrentTool string  `json:"current_tool,omitempty"`
	StopReason  string  `json:"stop_reason,omitempty"`
	ToolCalls   int     `json:"tool_calls"`
	Tokens      int     `json:"tokens"`
	ElapsedS    float64 `json:"elapsed_s"`
	Results     Results `json:"results"`
	Error       string  `json:"error,omitempty"`
}

func toPollView(s Status) pollView {
	return pollView{
		ID:          s.ID,
		State:       string(s.State),
		Scope:       s.Scope,
		CurrentTool: s.CurrentTool,
		StopReason:  s.StopReason,
		ToolCalls:   s.ToolCalls,
		Tokens:      s.Tokens,
		ElapsedS:    s.Elapsed.Round(time.Millisecond).Seconds(),
		Results:     s.Results,
		Error:       s.Err,
	}
}

func marshalResult(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("{\"error\":%q}", err.Error())
	}
	return string(b)
}
