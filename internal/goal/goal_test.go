package goal

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/syrull/pluto/internal/llm"
)

type fakeProvider struct {
	text string
	err  error
}

func (fakeProvider) Name() string { return "fake" }
func (f fakeProvider) Generate(context.Context, []llm.Message, []llm.ToolSpec) (llm.Response, error) {
	if f.err != nil {
		return llm.Response{}, f.err
	}
	return llm.Response{Text: f.text}, nil
}

// flakyProvider fails the first failN Generate calls with failErr, then succeeds.
type flakyProvider struct {
	calls   int
	failN   int
	failErr error
	text    string
}

func (*flakyProvider) Name() string { return "flaky" }
func (p *flakyProvider) Generate(context.Context, []llm.Message, []llm.ToolSpec) (llm.Response, error) {
	p.calls++
	if p.calls <= p.failN {
		return llm.Response{}, p.failErr
	}
	return llm.Response{Text: p.text}, nil
}

func TestParseVerdict(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		wantMet bool
		reason  string
		wantErr bool
	}{
		{"met true", `{"met":true,"reason":"tests pass"}`, true, "tests pass", false},
		{"met false", `{"met":false,"reason":"still failing"}`, false, "still failing", false},
		{"json in prose", "Sure!\n{\"met\": true, \"reason\":\"done\"}\nhope that helps", true, "done", false},
		{"no reason", `{"met":false}`, false, "", false},
		{"no json", "I think it's done", false, "", true},
		{"missing met", `{"reason":"unclear"}`, false, "", true},
		{"malformed json", `{"met":`, false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := parseVerdict(tc.text)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseVerdict(%q) err = nil, want error", tc.text)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVerdict(%q) err = %v, want nil", tc.text, err)
			}
			if v.Met != tc.wantMet || v.Reason != tc.reason {
				t.Fatalf("parseVerdict(%q) = %+v, want met=%v reason=%q", tc.text, v, tc.wantMet, tc.reason)
			}
		})
	}
}

func TestLLMEvaluate(t *testing.T) {
	e := NewLLM(fakeProvider{text: `{"met":true,"reason":"all tests pass"}`})
	v, err := e.Evaluate(context.Background(), Request{Condition: "tests pass", Transcript: "PASS"})
	if err != nil {
		t.Fatalf("Evaluate() err = %v", err)
	}
	if !v.Met || v.Reason != "all tests pass" {
		t.Fatalf("Evaluate() = %+v, want met/all tests pass", v)
	}
}

func TestLLMEvaluateProviderError(t *testing.T) {
	e := NewLLM(fakeProvider{err: errors.New("boom")})
	if _, err := e.Evaluate(context.Background(), Request{Condition: "x"}); err == nil {
		t.Fatal("Evaluate() err = nil, want provider error")
	}
}

func TestLLMEvaluateRetriesTransient(t *testing.T) {
	p := &flakyProvider{failN: 2, failErr: syscall.ECONNREFUSED, text: `{"met":false,"reason":"not yet"}`}
	e := NewLLM(p)
	e.backoff = time.Millisecond

	v, err := e.Evaluate(context.Background(), Request{Condition: "x"})
	if err != nil {
		t.Fatalf("Evaluate() err = %v, want nil after retry", err)
	}
	if v.Met {
		t.Fatalf("Evaluate() = %+v, want not met", v)
	}
	if p.calls != 3 {
		t.Fatalf("provider called %d times, want 3 (two transient failures then success)", p.calls)
	}
}

func TestLLMEvaluateTransientExhausted(t *testing.T) {
	p := &flakyProvider{failN: 99, failErr: syscall.ECONNRESET}
	e := NewLLM(p)
	e.backoff = time.Millisecond

	if _, err := e.Evaluate(context.Background(), Request{Condition: "x"}); err == nil {
		t.Fatal("Evaluate() err = nil, want error after exhausting retries")
	}
	if p.calls != defaultAttempts {
		t.Fatalf("provider called %d times, want %d", p.calls, defaultAttempts)
	}
}

func TestLLMEvaluateNoRetryPermanent(t *testing.T) {
	p := &flakyProvider{failN: 99, failErr: errors.New("bad response")}
	e := NewLLM(p)
	e.backoff = time.Millisecond

	if _, err := e.Evaluate(context.Background(), Request{Condition: "x"}); err == nil {
		t.Fatal("Evaluate() err = nil, want error")
	}
	if p.calls != 1 {
		t.Fatalf("provider called %d times, want 1 (permanent error must not retry)", p.calls)
	}
}

func TestLLMEvaluateCanceledNoRetry(t *testing.T) {
	p := &flakyProvider{failN: 99, failErr: context.Canceled}
	e := NewLLM(p)
	e.backoff = time.Millisecond

	if _, err := e.Evaluate(context.Background(), Request{Condition: "x"}); err == nil {
		t.Fatal("Evaluate() err = nil, want error")
	}
	if p.calls != 1 {
		t.Fatalf("provider called %d times, want 1 (cancellation must not retry)", p.calls)
	}
}

// detProvider records a pinned temperature.
type detProvider struct{ temp *float64 }

func (*detProvider) Name() string { return "det" }
func (*detProvider) Generate(context.Context, []llm.Message, []llm.ToolSpec) (llm.Response, error) {
	return llm.Response{}, nil
}
func (p *detProvider) SetTemperature(t float64) { p.temp = &t }

func TestNewLLMPinsDeterministic(t *testing.T) {
	p := &detProvider{}
	_ = NewLLM(p)
	if p.temp == nil {
		t.Fatal("NewLLM did not pin temperature on a Deterministic provider")
	}
	if *p.temp != 0 {
		t.Fatalf("pinned temperature = %v, want 0", *p.temp)
	}
}

func TestFake(t *testing.T) {
	f := Fake{Verdict: Verdict{Met: true, Reason: "done"}}
	v, err := f.Evaluate(context.Background(), Request{})
	if err != nil || !v.Met || v.Reason != "done" {
		t.Fatalf("Fake.Evaluate() = %+v, %v", v, err)
	}
}
