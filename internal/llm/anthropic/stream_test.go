package anthropic

import (
	"strings"
	"testing"

	"github.com/pluto/harness/internal/llm"
)

const sseFixture = `event: message_start
data: {"type":"message_start","message":{"content":[],"stop_reason":null}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"# Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"read"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":" \"a.txt\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}

event: message_stop
data: {"type":"message_stop"}
`

func TestParseSSEAssemblesTurn(t *testing.T) {
	var textDeltas, thinkDeltas []string
	resp, err := parseSSE(strings.NewReader(sseFixture), func(d llm.StreamDelta) {
		switch d.Kind {
		case llm.DeltaText:
			textDeltas = append(textDeltas, d.Text)
		case llm.DeltaThinking:
			thinkDeltas = append(thinkDeltas, d.Text)
		}
	})
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}

	// Deltas streamed incrementally, in order.
	if got := strings.Join(textDeltas, ""); got != "# Hello world" {
		t.Fatalf("text deltas = %q", got)
	}
	if got := strings.Join(thinkDeltas, ""); got != "Let me reason." {
		t.Fatalf("thinking deltas = %q", got)
	}

	// Final assembled response.
	if resp.Text != "# Hello world" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if resp.Thinking != "Let me reason." || resp.ThinkingSig != "sig-abc" {
		t.Fatalf("resp thinking = %q sig = %q", resp.Thinking, resp.ThinkingSig)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Name != "read" || string(tc.Args) != `{"path": "a.txt"}` {
		t.Fatalf("tool call = %+v args=%s", tc, tc.Args)
	}
}

func TestParseSSEError(t *testing.T) {
	fixture := `event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"slow down"}}
`
	_, err := parseSSE(strings.NewReader(fixture), func(llm.StreamDelta) {})
	if err == nil || !strings.Contains(err.Error(), "overloaded_error") {
		t.Fatalf("expected overloaded_error, got %v", err)
	}
}

func TestParseSSEToolOnlyEmptyArgs(t *testing.T) {
	fixture := `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t","name":"noop"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}
`
	resp, err := parseSSE(strings.NewReader(fixture), func(llm.StreamDelta) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Args) != "{}" {
		t.Fatalf("want empty-object args, got %+v", resp.ToolCalls)
	}
}
