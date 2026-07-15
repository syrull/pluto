package judge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
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
		want    Decision
		risk    string
		wantErr bool
	}{
		{"clean allow", `{"decision":"allow","risk":"low","reason":"ok"}`, DecisionAllow, "low", false},
		{"clean block", `{"decision":"block","risk":"critical","reason":"nope"}`, DecisionBlock, "critical", false},
		{"json in prose", "Sure!\n{\"decision\":\"block\",\"risk\":\"high\",\"reason\":\"x\"}\nHope that helps", DecisionBlock, "high", false},
		{"uppercase decision", `{"decision":"ALLOW","risk":"none","reason":""}`, DecisionAllow, "none", false},
		{"no json", "I think this is fine", "", "", true},
		{"invalid decision", `{"decision":"maybe","risk":"low"}`, "", "", true},
		{"malformed json", `{"decision":`, "", "", true},
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
			if v.Decision != tc.want || v.Risk != tc.risk {
				t.Fatalf("parseVerdict(%q) = %+v, want decision %q risk %q", tc.text, v, tc.want, tc.risk)
			}
		})
	}
}

func TestLLMAssess(t *testing.T) {
	j := NewLLM(fakeProvider{text: `{"decision":"block","risk":"high","reason":"destructive"}`})
	v, err := j.Assess(context.Background(), Request{Command: "rm -rf /tmp/x", Intent: "clean"})
	if err != nil {
		t.Fatalf("Assess() err = %v", err)
	}
	if v.Decision != DecisionBlock || v.Reason != "destructive" {
		t.Fatalf("Assess() = %+v, want block/destructive", v)
	}
}

func TestLLMAssessProviderError(t *testing.T) {
	j := NewLLM(fakeProvider{err: errors.New("boom")})
	if _, err := j.Assess(context.Background(), Request{Command: "ls"}); err == nil {
		t.Fatal("Assess() err = nil, want provider error")
	}
}

func TestLLMAssessRetriesTransient(t *testing.T) {
	p := &flakyProvider{failN: 2, failErr: syscall.ECONNREFUSED, text: `{"decision":"allow","risk":"low","reason":"ok"}`}
	j := NewLLM(p)
	j.backoff = time.Millisecond // keep the test fast

	v, err := j.Assess(context.Background(), Request{Command: "ls"})
	if err != nil {
		t.Fatalf("Assess() err = %v, want nil after retry", err)
	}
	if v.Decision != DecisionAllow {
		t.Fatalf("Assess() = %+v, want allow", v)
	}
	if p.calls != 3 {
		t.Fatalf("provider called %d times, want 3 (two transient failures then success)", p.calls)
	}
}

func TestLLMAssessTransientExhausted(t *testing.T) {
	p := &flakyProvider{failN: 99, failErr: syscall.ECONNRESET}
	j := NewLLM(p)
	j.backoff = time.Millisecond

	if _, err := j.Assess(context.Background(), Request{Command: "ls"}); err == nil {
		t.Fatal("Assess() err = nil, want error after exhausting retries")
	}
	if p.calls != defaultAttempts {
		t.Fatalf("provider called %d times, want %d", p.calls, defaultAttempts)
	}
}

func TestLLMAssessNoRetryPermanent(t *testing.T) {
	p := &flakyProvider{failN: 99, failErr: errors.New("bad response")}
	j := NewLLM(p)
	j.backoff = time.Millisecond

	if _, err := j.Assess(context.Background(), Request{Command: "ls"}); err == nil {
		t.Fatal("Assess() err = nil, want error")
	}
	if p.calls != 1 {
		t.Fatalf("provider called %d times, want 1 (permanent error must not retry)", p.calls)
	}
}

func TestLLMAssessCanceledNoRetry(t *testing.T) {
	p := &flakyProvider{failN: 99, failErr: context.Canceled}
	j := NewLLM(p)
	j.backoff = time.Millisecond

	if _, err := j.Assess(context.Background(), Request{Command: "ls"}); err == nil {
		t.Fatal("Assess() err = nil, want error")
	}
	if p.calls != 1 {
		t.Fatalf("provider called %d times, want 1 (cancellation must not retry)", p.calls)
	}
}

func TestTransient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, true},
		{"conn refused", syscall.ECONNREFUSED, true},
		{"conn reset", syscall.ECONNRESET, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"dns", &net.DNSError{Err: "no such host"}, true},
		{"wrapped dial refused", fmt.Errorf("judge: generate: %w", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}), true},
		{"plain error", errors.New("bad response"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := transient(tc.err); got != tc.want {
				t.Fatalf("transient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestFake(t *testing.T) {
	f := Fake{Verdict: Verdict{Decision: DecisionAllow, Risk: "none"}}
	v, err := f.Assess(context.Background(), Request{})
	if err != nil || v.Decision != DecisionAllow {
		t.Fatalf("Fake.Assess() = %+v, %v", v, err)
	}
}
