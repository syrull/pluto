package tui

import (
	"strings"
	"testing"
)

func TestParseIssues(t *testing.T) {
	data := []byte(`[
		{"number":24,"title":"Show branch","state":"OPEN","url":"https://x/24","body":"do it","author":{"login":"syrull"},"labels":[{"name":"bug"},{"name":"ui"}]}
	]`)
	issues, err := parseIssues(data)
	if err != nil {
		t.Fatalf("parseIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("want 1 issue, got %d", len(issues))
	}
	is := issues[0]
	if is.Number != 24 || is.Title != "Show branch" || is.Author != "syrull" {
		t.Fatalf("unexpected issue: %+v", is)
	}
	if strings.Join(is.Labels, ",") != "bug,ui" {
		t.Fatalf("labels = %v", is.Labels)
	}
}

func TestParsePRsLinksClosingIssues(t *testing.T) {
	data := []byte(`[
		{"number":12,"title":"Fix branch","state":"OPEN","url":"https://x/12","body":"b","author":{"login":"me"},"headRefName":"fix-24","isDraft":true,"closingIssuesReferences":[{"number":24}]}
	]`)
	prs, links, err := parsePRs(data)
	if err != nil {
		t.Fatalf("parsePRs: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 12 || prs[0].Branch != "fix-24" || !prs[0].Draft {
		t.Fatalf("unexpected pr: %+v", prs)
	}
	if links[24] != 12 {
		t.Fatalf("expected issue 24 linked to PR 12, got %v", links)
	}
}

func TestFetchGitHubCmdLinksIssues(t *testing.T) {
	orig := ghRunRaw
	defer func() { ghRunRaw = orig }()
	ghRunRaw = func(args ...string) ([]byte, string, error) {
		switch args[0] {
		case "issue":
			return []byte(`[{"number":24,"title":"t","state":"OPEN","url":"u","author":{"login":"a"}}]`), "", nil
		case "pr":
			return []byte(`[{"number":12,"title":"p","state":"OPEN","url":"u","closingIssuesReferences":[{"number":24}]}]`), "", nil
		}
		return []byte("[]"), "", nil
	}
	msg, ok := fetchGitHubCmd().(ghDataMsg)
	if !ok {
		t.Fatal("fetchGitHubCmd did not return ghDataMsg")
	}
	if msg.err != nil {
		t.Fatalf("unexpected err: %v", msg.err)
	}
	if len(msg.issues) != 1 || msg.issues[0].LinkedPR != 12 {
		t.Fatalf("issue not linked to PR: %+v", msg.issues)
	}
}

func TestFetchGitHubCmdSurfacesError(t *testing.T) {
	orig := ghRunRaw
	defer func() { ghRunRaw = orig }()
	ghRunRaw = func(args ...string) ([]byte, string, error) {
		return nil, "gh: not authenticated", errString("exit status 1")
	}
	msg := fetchGitHubCmd().(ghDataMsg)
	if msg.err == nil {
		t.Fatal("expected error from fetch")
	}
}

func TestRemoteIsGitHub(t *testing.T) {
	if !remoteIsGitHub("origin\tgit@github.com:syrull/pluto.git (fetch)") {
		t.Fatal("ssh github remote should be detected")
	}
	if !remoteIsGitHub("origin\thttps://github.com/x/y.git (push)") {
		t.Fatal("https github remote should be detected")
	}
	if remoteIsGitHub("origin\tgit@gitlab.com:x/y.git (fetch)") {
		t.Fatal("non-github remote should not be detected")
	}
}

func TestDevelopPrompt(t *testing.T) {
	p := developPrompt(ghIssue{Number: 24, Title: "Show branch", Body: "please add it"})
	if !strings.Contains(p, "#24") || !strings.Contains(p, "Show branch") || !strings.Contains(p, "please add it") {
		t.Fatalf("develop prompt missing content:\n%s", p)
	}
}

func TestReviewPrompt(t *testing.T) {
	p := reviewPrompt(ghPR{Number: 12, Title: "Fix it", Branch: "fix-24"})
	if !strings.Contains(p, "#12") || !strings.Contains(p, "fix-24") || !strings.Contains(p, "gh pr diff 12") {
		t.Fatalf("review prompt missing content:\n%s", p)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
