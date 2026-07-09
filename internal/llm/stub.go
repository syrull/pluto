package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Stub is an offline Provider for development and demos.
type Stub struct{}

var _ Provider = Stub{}

func (Stub) Name() string { return "stub-echo" }

func (Stub) Generate(_ context.Context, transcript []Message, _ []ToolSpec) (Response, error) {
	last := lastRelevant(transcript)

	// If we're responding to a tool result, summarize and stop.
	if last.Role == RoleTool {
		return Response{Text: fmt.Sprintf("[%s] %s", last.ToolName, strings.TrimSpace(last.Content))}, nil
	}

	fields := strings.Fields(last.Content)
	if len(fields) == 0 {
		return Response{Text: ""}, nil
	}

	switch fields[0] {
	case "read":
		if len(fields) < 2 {
			return Response{Text: "usage: read <path>"}, nil
		}
		args, _ := json.Marshal(map[string]string{"path": fields[1]})
		return Response{ToolCalls: []ToolCall{{ID: "stub-read", Name: "read", Args: args}}}, nil

	case "write":
		if len(fields) < 3 {
			return Response{Text: "usage: write <path> <content>"}, nil
		}
		path := fields[1]
		content := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(last.Content), "write "+path))
		args, _ := json.Marshal(map[string]string{"path": path, "content": content})
		return Response{ToolCalls: []ToolCall{{ID: "stub-write", Name: "write", Args: args}}}, nil

	default:
		return Response{Text: "echo: " + last.Content}, nil
	}
}

func lastRelevant(transcript []Message) Message {
	for i := len(transcript) - 1; i >= 0; i-- {
		if r := transcript[i].Role; r == RoleUser || r == RoleTool {
			return transcript[i]
		}
	}
	return Message{}
}
