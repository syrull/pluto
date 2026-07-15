package anthropic

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/syrull/pluto/internal/llm"
)

func TestBuildMessagesCoalescesToolResults(t *testing.T) {
	transcript := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"}, // must be skipped here
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleModel, Content: "working", ToolCalls: []llm.ToolCall{
			{ID: "t1", Name: "read", Args: json.RawMessage(`{"path":"a"}`)},
			{ID: "t2", Name: "read", Args: json.RawMessage(`{"path":"b"}`)},
		}},
		{Role: llm.RoleTool, ToolCallID: "t1", ToolName: "read", Content: "A"},
		{Role: llm.RoleTool, ToolCallID: "t2", ToolName: "read", Content: "B"},
	}

	msgs := buildMessages(transcript, true)
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 (user, assistant, tool-result user)", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[2].Role != "user" {
		t.Fatalf("roles = %q/%q/%q", msgs[0].Role, msgs[1].Role, msgs[2].Role)
	}

	// Assistant turn: leading text + two tool_use blocks.
	asst := msgs[1].Content
	if len(asst) != 3 || asst[0].Type != "text" || asst[1].Type != "tool_use" || asst[2].Type != "tool_use" {
		t.Fatalf("assistant blocks = %+v", asst)
	}
	if asst[1].ID != "t1" || asst[2].ID != "t2" {
		t.Fatalf("tool_use ids = %q,%q, want t1,t2", asst[1].ID, asst[2].ID)
	}

	// Both results coalesced into one user message, correlated by id.
	res := msgs[2].Content
	if len(res) != 2 || res[0].Type != "tool_result" || res[1].Type != "tool_result" {
		t.Fatalf("result blocks = %+v", res)
	}
	if res[0].ToolUseID != "t1" || res[1].ToolUseID != "t2" {
		t.Fatalf("tool_use_ids = %q,%q, want t1,t2", res[0].ToolUseID, res[1].ToolUseID)
	}
}

func TestBuildMessagesFoldsSteeringAfterToolResult(t *testing.T) {
	transcript := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleModel, ToolCalls: []llm.ToolCall{{ID: "t1", Name: "read", Args: json.RawMessage(`{}`)}}},
		{Role: llm.RoleTool, ToolCallID: "t1", ToolName: "read", Content: "R"},
		{Role: llm.RoleUser, Content: "actually stop"}, // steered in after the tool result
	}

	msgs := buildMessages(transcript, true)
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 (no dangling second user turn)", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[2].Role != "user" {
		t.Fatalf("roles = %q/%q/%q", msgs[0].Role, msgs[1].Role, msgs[2].Role)
	}
	// The steering text rides in the same user turn as the tool result, after it.
	c := msgs[2].Content
	if len(c) != 2 || c[0].Type != "tool_result" || c[1].Type != "text" || c[1].Text != "actually stop" {
		t.Fatalf("tool-result turn should carry the steering text after the result: %+v", c)
	}
}

func TestBuildMessagesMergesConsecutiveUsers(t *testing.T) {
	msgs := buildMessages([]llm.Message{
		{Role: llm.RoleUser, Content: "one"},
		{Role: llm.RoleUser, Content: "two"},
	}, true)
	if len(msgs) != 1 || len(msgs[0].Content) != 2 {
		t.Fatalf("consecutive user turns should merge into one message, got %+v", msgs)
	}
	if msgs[0].Content[0].Text != "one" || msgs[0].Content[1].Text != "two" {
		t.Fatalf("merged blocks = %+v, want [one two]", msgs[0].Content)
	}
}

func TestBuildMessagesEmitsImageBlocks(t *testing.T) {
	transcript := []llm.Message{
		{Role: llm.RoleUser, Content: "what is this?", Attachments: []llm.Attachment{
			{Kind: llm.AttachmentImage, MediaType: "image/png", Data: []byte{0x89, 0x50, 0x4e, 0x47}},
		}},
	}

	msgs := buildMessages(transcript, true)
	if len(msgs) != 1 || len(msgs[0].Content) != 2 {
		t.Fatalf("want one user message with an image + text block, got %+v", msgs)
	}
	img, txt := msgs[0].Content[0], msgs[0].Content[1]
	if img.Type != "image" || img.Source == nil {
		t.Fatalf("first block = %+v, want an image block with a source", img)
	}
	if img.Source.Type != "base64" || img.Source.MediaType != "image/png" || img.Source.Data == "" {
		t.Fatalf("image source = %+v, want base64 png with data", img.Source)
	}
	if txt.Type != "text" || txt.Text != "what is this?" {
		t.Fatalf("second block = %+v, want the text after the image", txt)
	}
}

func TestBuildMessagesDropsImagesWithoutVision(t *testing.T) {
	transcript := []llm.Message{
		{Role: llm.RoleUser, Content: "hi", Attachments: []llm.Attachment{
			{Kind: llm.AttachmentImage, MediaType: "image/png", Data: []byte{1, 2, 3}},
		}},
	}

	msgs := buildMessages(transcript, false)
	if len(msgs) != 1 || len(msgs[0].Content) != 1 || msgs[0].Content[0].Type != "text" {
		t.Fatalf("non-vision model should get text only, got %+v", msgs)
	}
}

func TestBuildMessagesImageOnlyTurn(t *testing.T) {
	transcript := []llm.Message{
		{Role: llm.RoleUser, Attachments: []llm.Attachment{
			{Kind: llm.AttachmentImage, MediaType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff}},
		}},
	}

	msgs := buildMessages(transcript, true)
	if len(msgs) != 1 || len(msgs[0].Content) != 1 || msgs[0].Content[0].Type != "image" {
		t.Fatalf("image-only turn should carry a single image block, got %+v", msgs)
	}
}

func TestCacheBreakpoints(t *testing.T) {
	p := &Provider{model: "claude-opus-4-8", maxTok: defaultMaxTok, thinkLvl: llm.ThinkNone}
	transcript := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "one"},
		{Role: llm.RoleModel, Content: "reply one"},
		{Role: llm.RoleUser, Content: "two"},
	}
	tools := []llm.ToolSpec{{Name: "read", Description: "reads", Schema: json.RawMessage(`{"type":"object"}`)}}

	req := p.buildRequest(transcript, tools, false)

	// Last system block carries the tools+system breakpoint.
	if n := len(req.System); n == 0 || req.System[n-1].CacheControl == nil {
		t.Fatalf("last system block not marked ephemeral: %+v", req.System)
	}
	if req.System[0].CacheControl != nil && len(req.System) > 1 {
		t.Fatalf("only the last system block should be marked, got marker on block 0")
	}

	// The final two messages get a rolling breakpoint on their last block.
	msgs := req.Messages
	if n := len(msgs); n < 2 {
		t.Fatalf("want >=2 messages, got %d", n)
	}
	for _, i := range []int{len(msgs) - 1, len(msgs) - 2} {
		c := msgs[i].Content
		if len(c) == 0 || c[len(c)-1].CacheControl == nil {
			t.Fatalf("message %d last block not marked ephemeral: %+v", i, c)
		}
	}
	// An earlier message must not be marked (max 4 breakpoints; rolling window is 2).
	if n := len(msgs); n >= 3 {
		c := msgs[n-3].Content
		if len(c) > 0 && c[len(c)-1].CacheControl != nil {
			t.Fatalf("message %d should not be marked", n-3)
		}
	}
}

func TestAdaptiveThinkingWire(t *testing.T) {
	p := &Provider{model: "claude-sonnet-5", maxTok: defaultMaxTok, thinkLvl: llm.ThinkHigh}

	req := p.buildRequest(nil, nil, false)
	if req.Thinking == nil || req.Thinking.Type != "adaptive" {
		t.Fatalf("Thinking = %+v, want type adaptive", req.Thinking)
	}
	if req.Thinking.Display != "summarized" {
		t.Fatalf("Display = %q, want summarized (else adaptive thinking text is omitted)", req.Thinking.Display)
	}
	if req.Thinking.BudgetTokens != 0 {
		t.Fatalf("budget_tokens = %d, want 0 (never sent on adaptive)", req.Thinking.BudgetTokens)
	}
	if req.OutputConfig == nil || req.OutputConfig.Effort != "high" {
		t.Fatalf("OutputConfig = %+v, want effort high", req.OutputConfig)
	}

	// xhigh raises max_tokens to the high-effort floor.
	p.SetThinkLevel(llm.ThinkXHigh)
	req = p.buildRequest(nil, nil, false)
	if req.OutputConfig.Effort != "xhigh" {
		t.Fatalf("effort = %q, want xhigh", req.OutputConfig.Effort)
	}
	if req.MaxTokens < highEffortMaxTok {
		t.Fatalf("MaxTokens = %d, want >= %d at xhigh", req.MaxTokens, highEffortMaxTok)
	}

	// none on a default-on model sends type:"disabled", no effort.
	p.SetThinkLevel(llm.ThinkNone)
	req = p.buildRequest(nil, nil, false)
	if req.Thinking == nil || req.Thinking.Type != "disabled" {
		t.Fatalf("Thinking = %+v, want type disabled", req.Thinking)
	}
	if req.OutputConfig != nil {
		t.Fatalf("OutputConfig = %+v, want nil when disabled", req.OutputConfig)
	}
}

func TestAdaptiveDefaultOffOmitsDisabled(t *testing.T) {
	p := &Provider{model: "claude-opus-4-8", maxTok: defaultMaxTok, thinkLvl: llm.ThinkNone}
	req := p.buildRequest(nil, nil, false)
	if req.Thinking != nil {
		t.Fatalf("Thinking = %+v, want nil (omitted) on default-off model", req.Thinking)
	}
}

func TestLegacyThinkingWire(t *testing.T) {
	p := &Provider{model: "claude-sonnet-4-5", maxTok: defaultMaxTok, thinkLvl: llm.ThinkMax}
	req := p.buildRequest(nil, nil, false)
	budget := legacyBudgets[llm.ThinkMax]
	if req.Thinking == nil || req.Thinking.Type != "enabled" || req.Thinking.BudgetTokens != budget {
		t.Fatalf("Thinking = %+v, want enabled budget %d", req.Thinking, budget)
	}
	if req.OutputConfig != nil {
		t.Fatalf("OutputConfig = %+v, want nil on legacy (no effort)", req.OutputConfig)
	}
	if req.MaxTokens <= budget {
		t.Fatalf("MaxTokens = %d, want > budget %d", req.MaxTokens, budget)
	}

	// none omits thinking entirely.
	p.SetThinkLevel(llm.ThinkNone)
	if req = p.buildRequest(nil, nil, false); req.Thinking != nil {
		t.Fatalf("Thinking = %+v, want nil when none", req.Thinking)
	}
}

func TestNoThinkingRegime(t *testing.T) {
	p := &Provider{model: "claude-3-5-haiku-latest", maxTok: defaultMaxTok, thinkLvl: llm.ThinkHigh}
	// Even with a non-none level set, the regime forces no thinking.
	req := p.buildRequest(nil, nil, false)
	if req.Thinking != nil {
		t.Fatalf("Thinking = %+v, want nil for no-thinking model", req.Thinking)
	}
	if got := p.ThinkLevels(); len(got) != 1 || got[0] != llm.ThinkNone {
		t.Fatalf("ThinkLevels() = %v, want [none]", got)
	}
}

func TestThinkLevelClampOnModelSwitch(t *testing.T) {
	p := &Provider{model: "claude-sonnet-5", maxTok: defaultMaxTok, thinkLvl: llm.ThinkXHigh}
	if p.ThinkLevel() != llm.ThinkXHigh {
		t.Fatalf("precondition: level = %q, want xhigh", p.ThinkLevel())
	}
	// Sonnet 4.5 is legacy without xhigh -> clamp to max.
	p.SetModel("claude-sonnet-4-5")
	if p.ThinkLevel() != llm.ThinkMax {
		t.Fatalf("after switch to sonnet-4-5, level = %q, want max", p.ThinkLevel())
	}
	// No-thinking model -> clamp to none.
	p.SetModel("claude-3-5-haiku-latest")
	if p.ThinkLevel() != llm.ThinkNone {
		t.Fatalf("after switch to 3-5-haiku, level = %q, want none", p.ThinkLevel())
	}
}

func TestBuildSystemOAuthIdentity(t *testing.T) {
	transcript := []llm.Message{{Role: llm.RoleSystem, Content: "be helpful"}}

	oauth := &Provider{creds: credentials{mode: authOAuth, token: "x"}}
	sys := oauth.buildSystem(transcript)
	if len(sys) != 2 || sys[0].Text != claudeCodeIdentity || sys[1].Text != "be helpful" {
		t.Fatalf("oauth system = %+v, want [identity, user prompt]", sys)
	}

	apikey := &Provider{creds: credentials{mode: authAPIKey, token: "x"}}
	sys = apikey.buildSystem(transcript)
	if len(sys) != 1 || sys[0].Text != "be helpful" {
		t.Fatalf("apikey system = %+v, want [user prompt only]", sys)
	}
}

func TestCredentialHeaders(t *testing.T) {
	oauth := credentials{mode: authOAuth, token: "tok"}
	h := make(map[string][]string)
	oauth.apply(h)
	if h["Authorization"][0] != "Bearer tok" {
		t.Fatalf("oauth authorization = %q", h["Authorization"])
	}
	if h["Anthropic-Beta"][0] != "oauth-2025-04-20" {
		t.Fatalf("oauth beta header = %q", h["Anthropic-Beta"])
	}

	apikey := credentials{mode: authAPIKey, token: "key"}
	h = make(map[string][]string)
	apikey.apply(h)
	if h["X-Api-Key"][0] != "key" {
		t.Fatalf("apikey x-api-key = %q", h["X-Api-Key"])
	}
	if _, set := h["Authorization"]; set {
		t.Fatalf("apikey must not set Authorization")
	}
}

func TestMapResponse(t *testing.T) {
	wire := wireResponse{
		Content: []wireBlock{
			{Type: "text", Text: "sure"},
			{Type: "tool_use", ID: "u1", Name: "write", Input: json.RawMessage(`{"path":"p"}`)},
		},
		Usage: &wireUsage{InputTokens: 1000, OutputTokens: 25, CacheReadInputTokens: 500},
	}
	got := mapResponse(wire)
	if got.Text != "sure" {
		t.Fatalf("text = %q", got.Text)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "u1" || got.ToolCalls[0].Name != "write" {
		t.Fatalf("tool calls = %+v", got.ToolCalls)
	}
	if got.Usage.InputTokens != 1500 || got.Usage.OutputTokens != 25 {
		t.Fatalf("usage = %+v, want input 1500 output 25", got.Usage)
	}
}

func TestContextWindow(t *testing.T) {
	p := &Provider{model: "claude-sonnet-5"}
	if got := p.ContextWindow(); got != 1_000_000 {
		t.Fatalf("sonnet-5 context window = %d, want 1000000", got)
	}
	p.model = "claude-opus-4-8"
	if got := p.ContextWindow(); got != 1_000_000 {
		t.Fatalf("opus-4-8 context window = %d, want 1000000", got)
	}
	p.model = "claude-haiku-4-5"
	if got := p.ContextWindow(); got != 200_000 {
		t.Fatalf("haiku-4-5 context window = %d, want 200000", got)
	}
	p.model = "unknown-model"
	if got := p.ContextWindow(); got != defaultContextWindow {
		t.Fatalf("unknown model context window = %d, want %d", got, defaultContextWindow)
	}
}

func TestBuildMessagesReplaysThinkingFirst(t *testing.T) {
	transcript := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{
			Role: llm.RoleModel, Content: "sure", Thinking: "reasoning", ThinkingSig: "sig-1",
			ToolCalls: []llm.ToolCall{{ID: "t1", Name: "read", Args: []byte(`{}`)}},
		},
	}
	msgs := buildMessages(transcript, true)
	asst := msgs[1].Content
	if len(asst) != 3 {
		t.Fatalf("want 3 blocks (thinking, text, tool_use), got %d: %+v", len(asst), asst)
	}
	if asst[0].Type != "thinking" || asst[0].Thinking != "reasoning" || asst[0].Signature != "sig-1" {
		t.Fatalf("first block = %+v, want thinking with signature", asst[0])
	}
	if asst[1].Type != "text" || asst[2].Type != "tool_use" {
		t.Fatalf("block order = %q,%q,%q", asst[0].Type, asst[1].Type, asst[2].Type)
	}
}

// TestProviderConcurrentAccessNoRace exercises the shared-provider path: parallel
// agents build requests / read accessors while the UI mutates model, effort, and
// web-search settings. It must be race-free under `go test -race`.
func TestProviderConcurrentAccessNoRace(t *testing.T) {
	p := &Provider{
		model:            DefaultModel,
		creds:            credentials{mode: authAPIKey, token: "k"},
		http:             &http.Client{},
		baseURL:          defaultBaseURL,
		maxTok:           defaultMaxTok,
		thinkLvl:         defaultThinkLevel,
		webSearchMaxUses: defaultWebSearchMaxUses,
	}
	transcript := []llm.Message{{Role: llm.RoleSystem, Content: "sys"}, {Role: llm.RoleUser, Content: "hi"}}

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ { // readers: mimic parallel agents assembling requests
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				_ = p.buildRequest(transcript, nil, j%2 == 0)
				_ = p.Name()
				_ = p.ThinkLevel()
				_ = p.ThinkLevels()
				_ = p.ContextWindow()
			}
		}()
	}
	for i := 0; i < 3; i++ { // writers: mimic /model, /think, web-search toggles
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				p.SetThinkLevel(llm.ThinkLow)
				p.SetModel(DefaultModel)
				p.SetWebSearchMaxUses(j % 4)
			}
		}()
	}
	wg.Wait()
}
