// Package llm defines the model-provider contract and message types.
package llm

import (
	"context"
	"encoding/json"
)

// Role identifies the author of a Message.
type Role string

const (
	RoleSystem Role = "system"
	RoleUser   Role = "user"
	RoleModel  Role = "model"
	RoleTool   Role = "tool"
)

// Message is a single entry in the conversation transcript.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
	// ToolName is set on RoleTool messages to identify which tool produced
	// the content.
	ToolName string `json:"tool_name,omitempty"`
	// ToolCallID correlates a RoleTool result with the ToolCall it answers
	// (Anthropic tool_use.id ↔ tool_result.tool_use_id).
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolCalls are the tool invocations a RoleModel turn requested. Preserved
	// on the transcript so the provider can replay the assistant turn verbatim.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// Thinking and ThinkingSig hold an extended-thinking block emitted on a
	// RoleModel turn. Anthropic requires them replayed verbatim (with the
	// signature) on subsequent turns when thinking is combined with tools.
	Thinking    string `json:"thinking,omitempty"`
	ThinkingSig string `json:"thinking_sig,omitempty"`
}

// ToolCall is a model request to invoke a named tool with JSON arguments.
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// Response is one turn from the provider.
type Response struct {
	Text        string
	Thinking    string
	ThinkingSig string
	ToolCalls   []ToolCall
	Usage       Usage
}

// Usage reports token counts for a single provider turn.
type Usage struct {
	// InputTokens is the full prompt size sent, including any cached tokens.
	InputTokens int
	// OutputTokens is the tokens generated in this turn.
	OutputTokens int
}

// ToolSpec is the model-facing description of a tool.
type ToolSpec struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// Provider is any model backend the agent can drive.
type Provider interface {
	// Name identifies the backend, for display.
	Name() string
	// Generate produces the next Response given the full transcript and the
	// specs of tools available for selection.
	Generate(ctx context.Context, transcript []Message, tools []ToolSpec) (Response, error)
}

// Switchable is an optional capability to enumerate and switch active models at runtime.
type Switchable interface {
	// Model returns the current model id.
	Model() string
	// Available lists selectable model ids.
	Available() []string
	// SetModel switches the active model. Implementations SHOULD accept any id
	// from Available and MAY accept others.
	SetModel(model string)
}

// ContextWindower is an optional capability reporting the active model's total
// context window, in tokens.
type ContextWindower interface {
	// ContextWindow returns the active model's context window size in tokens.
	ContextWindow() int
}

// ThinkLevel is a named extended-thinking effort level.
type ThinkLevel string

const (
	ThinkNone   ThinkLevel = "none"
	ThinkLow    ThinkLevel = "low"
	ThinkMedium ThinkLevel = "medium"
	ThinkHigh   ThinkLevel = "high"
	ThinkXHigh  ThinkLevel = "xhigh"
	ThinkMax    ThinkLevel = "max"
)

// Thinkable is an optional capability to adjust extended-thinking effort at runtime.
type Thinkable interface {
	// ThinkLevel reports the current effort level (ThinkNone when disabled).
	ThinkLevel() ThinkLevel
	// SetThinkLevel sets the effort level. ThinkNone disables thinking.
	// Implementations SHOULD clamp an unsupported level to their nearest
	// supported one rather than erroring.
	SetThinkLevel(level ThinkLevel)
	// ThinkLevels lists the levels this provider supports for the active
	// model, in ascending effort order, always beginning with ThinkNone.
	ThinkLevels() []ThinkLevel
}

// Thinking reports whether l is any level other than none.
func (l ThinkLevel) Thinking() bool { return l != "" && l != ThinkNone }

// DeltaKind classifies an incremental streaming chunk.
type DeltaKind string

const (
	// DeltaText is user-visible answer text.
	DeltaText DeltaKind = "text"
	// DeltaThinking is extended-thinking reasoning content.
	DeltaThinking DeltaKind = "thinking"
)

// StreamDelta is one incremental chunk emitted during streaming generation.
type StreamDelta struct {
	Kind DeltaKind
	Text string
}

// StreamingProvider is an optional capability to stream output token-by-token.
type StreamingProvider interface {
	Provider
	GenerateStream(ctx context.Context, transcript []Message, tools []ToolSpec, onDelta func(StreamDelta)) (Response, error)
}
