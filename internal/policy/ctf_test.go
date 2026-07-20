package policy

import (
	"context"
	"net/netip"
	"testing"

	"github.com/syrull/pluto/internal/judge"
)

func TestCTFRoEFastPathsAuthorizedActions(t *testing.T) {
	called := false
	j := judgeFunc(func() (judge.Verdict, error) { called = true; return judge.Verdict{Decision: judge.DecisionAllow}, nil })
	g := newGate(t, j, func(c *Config) { c.CTF = true })

	for _, cmd := range []string{
		"nmap -sVC -p- 10.0.0.5",
		"hydra -L users.txt -P pass.txt ssh://10.0.0.5",
		"kubectl get pods -A",
		"gobuster dir -u http://target -w list.txt",
	} {
		rr := g.Review(context.Background(), bashCall(cmd))
		if !rr.Allowed || rr.Source != "ctf-roe" {
			t.Fatalf("Review(%q) = %+v, want allowed ctf-roe", cmd, rr)
		}
	}
	if called {
		t.Fatal("judge must not run for authorized in-scope CTF actions")
	}
}

func TestCTFRoEOffKeepsJudge(t *testing.T) {
	called := false
	j := judgeFunc(func() (judge.Verdict, error) { called = true; return judge.Verdict{Decision: judge.DecisionAllow}, nil })
	g := newGate(t, j, nil) // CTF off

	rr := g.Review(context.Background(), bashCall("nmap -sV 10.0.0.5"))
	if rr.Source != "judge" {
		t.Fatalf("with CTF off, nmap should reach the judge, got %+v", rr)
	}
	if !called {
		t.Fatal("judge should run when CTF mode is off")
	}
}

func TestCTFRoEEscalatesUnrecognizedActions(t *testing.T) {
	called := false
	j := judgeFunc(func() (judge.Verdict, error) { called = true; return judge.Verdict{Decision: judge.DecisionAllow}, nil })
	g := newGate(t, j, func(c *Config) { c.CTF = true })

	// A non-offensive command is not fast-pathed even in CTF mode.
	rr := g.Review(context.Background(), bashCall("rm -rf ./build && make deploy"))
	if rr.Source == "ctf-roe" {
		t.Fatalf("unrecognized action should not fast-path, got %+v", rr)
	}
	if !called {
		t.Fatal("unrecognized action should still reach the judge")
	}
}

func TestCTFRoEStillGuardsCatastrophic(t *testing.T) {
	j := judgeFunc(func() (judge.Verdict, error) { return judge.Verdict{Decision: judge.DecisionAllow}, nil })
	g := newGate(t, j, func(c *Config) { c.CTF = true })

	rr := g.Review(context.Background(), bashCall("rm -rf /"))
	if rr.Allowed || rr.Source != "guard" {
		t.Fatalf("guard must still block rm -rf / in CTF mode, got %+v", rr)
	}
}

func TestCTFRoEScopeEscalatesOutOfScope(t *testing.T) {
	called := false
	j := judgeFunc(func() (judge.Verdict, error) {
		called = true
		return judge.Verdict{Decision: judge.DecisionBlock, Reason: "oos"}, nil
	})
	g := newGate(t, j, func(c *Config) { c.CTF = true })
	g.engagement = Engagement{Scope: mustPrefixes(t, "10.0.0.0/24")}

	// In-scope host fast-paths.
	in := g.Review(context.Background(), bashCall("nmap -sV 10.0.0.42"))
	if in.Source != "ctf-roe" {
		t.Fatalf("in-scope host should fast-path, got %+v", in)
	}
	// Out-of-scope host escalates to the judge.
	out := g.Review(context.Background(), bashCall("nmap -sV 8.8.8.8"))
	if out.Source != "judge" {
		t.Fatalf("out-of-scope host should escalate to judge, got %+v", out)
	}
	if !called {
		t.Fatal("judge should run for the out-of-scope action")
	}
}

func TestSetCTFModeTogglesGate(t *testing.T) {
	g := newGate(t, judge.Fake{}, nil)
	if g.CTFMode() {
		t.Fatal("CTF mode should start off")
	}
	g.SetCTFMode(true)
	if !g.CTFMode() {
		t.Fatal("SetCTFMode(true) should enable CTF mode")
	}
	g.SetCTFMode(false)
	if g.CTFMode() {
		t.Fatal("SetCTFMode(false) should disable CTF mode")
	}
}

func TestLoadEngagementParsesScope(t *testing.T) {
	t.Setenv("PLUTO_CTF_SCOPE", "10.0.0.0/24, 192.168.1.5 , garbage")
	e := LoadEngagement()
	if len(e.Scope) != 2 {
		t.Fatalf("expected 2 valid prefixes, got %d (%v)", len(e.Scope), e.Scope)
	}
}

func mustPrefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}
