package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/syrull/pluto/internal/auth"
)

func main() {
	tok, _ := auth.Load()
	body := map[string]any{
		"model":      "claude-sonnet-5",
		"max_tokens": 8192,
		"system": []map[string]string{
			{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
		},
		"messages": []map[string]any{
			{"role": "user", "content": []map[string]string{{"type": "text", "text": "What is 17*23? Think it through step by step, showing your work carefully."}}},
		},
		"thinking":      map[string]any{"type": "adaptive"},
		"output_config": map[string]string{"effort": "high"},
		"stream":        true,
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(raw))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	fmt.Println(string(b))
}
