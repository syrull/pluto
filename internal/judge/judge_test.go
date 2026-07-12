package judge

import (
	"context"
	"errors"
	"testing"

	"github.com/pluto/harness/internal/llm"
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

func TestFake(t *testing.T) {
	f := Fake{Verdict: Verdict{Decision: DecisionAllow, Risk: "none"}}
	v, err := f.Assess(context.Background(), Request{})
	if err != nil || v.Decision != DecisionAllow {
		t.Fatalf("Fake.Assess() = %+v, %v", v, err)
	}
}
