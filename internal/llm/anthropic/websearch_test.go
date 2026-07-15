package anthropic

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/syrull/pluto/internal/llm"
)

func TestResolveWebSearchMaxUses(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want int
	}{
		{"unset defaults on", "", defaultWebSearchMaxUses},
		{"zero", "0", 0},
		{"false lowercase", "false", 0},
		{"FALSE uppercase", "FALSE", 0},
		{"off lowercase", "off", 0},
		{"OFF uppercase", "OFF", 0},
		{"no lowercase", "no", 0},
		{"NO uppercase", "NO", 0},
		{"one", "1", defaultWebSearchMaxUses},
		{"true lowercase", "true", defaultWebSearchMaxUses},
		{"TRUE uppercase", "TRUE", defaultWebSearchMaxUses},
		{"on lowercase", "on", defaultWebSearchMaxUses},
		{"ON uppercase", "ON", defaultWebSearchMaxUses},
		{"yes lowercase", "yes", defaultWebSearchMaxUses},
		{"YES uppercase", "YES", defaultWebSearchMaxUses},
		{"explicit positive int", "7", 7},
		{"explicit large int", "42", 42},
		{"negative int defaults on", "-3", defaultWebSearchMaxUses},
		{"garbage defaults on", "garbage", defaultWebSearchMaxUses},
		{"whitespace trimmed", "  true  ", defaultWebSearchMaxUses},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_WEB_SEARCH", tt.env)
			got := resolveWebSearchMaxUses()
			if got != tt.want {
				t.Fatalf("resolveWebSearchMaxUses() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBuildRequestAppendsWebSearchTool(t *testing.T) {
	// Test 1: webSearchMaxUses=5 produces a web_search tool appended after client tools.
	p := &Provider{model: "claude-sonnet-5", maxTok: defaultMaxTok, thinkLvl: llm.ThinkNone, webSearchMaxUses: 5}
	req := p.buildRequest(nil, nil, false)
	if len(req.Tools) != 1 {
		t.Fatalf("with no client tools, want 1 tool, got %d", len(req.Tools))
	}
	wst := req.Tools[0]
	if wst.Type != webSearchToolVersion {
		t.Fatalf("web_search tool Type = %q, want %q", wst.Type, webSearchToolVersion)
	}
	if wst.Name != webSearchToolName {
		t.Fatalf("web_search tool Name = %q, want %q", wst.Name, webSearchToolName)
	}
	if wst.MaxUses != 5 {
		t.Fatalf("web_search tool MaxUses = %d, want 5", wst.MaxUses)
	}
	if len(wst.InputSchema) != 0 {
		t.Fatalf("web_search tool InputSchema should be empty, got %q", wst.InputSchema)
	}

	// Test 2: with a client tool, web_search is appended after it.
	clientTool := llm.ToolSpec{
		Name:        "read",
		Description: "Read file",
		Schema:      json.RawMessage(`{"type":"object"}`),
	}
	req = p.buildRequest(nil, []llm.ToolSpec{clientTool}, false)
	if len(req.Tools) != 2 {
		t.Fatalf("with one client tool, want 2 tools, got %d", len(req.Tools))
	}
	ct := req.Tools[0]
	if ct.Type != "" {
		t.Fatalf("client tool Type should be empty, got %q", ct.Type)
	}
	if ct.Name != "read" {
		t.Fatalf("client tool Name = %q, want read", ct.Name)
	}
	if len(ct.InputSchema) == 0 {
		t.Fatalf("client tool InputSchema should not be empty")
	}

	wst = req.Tools[1]
	if wst.Type != webSearchToolVersion {
		t.Fatalf("web_search appended tool Type = %q, want %q", wst.Type, webSearchToolVersion)
	}

	// Test 3: webSearchMaxUses=0 does not append a web_search tool.
	p.webSearchMaxUses = 0
	req = p.buildRequest(nil, []llm.ToolSpec{clientTool}, false)
	if len(req.Tools) != 1 {
		t.Fatalf("with webSearchMaxUses=0, want 1 tool, got %d", len(req.Tools))
	}
	if req.Tools[0].Name != "read" {
		t.Fatalf("only client tool should remain, got %q", req.Tools[0].Name)
	}
}

func TestMapResponseWebSearchResultAndCitations(t *testing.T) {
	// Build a realistic wireResponse with text, server_tool_use, web_search_tool_result, and citations.
	respJSON := `{
		"content": [
			{"type": "text", "text": "I found information."},
			{"type": "server_tool_use", "id": "search_1", "name": "web_search", "input": {}},
			{"type": "web_search_tool_result", "id": "search_1", "content": [
				{"type": "web_search_result", "url": "https://example.com", "title": "Example Site"},
				{"type": "web_search_result", "url": "https://example.org", "title": ""}
			]},
			{"type": "text", "text": " Here is the answer.", "citations": [
				{"type": "web_search_result_location", "url": "https://example.com", "title": "Example Site"},
				{"type": "web_search_result_location", "url": "https://example.com", "title": "Example Site"},
				{"type": "web_search_result_location", "url": "https://example.org", "title": ""}
			]}
		],
		"usage": {"input_tokens": 100, "output_tokens": 50}
	}`

	var wire wireResponse
	if err := json.Unmarshal([]byte(respJSON), &wire); err != nil {
		t.Fatalf("json.Unmarshal wireResponse: %v", err)
	}

	resp := mapResponse(wire)

	// Assert text is concatenated (server_tool_use and web_search_tool_result skipped).
	expectedText := "I found information. Here is the answer.\n\nSources:\n- Example Site (https://example.com)\n- https://example.org (https://example.org)\n"
	if resp.Text != expectedText {
		t.Fatalf("resp.Text = %q\nwant %q", resp.Text, expectedText)
	}

	// Assert no ToolCalls (server_tool_use blocks are not client-actionable).
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("resp.ToolCalls should be empty, got %d", len(resp.ToolCalls))
	}

	// Assert the web search is surfaced as a ServerToolUse for display.
	if len(resp.ServerToolUses) != 1 {
		t.Fatalf("resp.ServerToolUses = %d, want 1", len(resp.ServerToolUses))
	}
	st := resp.ServerToolUses[0]
	if st.Name != webSearchToolName {
		t.Fatalf("ServerToolUse.Name = %q, want %q", st.Name, webSearchToolName)
	}
	if st.Args != "{}" {
		t.Fatalf("ServerToolUse.Args = %q, want %q", st.Args, "{}")
	}
	wantResult := "Example Site (https://example.com)\nhttps://example.org"
	if st.Result != wantResult {
		t.Fatalf("ServerToolUse.Result = %q\nwant %q", st.Result, wantResult)
	}

	// Assert sources are deduplicated by URL in first-seen order and titles fall back to URL.
	if !strings.Contains(resp.Text, "Example Site (https://example.com)") {
		t.Fatalf("citations footer missing expected source 1")
	}
	if !strings.Contains(resp.Text, "https://example.org (https://example.org)") {
		t.Fatalf("citations footer missing expected source 2 with fallback title")
	}
}

func TestWebSearchResultSummary(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"two results", `[{"type":"web_search_result","url":"https://a.com","title":"A"},{"type":"web_search_result","url":"https://b.com","title":"B"}]`, "A (https://a.com)\nB (https://b.com)"},
		{"title falls back to url", `[{"type":"web_search_result","url":"https://a.com","title":""}]`, "https://a.com"},
		{"empty list", `[]`, "no results"},
		{"missing content", ``, "no results"},
		{"error object", `{"type":"web_search_tool_result_error","error_code":"max_uses_exceeded"}`, "error: max_uses_exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := webSearchResultSummary([]byte(tt.raw))
			if got != tt.want {
				t.Fatalf("webSearchResultSummary(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseSSEWebSearchStream(t *testing.T) {
	// Build a realistic SSE stream with web_search_tool_result blocks and citations_delta frames.
	sseStream := `event: message_start
data: {"type":"message_start","message":{"content":[],"stop_reason":null,"usage":{"input_tokens":200,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me search"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"search_1","name":"web_search"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"golang\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"web_search_tool_result","id":"search_1","content":[{"type":"web_search_result","url":"https://go.dev","title":"Go"}]}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: content_block_start
data: {"type":"content_block_start","index":3,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":" for you."}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://google.com","title":"Google"}}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://google.com","title":"Google"}}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://wikipedia.org","title":""}}}

event: content_block_stop
data: {"type":"content_block_stop","index":3}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":75}}

event: message_stop
data: {"type":"message_stop"}
`

	var deltas []llm.StreamDelta
	resp, err := parseSSE(strings.NewReader(sseStream), time.Minute, func() {}, func(d llm.StreamDelta) {
		deltas = append(deltas, d)
	})
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}

	// Assert text is correctly assembled and citations footer is appended.
	if resp.Text != "Let me search for you.\n\nSources:\n- Google (https://google.com)\n- https://wikipedia.org (https://wikipedia.org)\n" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}

	// Assert no server_tool_use blocks are surfaced as ToolCalls.
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("resp.ToolCalls should be empty, got %d", len(resp.ToolCalls))
	}

	// Assert citations footer was emitted via onDelta.
	var foundFooter bool
	for _, d := range deltas {
		if strings.Contains(d.Text, "Sources:") {
			foundFooter = true
			break
		}
	}
	if !foundFooter {
		t.Fatalf("citations footer not found in deltas")
	}

	// Assert the web search call (with its query) and result were surfaced as
	// deltas, in order, before the answer text that follows.
	var call, result *llm.StreamDelta
	callIdx, textAfterIdx := -1, -1
	for i := range deltas {
		switch deltas[i].Kind {
		case llm.DeltaServerToolCall:
			call = &deltas[i]
			callIdx = i
		case llm.DeltaServerToolResult:
			result = &deltas[i]
		case llm.DeltaText:
			if strings.Contains(deltas[i].Text, "for you") {
				textAfterIdx = i
			}
		}
	}
	if call == nil || call.Tool != webSearchToolName || !strings.Contains(call.Text, "golang") {
		t.Fatalf("web search call delta missing or wrong: %+v", call)
	}
	if result == nil || result.Tool != webSearchToolName || !strings.Contains(result.Text, "Go (https://go.dev)") {
		t.Fatalf("web search result delta missing or wrong: %+v", result)
	}
	if callIdx == -1 || textAfterIdx == -1 || callIdx > textAfterIdx {
		t.Fatalf("web search deltas must precede the trailing answer text (call=%d textAfter=%d)", callIdx, textAfterIdx)
	}
}

func TestBuildMessagesToolResultRoundTrips(t *testing.T) {
	// Construct a transcript with a RoleTool message containing plain text content.
	transcript := []llm.Message{
		{Role: llm.RoleUser, Content: "use read"},
		{Role: llm.RoleModel, Content: "ok", ToolCalls: []llm.ToolCall{
			{ID: "t1", Name: "read", Args: json.RawMessage(`{"path":"file.txt"}`)},
		}},
		{Role: llm.RoleTool, ToolCallID: "t1", ToolName: "read", Content: "result text"},
	}

	msgs := buildMessages(transcript, true)

	// Find the tool_result block in the last message (should be coalesced into the tool-result user message).
	var toolResultBlock wireBlock
	found := false
	for _, msg := range msgs {
		if msg.Role == "user" {
			for _, block := range msg.Content {
				if block.Type == "tool_result" {
					toolResultBlock = block
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Fatalf("tool_result block not found in messages")
	}

	// Assert that the Content field can be unmarshaled back to the original string.
	// This proves that jsonString() encoded it correctly and it's stored as json.RawMessage.
	var roundTripped string
	if err := json.Unmarshal(toolResultBlock.Content, &roundTripped); err != nil {
		t.Fatalf("json.Unmarshal tool_result.Content: %v", err)
	}
	if roundTripped != "result text" {
		t.Fatalf("roundTripped content = %q, want %q", roundTripped, "result text")
	}
}
