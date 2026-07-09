package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/pluto/harness/internal/llm"
)

const (
	defaultBaseURL = "https://api.anthropic.com/v1/messages"
	defaultMaxTok  = 8192
	// highEffortMaxTok is the max_tokens floor used at xhigh/max effort so the
	// model has room for deep thinking plus its response. Anthropic suggests a
	// large budget (their docs start at 64k) for these levels.
	highEffortMaxTok = 64000
)

// legacyBudgets maps effort levels to budget_tokens for regimeLegacy models; API requires budget_tokens < max_tokens.
var legacyBudgets = map[llm.ThinkLevel]int{
	llm.ThinkLow:    2048,
	llm.ThinkMedium: 4096,
	llm.ThinkHigh:   12288,
	llm.ThinkXHigh:  24576,
	llm.ThinkMax:    24576,
}

// effortOrder lists thinking levels in ascending effort order.
var effortOrder = []llm.ThinkLevel{
	llm.ThinkLow, llm.ThinkMedium, llm.ThinkHigh, llm.ThinkXHigh, llm.ThinkMax,
}

const defaultThinkLevel = llm.ThinkHigh

// Provider talks to the Anthropic Messages API.
type Provider struct {
	model    string
	creds    credentials
	http     *http.Client
	baseURL  string
	maxTok   int
	thinkLvl llm.ThinkLevel // current effort level; ThinkNone disables thinking
}

var (
	_ llm.Provider   = (*Provider)(nil)
	_ llm.Switchable = (*Provider)(nil)
	_ llm.Thinkable  = (*Provider)(nil)
)

// New builds a Provider for the given model, resolving credentials from the environment.
func New(model string) (*Provider, error) {
	creds := resolveCredentials()
	if !creds.ok() {
		return nil, fmt.Errorf("anthropic: no credentials; set ANTHROPIC_OAUTH_TOKEN or ANTHROPIC_API_KEY")
	}
	return &Provider{
		model:    model,
		creds:    creds,
		http:     &http.Client{Timeout: 120 * time.Second},
		baseURL:  defaultBaseURL,
		maxTok:   defaultMaxTok,
		thinkLvl: defaultThinkLevel,
	}, nil
}

// Name implements llm.Provider.
func (p *Provider) Name() string { return "anthropic/" + p.model }

// Model returns the current model id.
func (p *Provider) Model() string { return p.model }

// SetModel switches the active model, re-clamping the effort level as needed.
func (p *Provider) SetModel(model string) {
	p.model = model
	p.SetThinkLevel(p.thinkLvl)
}

// ThinkLevel reports the current extended-thinking effort level.
func (p *Provider) ThinkLevel() llm.ThinkLevel {
	if p.thinkLvl == "" {
		return llm.ThinkNone
	}
	return p.thinkLvl
}

// SetThinkLevel sets the effort level, clamped to what the active model supports.
func (p *Provider) SetThinkLevel(level llm.ThinkLevel) {
	supported := p.ThinkLevels()
	// supported always leads with ThinkNone. A model with no thinking support
	// has only [ThinkNone], so any request collapses to none.
	if level == llm.ThinkNone || level == "" || len(supported) == 1 {
		p.thinkLvl = llm.ThinkNone
		return
	}
	if slices.Contains(supported, level) {
		p.thinkLvl = level
		return
	}
	// Unsupported non-none level: clamp to the highest supported (last entry).
	p.thinkLvl = supported[len(supported)-1]
}

// ThinkLevels lists the levels the active model supports.
func (p *Provider) ThinkLevels() []llm.ThinkLevel {
	levels := []llm.ThinkLevel{llm.ThinkNone}
	if regimeFor(p.model) == regimeNone {
		return levels
	}
	xhigh := modelsWithXHigh[p.model]
	for _, l := range effortOrder {
		if l == llm.ThinkXHigh && !xhigh {
			continue
		}
		levels = append(levels, l)
	}
	return levels
}

// Reauth re-resolves credentials from the environment and auth store.
func (p *Provider) Reauth() error {
	creds := resolveCredentials()
	if !creds.ok() {
		return fmt.Errorf("anthropic: no credentials after reauth")
	}
	p.creds = creds
	return nil
}

// --- wire types (subset of the Messages API used here) ---

type wireRequest struct {
	Model        string            `json:"model"`
	MaxTokens    int               `json:"max_tokens"`
	System       []wireSysText     `json:"system,omitempty"`
	Messages     []wireMessage     `json:"messages"`
	Tools        []wireTool        `json:"tools,omitempty"`
	Stream       bool              `json:"stream,omitempty"`
	Thinking     *wireThinking     `json:"thinking,omitempty"`
	OutputConfig *wireOutputConfig `json:"output_config,omitempty"`
}

// wireThinking configures extended thinking per the model regime (legacy vs adaptive).
type wireThinking struct {
	Type         string `json:"type"` // "enabled" | "adaptive" | "disabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	// Display controls whether adaptive thinking text is returned. Newest
	// models default to "omitted" (signed block, empty thinking); "summarized"
	// makes the reasoning text visible. Only meaningful for adaptive thinking.
	Display string `json:"display,omitempty"`
}

// wireOutputConfig carries the effort level (not inside thinking).
type wireOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type wireSysText struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

type wireMessage struct {
	Role    string      `json:"role"` // "user" | "assistant"
	Content []wireBlock `json:"content"`
}

// wireBlock is a content block; omitempty keeps JSON valid for each variant.
type wireBlock struct {
	Type string `json:"type"` // text | tool_use | tool_result

	// text
	Text string `json:"text,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type wireResponse struct {
	Content    []wireBlock `json:"content"`
	StopReason string      `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// buildRequest assembles the wire request, routing extended-thinking per the model regime.
func (p *Provider) buildRequest(transcript []llm.Message, tools []llm.ToolSpec, stream bool) wireRequest {
	req := wireRequest{
		Model:     p.model,
		MaxTokens: p.maxTok,
		System:    p.buildSystem(transcript),
		Messages:  buildMessages(transcript),
		Tools:     buildTools(tools),
		Stream:    stream,
	}
	switch regimeFor(p.model) {
	case regimeAdaptive:
		p.applyAdaptiveThinking(&req)
	case regimeLegacy:
		p.applyLegacyThinking(&req)
	case regimeNone:
		// no thinking control available
	}
	return req
}

// applyAdaptiveThinking sets the adaptive thinking block and effort level.
func (p *Provider) applyAdaptiveThinking(req *wireRequest) {
	if !p.thinkLvl.Thinking() {
		// Disabling: only models where thinking is on by default accept (and
		// need) type:"disabled". On default-off adaptive models, disabled is
		// rejected, so omit the thinking field entirely.
		if modelAdaptiveDefaultOn[p.model] {
			req.Thinking = &wireThinking{Type: "disabled"}
		}
		return
	}
	req.Thinking = &wireThinking{Type: "adaptive", Display: "summarized"}
	req.OutputConfig = &wireOutputConfig{Effort: string(p.thinkLvl)}
	// At xhigh/max the model needs ample room for thinking + response; the
	// small default answer budget would truncate. Anthropic recommends a large
	// max_tokens at these levels.
	if p.thinkLvl == llm.ThinkXHigh || p.thinkLvl == llm.ThinkMax {
		if req.MaxTokens < highEffortMaxTok {
			req.MaxTokens = highEffortMaxTok
		}
	}
}

// applyLegacyThinking sets the enabled+budget_tokens block and grows max_tokens.
func (p *Provider) applyLegacyThinking(req *wireRequest) {
	budget, ok := legacyBudgets[p.thinkLvl]
	if !ok {
		return // none level: no thinking
	}
	req.Thinking = &wireThinking{Type: "enabled", BudgetTokens: budget}
	if req.MaxTokens <= budget {
		req.MaxTokens = budget + defaultMaxTok
	}
}

// send POSTs the request and returns the raw HTTP response.
func (p *Provider) send(ctx context.Context, req wireRequest) (*http.Response, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	p.creds.apply(httpReq.Header)
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: request failed: %w", err)
	}
	return resp, nil
}

// Generate implements llm.Provider (non-streaming).
func (p *Provider) Generate(ctx context.Context, transcript []llm.Message, tools []llm.ToolSpec) (llm.Response, error) {
	resp, err := p.send(ctx, p.buildRequest(transcript, tools, false))
	if err != nil {
		return llm.Response{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.Response{}, fmt.Errorf("anthropic: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return llm.Response{}, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, truncate(body, 500))
	}

	var wire wireResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return llm.Response{}, fmt.Errorf("anthropic: decode response: %w", err)
	}
	if wire.Error != nil {
		return llm.Response{}, fmt.Errorf("anthropic: api error: %s: %s", wire.Error.Type, wire.Error.Message)
	}
	return mapResponse(wire), nil
}

// buildSystem assembles the system blocks; OAuth requires the Claude Code identity first.
func (p *Provider) buildSystem(transcript []llm.Message) []wireSysText {
	var blocks []wireSysText
	if p.creds.requiresClaudeCodeIdentity() {
		blocks = append(blocks, wireSysText{Type: "text", Text: claudeCodeIdentity})
	}
	for _, m := range transcript {
		if m.Role == llm.RoleSystem && m.Content != "" {
			blocks = append(blocks, wireSysText{Type: "text", Text: m.Content})
		}
	}
	return blocks
}

// buildMessages translates the transcript into alternating user/assistant messages.
func buildMessages(transcript []llm.Message) []wireMessage {
	var out []wireMessage
	for _, m := range transcript {
		switch m.Role {
		case llm.RoleSystem:
			// handled in buildSystem
		case llm.RoleUser:
			out = append(out, wireMessage{
				Role:    "user",
				Content: []wireBlock{{Type: "text", Text: m.Content}},
			})
		case llm.RoleModel:
			blocks := make([]wireBlock, 0, 2+len(m.ToolCalls))
			// Thinking block MUST lead the assistant turn when present (the API
			// rejects a thinking+tool turn that does not start with thinking).
			if m.Thinking != "" {
				blocks = append(blocks, wireBlock{
					Type: "thinking", Thinking: m.Thinking, Signature: m.ThinkingSig,
				})
			}
			if m.Content != "" {
				blocks = append(blocks, wireBlock{Type: "text", Text: m.Content})
			}
			for _, c := range m.ToolCalls {
				blocks = append(blocks, wireBlock{
					Type: "tool_use", ID: c.ID, Name: c.Name, Input: rawOrNull(c.Args),
				})
			}
			out = append(out, wireMessage{Role: "assistant", Content: blocks})
		case llm.RoleTool:
			block := wireBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}
			// Coalesce into the preceding user message if it is already a
			// tool_result carrier; otherwise open a new user message.
			if n := len(out); n > 0 && out[n-1].Role == "user" && isToolResultCarrier(out[n-1]) {
				out[n-1].Content = append(out[n-1].Content, block)
			} else {
				out = append(out, wireMessage{Role: "user", Content: []wireBlock{block}})
			}
		}
	}
	return out
}

// isToolResultCarrier reports whether a user message contains only tool_result blocks.
func isToolResultCarrier(m wireMessage) bool {
	for _, b := range m.Content {
		if b.Type != "tool_result" {
			return false
		}
	}
	return len(m.Content) > 0
}

func buildTools(tools []llm.ToolSpec) []wireTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]wireTool, len(tools))
	for i, t := range tools {
		out[i] = wireTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: rawOrObject(t.Schema),
		}
	}
	return out
}

// mapResponse turns the assistant content blocks into an llm.Response.
func mapResponse(wire wireResponse) llm.Response {
	var out llm.Response
	for _, b := range wire.Content {
		switch b.Type {
		case "text":
			out.Text += b.Text
		case "thinking":
			out.Thinking += b.Thinking
			if b.Signature != "" {
				out.ThinkingSig = b.Signature
			}
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
				ID: b.ID, Name: b.Name, Args: json.RawMessage(b.Input),
			})
		}
	}
	return out
}

func rawOrNull(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("null")
	}
	return r
}

func rawOrObject(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage(`{"type":"object"}`)
	}
	return r
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
