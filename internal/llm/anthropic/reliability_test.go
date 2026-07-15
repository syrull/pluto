package anthropic

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/syrull/pluto/internal/llm"
)

func TestSetTemperaturePinsWhenThinkingOff(t *testing.T) {
	// No-thinking model: a pinned temperature rides on the request.
	p := &Provider{model: "claude-3-5-haiku-latest", maxTok: defaultMaxTok, thinkLvl: llm.ThinkNone}
	if req := p.buildRequest(nil, nil, false); req.Temperature != nil {
		t.Fatalf("Temperature = %v, want nil before SetTemperature", *req.Temperature)
	}
	p.SetTemperature(0)
	req := p.buildRequest(nil, nil, false)
	if req.Temperature == nil {
		t.Fatal("Temperature = nil, want pinned 0")
	}
	if *req.Temperature != 0 {
		t.Fatalf("Temperature = %v, want 0", *req.Temperature)
	}
}

func TestSetTemperatureDroppedWhenThinkingEnabled(t *testing.T) {
	// Anthropic rejects a non-default temperature alongside extended thinking,
	// so buildRequest must drop the pin when thinking is enabled.
	p := &Provider{model: "claude-sonnet-4-5", maxTok: defaultMaxTok, thinkLvl: llm.ThinkMax}
	p.SetTemperature(0.3)
	req := p.buildRequest(nil, nil, false)
	if req.Thinking == nil || req.Thinking.Type != "enabled" {
		t.Fatalf("precondition: Thinking = %+v, want enabled", req.Thinking)
	}
	if req.Temperature != nil {
		t.Fatalf("Temperature = %v, want nil (dropped) with thinking enabled", *req.Temperature)
	}
}

func TestProviderImplementsDeterministic(t *testing.T) {
	var _ llm.Deterministic = (*Provider)(nil)
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		name string
		val  string
		want time.Duration
	}{
		{"absent", "", 0},
		{"seconds", "3", 3 * time.Second},
		{"zero", "0", 0},
		{"negative", "-5", 0},
		{"garbage", "soon", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.val != "" {
				h.Set("Retry-After", tc.val)
			}
			if got := parseRetryAfter(h); got != tc.want {
				t.Fatalf("parseRetryAfter(%q) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}

	// HTTP-date in the future yields a positive delay.
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	if got := parseRetryAfter(h); got <= 0 {
		t.Fatalf("parseRetryAfter(future date) = %v, want > 0", got)
	}
}

func TestAPIErrorRetryableAndUnwrap(t *testing.T) {
	retryable := []int{408, 425, 429, 500, 502, 503, 504, 529}
	for _, code := range retryable {
		if !(&llm.APIError{StatusCode: code}).Retryable() {
			t.Fatalf("APIError{%d}.Retryable() = false, want true", code)
		}
	}
	notRetryable := []int{400, 401, 403, 404, 422}
	for _, code := range notRetryable {
		if (&llm.APIError{StatusCode: code}).Retryable() {
			t.Fatalf("APIError{%d}.Retryable() = true, want false", code)
		}
	}

	// Wrapped by the provider prefix, errors.As must still recover the type.
	wrapped := fmt.Errorf("anthropic: %w", &llm.APIError{StatusCode: 529, Body: "Overloaded"})
	var apiErr *llm.APIError
	if !errors.As(wrapped, &apiErr) || apiErr.StatusCode != 529 {
		t.Fatalf("errors.As failed to recover APIError from %v", wrapped)
	}
}
