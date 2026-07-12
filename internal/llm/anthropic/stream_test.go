package anthropic

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/syrull/pluto/internal/llm"
)

const sseFixture = `event: message_start
data: {"type":"message_start","message":{"content":[],"stop_reason":null,"usage":{"input_tokens":1200,"output_tokens":1,"cache_read_input_tokens":300}}}

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
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}

event: message_stop
data: {"type":"message_stop"}
`

func TestParseSSEAssemblesTurn(t *testing.T) {
	var textDeltas, thinkDeltas []string
	resp, err := parseSSE(strings.NewReader(sseFixture), time.Minute, func() {}, func(d llm.StreamDelta) {
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

	// message_start input (with cache reads folded in) + message_delta output.
	if resp.Usage.InputTokens != 1500 {
		t.Fatalf("resp.Usage.InputTokens = %d, want 1500", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 42 {
		t.Fatalf("resp.Usage.OutputTokens = %d, want 42", resp.Usage.OutputTokens)
	}
}

func TestParseSSEError(t *testing.T) {
	fixture := `event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"slow down"}}
`
	_, err := parseSSE(strings.NewReader(fixture), time.Minute, func() {}, func(llm.StreamDelta) {})
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
	resp, err := parseSSE(strings.NewReader(fixture), time.Minute, func() {}, func(llm.StreamDelta) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Args) != "{}" {
		t.Fatalf("want empty-object args, got %+v", resp.ToolCalls)
	}
}

func TestParseSSEIdleTimeout(t *testing.T) {
	pr, pw := io.Pipe()

	// Cancel closes the reader, unblocking the blocked scanner.Scan()
	cancel := func() { pr.CloseWithError(context.Canceled) }

	// Capture text deltas
	var textDeltas []string

	// Writer goroutine: send one complete text block, then stall (don't close pw)
	go func() {
		frames := []byte(`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

`)
		pw.Write(frames)
		// Intentionally don't close pw — this is a stall, not a clean EOF
	}()

	// Should timeout after 50ms of no frames
	resp, err := parseSSE(pr, 50*time.Millisecond, cancel, func(d llm.StreamDelta) {
		if d.Kind == llm.DeltaText {
			textDeltas = append(textDeltas, d.Text)
		}
	})

	// Must error with "stream stalled"
	if err == nil {
		t.Fatal("expected stall error, got none")
	}
	if !strings.Contains(err.Error(), "stream stalled") {
		t.Fatalf("expected 'stream stalled' in error, got %v", err)
	}

	// Deltas that arrived before stall must be preserved
	if got := strings.Join(textDeltas, ""); got != "hi" {
		t.Fatalf("text deltas = %q, want %q", got, "hi")
	}

	// Response must contain the text that arrived before stall
	if resp.Text != "hi" {
		t.Fatalf("resp.Text = %q, want %q", resp.Text, "hi")
	}
}

func TestParseSSEResetsOnActivity(t *testing.T) {
	pr, pw := io.Pipe()

	// Cancel closes the reader, unblocking the blocked scanner.Scan()
	cancel := func() { pr.CloseWithError(context.Canceled) }

	// Capture text deltas
	var textDeltas []string

	// Writer goroutine: send frames spaced 40ms apart.
	// With 6 deltas, total time is ~240ms, which exceeds the 200ms idle timeout.
	// But since the watchdog resets on EVERY frame, it never fires.
	go func() {
		defer pw.Close()

		// Start the text block
		pw.Write([]byte(`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

`))
		time.Sleep(40 * time.Millisecond)

		// Send 6 text deltas, each 40ms apart
		for _, ch := range []string{"a", "b", "c", "d", "e", "f"} {
			pw.Write([]byte(`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + ch + `"}}

`))
			time.Sleep(40 * time.Millisecond)
		}

		// Stop the block
		pw.Write([]byte(`event: content_block_stop
data: {"type":"content_block_stop","index":0}

`))
		time.Sleep(40 * time.Millisecond)

		// Message stop (terminal frame)
		pw.Write([]byte(`event: message_stop
data: {"type":"message_stop"}

`))
	}()

	// Use 200ms idle timeout. Total time is >240ms, but frames every 40ms,
	// so no single inter-frame gap exceeds 200ms.
	resp, err := parseSSE(pr, 200*time.Millisecond, cancel, func(d llm.StreamDelta) {
		if d.Kind == llm.DeltaText {
			textDeltas = append(textDeltas, d.Text)
		}
	})

	// Must complete successfully (no stall)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All deltas must be captured
	if got := strings.Join(textDeltas, ""); got != "abcdef" {
		t.Fatalf("text deltas = %q, want %q", got, "abcdef")
	}

	// Response must contain all deltas
	if resp.Text != "abcdef" {
		t.Fatalf("resp.Text = %q, want %q", resp.Text, "abcdef")
	}
}
