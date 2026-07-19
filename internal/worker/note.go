package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/session"
	"github.com/syrull/pluto/internal/tool"
)

// noteTool lets a worker append structured facts to the shared blackboard and,
// optionally, claim/release a lease so two workers don't duplicate a unit of
// work. Every worker gets its own noteTool bound to its id, so facts are always
// attributed and the append-only log stays a faithful action record.
type noteTool struct {
	board  *session.Blackboard
	worker string
}

var _ tool.Tool = (*noteTool)(nil)

func newNoteTool(board *session.Blackboard, worker string) *noteTool {
	return &noteTool{board: board, worker: worker}
}

func (*noteTool) Name() string { return "note" }

func (*noteTool) Description() string {
	return "Record a structured finding on the shared blackboard so the " +
		"orchestrator and sibling workers see it. action defaults to \"fact\": set " +
		"kind (host, service, cred, foothold, vuln, flag, note, or summary) and value " +
		"(the finding), with optional detail. Use action \"claim\" with value=<unit> " +
		"to lease a unit of work before starting it (returns whether you got it, so " +
		"two workers don't duplicate effort), and \"release\" to hand it back."
}

func (*noteTool) Schema() json.RawMessage {
	return tool.ObjectSchema(map[string]tool.Property{
		"action": {
			Type:        "string",
			Description: "One of fact (default), claim, or release.",
			Enum:        []string{"fact", "claim", "release"},
		},
		"kind": {
			Type:        "string",
			Description: "For a fact: the finding kind.",
			Enum: []string{
				session.FactHost, session.FactService, session.FactCred,
				session.FactFoothold, session.FactVuln, session.FactFlag,
				session.FactNote, session.FactSummary,
			},
		},
		"value":  {Type: "string", Description: "The finding text (fact) or the unit key (claim/release)."},
		"detail": {Type: "string", Description: "Optional supporting detail for a fact."},
	}, "value").MustJSON()
}

type noteArgs struct {
	Action string `json:"action"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Detail string `json:"detail"`
}

func (n *noteTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a noteArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("note: invalid arguments: %w", err)
	}
	value := strings.TrimSpace(a.Value)
	if value == "" {
		return "", fmt.Errorf("note: value is required")
	}
	switch strings.ToLower(strings.TrimSpace(a.Action)) {
	case "claim":
		if n.board.Claim(n.worker, value) {
			return fmt.Sprintf("lease granted: %q — you own this unit", value), nil
		}
		holder := n.board.Leases()[value]
		return fmt.Sprintf("lease denied: %q is already held by %s — pick another unit", value, holder), nil
	case "release":
		n.board.Release(n.worker, value)
		return fmt.Sprintf("lease released: %q", value), nil
	default: // fact
		kind := session.NormalizeKind(a.Kind)
		f, ok := n.board.Append(n.worker, kind, value, a.Detail)
		if !ok {
			return "", fmt.Errorf("note: could not record fact")
		}
		debug.Debug("worker", "note recorded", "worker", n.worker, "kind", f.Kind, "seq", f.Seq)
		return fmt.Sprintf("recorded %s #%d", f.Kind, f.Seq), nil
	}
}
