// Command probe sends a minimal request to the Anthropic Messages API using
// pluto's stored OAuth credentials and reports whether connectivity and
// authentication succeed. It is a diagnostic aid, not part of the CLI.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/syrull/pluto/internal/auth"
)

const (
	apiURL = "https://api.anthropic.com/v1/messages"
	// probeModel is the cheapest catalog model, keeping the probe request low-cost.
	probeModel = "claude-haiku-4-5"
	// claudeIdentity is the first system block the OAuth token is scoped to.
	claudeIdentity = "You are Claude Code, Anthropic's official CLI for Claude."
	requestTimeout = 30 * time.Second
)

// httpClient performs probe requests; a variable so tests can stub the transport.
var httpClient = http.DefaultClient

// loadToken resolves stored credentials; a variable so tests can stub it.
var loadToken = auth.Load

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "probe: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("probe: ok")
}

func run(ctx context.Context) error {
	tok, ok := loadToken()
	if !ok {
		return fmt.Errorf("no stored credentials; authenticate with pluto first")
	}

	payload, err := json.Marshal(map[string]any{
		"model":      probeModel,
		"max_tokens": 16,
		"system": []map[string]any{
			{"type": "text", "text": claudeIdentity},
		},
		"messages": []map[string]any{
			{"role": "user", "content": "ping"},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("authorization", "Bearer "+tok.AccessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("api returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
