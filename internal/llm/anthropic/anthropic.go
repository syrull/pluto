package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/syrull/pluto/internal/llm"
)

const (
	defaultBaseURL = "https://api.anthropic.com/v1/messages"
	defaultMaxTok  = 8192
	// highEffortMaxTok is the max_tokens floor used at xhigh/max effort so the
	// model has room for deep thinking plus its response. Anthropic suggests a
	// large budget (their docs start at 64k) for these levels.
	highEffortMaxTok = 64000
	// requestTimeout bounds a full non-streaming request (connect + read body).
	// Streaming uses a per-frame idle timeout instead (see streamIdleTimeout).
	requestTimeout = 120 * time.Second
	// webSearchToolVersion is the stable basic web-search server tool.
	webSearchToolVersion = "web_search_20250305"
	// webSearchToolName is the fixed name the API requires for the tool.
	webSearchToolName = "web_search"
	// defaultWebSearchMaxUses bounds searches per request when web search is
	// enabled without an explicit cap.
	defaultWebSearchMaxUses = 5
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

const defaultThinkLevel = llm.ThinkXHigh

// Provider talks to the Anthropic Messages API.
type Provider struct {
	model    string
	creds    credentials
	http     *http.Client
	baseURL  string
	maxTok   int
	thinkLvl llm.ThinkLevel // current effort level; ThinkNone disables thinking
	// webSearchMaxUses, when > 0, enables Anthropic's server-side web_search
	// tool and caps searches per request. Zero disables it.
	webSearchMaxUses int
}

var (
	_ llm.Provider        = (*Provider)(nil)
	_ llm.Switchable      = (*Provider)(nil)
	_ llm.Thinkable       = (*Provider)(nil)
	_ llm.ContextWindower = (*Provider)(nil)
)

// New builds a Provider for the given model, resolving credentials from the environment.
func New(model string) (*Provider, error) {
	creds := resolveCredentials()
	if !creds.ok() {
		return nil, fmt.Errorf("anthropic: no credentials; set ANTHROPIC_OAUTH_TOKEN or ANTHROPIC_API_KEY")
	}
	return &Provider{
		model: model,
		creds: creds,
		// No total Client.Timeout: it would cap streaming at a fixed wall-clock
		// budget regardless of activity. Bounds are applied per call instead
		// (requestTimeout for Generate, streamIdleTimeout watchdog for streams).
		// DefaultTransport still bounds dial/TLS handshake.
		http:             &http.Client{},
		baseURL:          defaultBaseURL,
		maxTok:           defaultMaxTok,
		thinkLvl:         defaultThinkLevel,
		webSearchMaxUses: resolveWebSearchMaxUses(),
	}, nil
}

// resolveWebSearchMaxUses reads ANTHROPIC_WEB_SEARCH to configure the
// server-side web search tool, which is ON by default for Anthropic. Unset (or
// "1"/"true") enables it with the default cap; a positive integer sets an
// explicit cap; "0"/"false"/"off"/"no" opts out.
func resolveWebSearchMaxUses() int {
	v := strings.TrimSpace(os.Getenv("ANTHROPIC_WEB_SEARCH"))
	switch strings.ToLower(v) {
	case "0", "false", "off", "no":
		return 0
	case "", "1", "true", "on", "yes":
		return defaultWebSearchMaxUses
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	return defaultWebSearchMaxUses
}

// Name implements llm.Provider.
func (p *Provider) Name() string { return "anthropic/" + p.model }

// Model returns the current model id.
func (p *Provider) Model() string { return p.model }

// ContextWindow reports the active model's total context window in tokens.
func (p *Provider) ContextWindow() int { return contextWindowFor(p.model) }

// SetWebSearchMaxUses sets the server-side web search cap; 0 disables the tool.
func (p *Provider) SetWebSearchMaxUses(n int) {
	if n < 0 {
		n = 0
	}
	p.webSearchMaxUses = n
}

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

// wireCacheControl marks a content block as a prompt-cache breakpoint. Everything
// from the start of the prompt up to and including a marked block is cached;
// subsequent requests reuse it at ~10% of input price. Type is always "ephemeral".
type wireCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// ephemeral returns a 5-minute cache breakpoint marker.
func ephemeral() *wireCacheControl { return &wireCacheControl{Type: "ephemeral"} }

type wireSysText struct {
	Type         string            `json:"type"` // "text"
	Text         string            `json:"text"`
	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
}

type wireMessage struct {
	Role    string      `json:"role"` // "user" | "assistant"
	Content []wireBlock `json:"content"`
}

// wireImageSource is an image block's source: base64-inlined bytes with a media
// type. (A "url" source is also valid but pluto only sends local bytes.)
type wireImageSource struct {
	Type      string `json:"type"` // "base64"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
}

// wireBlock is a content block; omitempty keeps JSON valid for each variant.
type wireBlock struct {
	Type string `json:"type"` // text | image | tool_use | tool_result

	// text
	Text string `json:"text,omitempty"`

	// image
	Source *wireImageSource `json:"source,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`

	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// text citations (server-side web search); inbound only
	Citations []wireCitation `json:"citations,omitempty"`

	// prompt-cache breakpoint (outbound only)
	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
}

// wireCitation is a web_search_result_location attached to a text block.
type wireCitation struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

// wireTool is a client tool (Name/Description/InputSchema) or a server tool
// (Type/Name plus tool-specific fields like MaxUses). omitempty keeps each
// variant's JSON minimal so the API sees only the fields it expects.
type wireTool struct {
	Type         string            `json:"type,omitempty"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	InputSchema  json.RawMessage   `json:"input_schema,omitempty"`
	MaxUses      int               `json:"max_uses,omitempty"`
	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
}

type wireResponse struct {
	Content    []wireBlock `json:"content"`
	StopReason string      `json:"stop_reason"`
	Usage      *wireUsage  `json:"usage"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// wireUsage is the token accounting the Messages API attaches to a turn.
type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// contextTokens totals the prompt size, folding cache reads/writes into the input count.
func (u wireUsage) contextTokens() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// buildRequest assembles the wire request, routing extended-thinking per the model regime.
func (p *Provider) buildRequest(transcript []llm.Message, tools []llm.ToolSpec, stream bool) wireRequest {
	req := wireRequest{
		Model:     p.model,
		MaxTokens: p.maxTok,
		System:    p.buildSystem(transcript),
		Messages:  buildMessages(transcript, visionFor(p.model)),
		Tools:     buildTools(tools),
		Stream:    stream,
	}
	if p.webSearchMaxUses > 0 {
		req.Tools = append(req.Tools, wireTool{
			Type:    webSearchToolVersion,
			Name:    webSearchToolName,
			MaxUses: p.webSearchMaxUses,
		})
	}
	switch regimeFor(p.model) {
	case regimeAdaptive:
		p.applyAdaptiveThinking(&req)
	case regimeLegacy:
		p.applyLegacyThinking(&req)
	case regimeNone:
		// no thinking control available
	}
	applyCacheBreakpoints(&req)
	return req
}

// applyCacheBreakpoints marks prompt-cache breakpoints so repeated requests in a
// session reuse the stable prefix instead of re-billing it every turn. Anthropic
// caching is opt-in: without these markers nothing is cached. Placement (max 4
// breakpoints, render order tools→system→messages):
//   - the last system block, which caches the whole tools+system prefix (the big
//     static chunk: tool schemas, base prompt, project context);
//   - the last block of each of the final two messages, a rolling window so the
//     growing conversation is cached turn-over-turn and the previous turn's write
//     stays within the 20-block lookback.
func applyCacheBreakpoints(req *wireRequest) {
	if n := len(req.System); n > 0 {
		req.System[n-1].CacheControl = ephemeral()
	} else if n := len(req.Tools); n > 0 {
		// No system blocks: cache the tool prefix directly.
		req.Tools[n-1].CacheControl = ephemeral()
	}
	msgs := req.Messages
	for i := len(msgs) - 1; i >= 0 && i >= len(msgs)-2; i-- {
		if c := msgs[i].Content; len(c) > 0 {
			c[len(c)-1].CacheControl = ephemeral()
		}
	}
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
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
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

// buildMessages translates the transcript into alternating user/assistant
// messages. vision reports whether the active model accepts image blocks; when
// false, attachments are dropped so a non-vision model isn't sent a 400.
func buildMessages(transcript []llm.Message, vision bool) []wireMessage {
	var out []wireMessage
	for _, m := range transcript {
		switch m.Role {
		case llm.RoleSystem:
			// handled in buildSystem
		case llm.RoleUser:
			// Coalesce into a preceding user turn (a tool_result carrier, or an
			// earlier user turn) so a steering message folded in after tool
			// results doesn't produce two consecutive user turns, which the API
			// rejects. Text after tool_result blocks is valid.
			blocks := userBlocks(m, vision)
			if n := len(out); n > 0 && out[n-1].Role == "user" {
				out[n-1].Content = append(out[n-1].Content, blocks...)
			} else {
				out = append(out, wireMessage{Role: "user", Content: blocks})
			}
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
			block := wireBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: jsonString(m.Content)}
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

// userBlocks renders a user turn as image blocks (when the model supports
// vision) followed by a text block. The text block is kept whenever there is
// text, or when no image block was emitted, so a text-only turn always carries
// its (possibly empty) text just as before.
func userBlocks(m llm.Message, vision bool) []wireBlock {
	var blocks []wireBlock
	if vision {
		for _, att := range m.Attachments {
			if b, ok := imageBlock(att); ok {
				blocks = append(blocks, b)
			}
		}
	}
	if m.Content != "" || len(blocks) == 0 {
		blocks = append(blocks, wireBlock{Type: "text", Text: m.Content})
	}
	return blocks
}

// imageBlock builds a base64 image block from an attachment, reporting false for
// a non-image or malformed attachment so it is skipped rather than sent invalid.
func imageBlock(att llm.Attachment) (wireBlock, bool) {
	if att.Kind != "" && att.Kind != llm.AttachmentImage {
		return wireBlock{}, false
	}
	if att.MediaType == "" || len(att.Data) == 0 {
		return wireBlock{}, false
	}
	return wireBlock{
		Type: "image",
		Source: &wireImageSource{
			Type:      "base64",
			MediaType: att.MediaType,
			Data:      base64.StdEncoding.EncodeToString(att.Data),
		},
	}, true
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
	var sources sourceSet
	for _, b := range wire.Content {
		switch b.Type {
		case "text":
			out.Text += b.Text
			sources.addAll(b.Citations)
		case "thinking":
			out.Thinking += b.Thinking
			if b.Signature != "" {
				out.ThinkingSig = b.Signature
			}
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
				ID: b.ID, Name: b.Name, Args: json.RawMessage(b.Input),
			})
			// server_tool_use and web_search_tool_result are executed by the
			// API within this turn; they carry no client-actionable call.
		}
	}
	out.Text += sources.footer()
	if wire.Usage != nil {
		out.Usage = llm.Usage{InputTokens: wire.Usage.contextTokens(), OutputTokens: wire.Usage.OutputTokens}
	}
	return out
}

func rawOrNull(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("null")
	}
	return r
}

// jsonString encodes s as a JSON string value for a tool_result content field.
func jsonString(s string) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return b
}

// sourceSet collects unique web-search citation sources in first-seen order.
type sourceSet struct {
	seen  map[string]struct{}
	order []wireCitation
}

func (s *sourceSet) addAll(cites []wireCitation) {
	for _, c := range cites {
		if c.URL == "" {
			continue
		}
		if s.seen == nil {
			s.seen = make(map[string]struct{})
		}
		if _, ok := s.seen[c.URL]; ok {
			continue
		}
		s.seen[c.URL] = struct{}{}
		s.order = append(s.order, c)
	}
}

// footer renders a trailing sources block, or empty when no citations were seen.
func (s *sourceSet) footer() string {
	if len(s.order) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nSources:\n")
	for _, c := range s.order {
		title := c.Title
		if title == "" {
			title = c.URL
		}
		b.WriteString("- ")
		b.WriteString(title)
		b.WriteString(" (")
		b.WriteString(c.URL)
		b.WriteString(")\n")
	}
	return b.String()
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
