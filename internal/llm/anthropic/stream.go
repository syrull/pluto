package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pluto/harness/internal/llm"
)

var _ llm.StreamingProvider = (*Provider)(nil)

// streamIdleTimeout bounds how long GenerateStream waits between SSE frames
// before treating the connection as stalled. It resets on every frame, so a
// slow but steady stream is never aborted.
const streamIdleTimeout = 120 * time.Second

// sseEvent is an SSE event/delta payload.
type sseEvent struct {
	Type         string      `json:"type"`
	Index        int         `json:"index"`
	ContentBlock *sseBlock   `json:"content_block"`
	Delta        *sseDelta   `json:"delta"`
	Error        *sseError   `json:"error"`
	Message      *sseMessage `json:"message"` // message_start carries initial usage
	Usage        *wireUsage  `json:"usage"`   // message_delta carries final output usage
}

// sseMessage is the message envelope on a message_start frame.
type sseMessage struct {
	Usage *wireUsage `json:"usage"`
}

type sseBlock struct {
	Type string `json:"type"` // text | thinking | tool_use
	ID   string `json:"id"`
	Name string `json:"name"`
}

type sseDelta struct {
	Type        string `json:"type"` // text_delta | thinking_delta | input_json_delta | signature_delta
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	PartialJSON string `json:"partial_json"`
	StopReason  string `json:"stop_reason"`
	Signature   string `json:"signature"`
}

type sseError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// blockAccumulator tracks an in-flight content block across its deltas.
type blockAccumulator struct {
	kind     string          // text | thinking | tool_use
	toolID   string          // tool_use id
	toolName string          // tool_use name
	sig      string          // thinking signature
	text     strings.Builder // text/thinking content
	json     strings.Builder // tool_use input_json fragments
}

// GenerateStream implements llm.StreamingProvider.
func (p *Provider) GenerateStream(ctx context.Context, transcript []llm.Message, tools []llm.ToolSpec, onDelta func(llm.StreamDelta)) (llm.Response, error) {
	// Derive a cancelable context so the idle watchdog can abort a stalled
	// read: scanner.Scan() on resp.Body has no read deadline, so canceling the
	// request context is what unblocks it.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	resp, err := p.send(ctx, p.buildRequest(transcript, tools, true))
	if err != nil {
		return llm.Response{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return llm.Response{}, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, truncate(body, 500))
	}

	return parseSSE(resp.Body, streamIdleTimeout, cancel, onDelta)
}

// parseSSE consumes an Anthropic SSE stream and assembles the final Response.
func parseSSE(r io.Reader, idle time.Duration, cancel context.CancelFunc, onDelta func(llm.StreamDelta)) (llm.Response, error) {
	scanner := bufio.NewScanner(r)
	// Allow large SSE lines (tool args / thinking can be long).
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	// Idle watchdog: if no SSE frame arrives within idle, cancel the request
	// context to unblock the blocked scanner.Scan(). The deadline resets on
	// every frame (see Reset in the loop), so a steady stream never trips it.
	var timedOut atomic.Bool
	watchdog := time.AfterFunc(idle, func() {
		timedOut.Store(true)
		cancel()
	})
	defer watchdog.Stop()

	blocks := map[int]*blockAccumulator{}
	var out llm.Response

	for scanner.Scan() {
		// Any frame — delta, ping, or keep-alive — proves the stream is live.
		watchdog.Reset(idle)
		line := scanner.Text()
		// SSE frames are "event: <type>" then "data: <json>"; we only need data.
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "" {
			continue
		}

		var ev sseEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue // tolerate keep-alives / unknown frames
		}

		switch ev.Type {
		case "message_start":
			if ev.Message != nil && ev.Message.Usage != nil {
				out.Usage.InputTokens = ev.Message.Usage.contextTokens()
				out.Usage.OutputTokens = ev.Message.Usage.OutputTokens
			}

		case "content_block_start":
			acc := &blockAccumulator{}
			if ev.ContentBlock != nil {
				acc.kind = ev.ContentBlock.Type
				acc.toolID = ev.ContentBlock.ID
				acc.toolName = ev.ContentBlock.Name
			}
			blocks[ev.Index] = acc

		case "content_block_delta":
			acc := blocks[ev.Index]
			if acc == nil || ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				acc.text.WriteString(ev.Delta.Text)
				onDelta(llm.StreamDelta{Kind: llm.DeltaText, Text: ev.Delta.Text})
			case "thinking_delta":
				acc.text.WriteString(ev.Delta.Thinking)
				onDelta(llm.StreamDelta{Kind: llm.DeltaThinking, Text: ev.Delta.Thinking})
			case "input_json_delta":
				acc.json.WriteString(ev.Delta.PartialJSON)
			case "signature_delta":
				acc.sig += ev.Delta.Signature
			}

		case "content_block_stop":
			acc := blocks[ev.Index]
			if acc == nil {
				continue
			}
			switch acc.kind {
			case "text":
				out.Text += acc.text.String()
			case "thinking":
				out.Thinking += acc.text.String()
				if acc.sig != "" {
					out.ThinkingSig = acc.sig
				}
			case "tool_use":
				args := acc.json.String()
				if args == "" {
					args = "{}"
				}
				out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
					ID: acc.toolID, Name: acc.toolName, Args: json.RawMessage(args),
				})
			}

		case "message_delta":
			if ev.Usage != nil {
				out.Usage.OutputTokens = ev.Usage.OutputTokens
			}

		case "error":
			if ev.Error != nil {
				return out, fmt.Errorf("anthropic: stream error: %s: %s", ev.Error.Type, ev.Error.Message)
			}

		case "message_stop":
			// terminal frame
		}
	}
	// A watchdog cancellation surfaces as a context error on scanner.Err();
	// report it as the idle timeout it actually is.
	err := scanner.Err()
	if timedOut.Load() {
		return out, fmt.Errorf("anthropic: stream stalled: no data for %s", idle)
	}
	if err != nil {
		return out, fmt.Errorf("anthropic: read stream: %w", err)
	}
	return out, nil
}
