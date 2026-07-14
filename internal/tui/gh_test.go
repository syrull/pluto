package tui

import (
	"strings"
	"testing"
	"time"
)

func TestParseIssues(t *testing.T) {
	data := []byte(`[
		{"number":24,"title":"Show branch","state":"OPEN","url":"https://x/24","body":"do it","author":{"login":"syrull"},"labels":[{"name":"bug"},{"name":"ui"}],"createdAt":"2024-01-02T03:04:05Z"}
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
	if !is.CreatedAt.Equal(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("createdAt = %v", is.CreatedAt)
	}
}

func TestParsePRsLinksClosingIssues(t *testing.T) {
	data := []byte(`[
		{"number":12,"title":"Fix branch","state":"OPEN","url":"https://x/12","body":"b","author":{"login":"me"},"headRefName":"fix-24","isDraft":true,"createdAt":"2024-01-02T03:04:05Z","closingIssuesReferences":[{"number":24}]}
	]`)
	prs, links, err := parsePRs(data)
	if err != nil {
		t.Fatalf("parsePRs: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 12 || prs[0].Branch != "fix-24" || !prs[0].Draft {
		t.Fatalf("unexpected pr: %+v", prs)
	}
	if !prs[0].CreatedAt.Equal(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("createdAt = %v", prs[0].CreatedAt)
	}
	if links[24] != 12 {
		t.Fatalf("expected issue 24 linked to PR 12, got %v", links)
	}
}

func TestOpenedAgo(t *testing.T) {
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, ""},
		{"seconds", now.Add(-30 * time.Second), "just now"},
		{"minutes", now.Add(-5 * time.Minute), "5m ago"},
		{"hours", now.Add(-3 * time.Hour), "3h ago"},
		{"days", now.Add(-3 * 24 * time.Hour), "3d ago"},
		{"old", now.Add(-90 * 24 * time.Hour), "on 2024-03-03"},
	}
	for _, c := range cases {
		if got := openedAgo(c.t, now); got != c.want {
			t.Errorf("%s: openedAgo = %q, want %q", c.name, got, c.want)
		}
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

func TestMergePRCmd(t *testing.T) {
	orig := ghRunRaw
	defer func() { ghRunRaw = orig }()
	var got []string
	ghRunRaw = func(args ...string) ([]byte, string, error) {
		got = args
		return nil, "", nil
	}
	msg, ok := mergePRCmd(12)().(ghMergeMsg)
	if !ok {
		t.Fatal("mergePRCmd did not return ghMergeMsg")
	}
	if msg.number != 12 || msg.err != nil {
		t.Fatalf("unexpected merge msg: %+v", msg)
	}
	if strings.Join(got, " ") != "pr merge 12 --squash" {
		t.Fatalf("unexpected gh args: %v", got)
	}
}

func TestMergePRCmdSurfacesError(t *testing.T) {
	orig := ghRunRaw
	defer func() { ghRunRaw = orig }()
	ghRunRaw = func(args ...string) ([]byte, string, error) {
		return nil, "Pull request is not mergeable", errString("exit status 1")
	}
	msg := mergePRCmd(12)().(ghMergeMsg)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "not mergeable") {
		t.Fatalf("expected mergeable error, got %v", msg.err)
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
