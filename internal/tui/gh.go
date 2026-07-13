package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ghListLimit caps how many issues/PRs are fetched per tab.
const ghListLimit = 50

// ghTimeout bounds each gh invocation so a hung network call can't wedge the UI.
const ghTimeout = 20 * time.Second

// ghIssue is one open GitHub issue. LinkedPR is the number of a pull request that
// would close it (via a closing reference), or 0 when none is linked.
type ghIssue struct {
	Number   int
	Title    string
	State    string
	URL      string
	Body     string
	Author   string
	Labels   []string
	LinkedPR int
}

// ghPR is one open GitHub pull request.
type ghPR struct {
	Number int
	Title  string
	State  string
	URL    string
	Body   string
	Author string
	Branch string
	Draft  bool
}

// ghData is the result of fetching issues and PRs; err is set when the fetch failed.
type ghData struct {
	issues []ghIssue
	prs    []ghPR
	err    error
}

// ghDataMsg delivers a fetch result to the model.
type ghDataMsg ghData

// ghRunRaw invokes the gh CLI and returns its stdout, stderr, and exit error
// separately. It is a package var so tests can stub the CLI. Callers that need
// stdout even on a non-zero exit (e.g. `gh pr checks`, which exits non-zero while
// checks are pending or failing) use this directly.
var ghRunRaw = func(args ...string) (stdout []byte, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return out.Bytes(), errBuf.String(), err
}

// ghRun invokes the gh CLI and returns its stdout, or an error carrying stderr.
func ghRun(args ...string) ([]byte, error) {
	out, stderr, err := ghRunRaw(args...)
	if err != nil {
		if msg := strings.TrimSpace(stderr); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, err
	}
	return out, nil
}

// ghAvailable reports whether the gh CLI is installed and the working tree has a
// github.com remote, so the /gh feature makes sense here.
func ghAvailable() bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	out, err := gitRun("remote", "-v")
	if err != nil {
		return false
	}
	return remoteIsGitHub(out)
}

// remoteIsGitHub reports whether git remote output points at github.com.
func remoteIsGitHub(remotes string) bool {
	return strings.Contains(remotes, "github.com")
}

// fetchGitHubCmd gathers open issues and PRs off the UI goroutine and links each
// issue to the PR that closes it.
func fetchGitHubCmd() tea.Msg {
	issues, err := ghListIssues()
	if err != nil {
		return ghDataMsg{err: err}
	}
	prs, links, err := ghListPRs()
	if err != nil {
		return ghDataMsg{err: err}
	}
	for i := range issues {
		if pr, ok := links[issues[i].Number]; ok {
			issues[i].LinkedPR = pr
		}
	}
	return ghDataMsg{issues: issues, prs: prs}
}

func ghListIssues() ([]ghIssue, error) {
	out, err := ghRun("issue", "list", "--state", "open", "--limit", strconv.Itoa(ghListLimit),
		"--json", "number,title,state,url,body,author,labels")
	if err != nil {
		return nil, err
	}
	return parseIssues(out)
}

func parseIssues(data []byte) ([]ghIssue, error) {
	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
		URL    string `json:"url"`
		Body   string `json:"body"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	issues := make([]ghIssue, len(raw))
	for i, r := range raw {
		labels := make([]string, len(r.Labels))
		for j, l := range r.Labels {
			labels[j] = l.Name
		}
		issues[i] = ghIssue{
			Number: r.Number, Title: r.Title, State: r.State, URL: r.URL,
			Body: r.Body, Author: r.Author.Login, Labels: labels,
		}
	}
	return issues, nil
}

// ghListPRs returns open PRs plus a map of issue number → the PR that closes it.
func ghListPRs() ([]ghPR, map[int]int, error) {
	out, err := ghRun("pr", "list", "--state", "open", "--limit", strconv.Itoa(ghListLimit),
		"--json", "number,title,state,url,body,author,headRefName,isDraft,closingIssuesReferences")
	if err != nil {
		return nil, nil, err
	}
	return parsePRs(out)
}

func parsePRs(data []byte) ([]ghPR, map[int]int, error) {
	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
		URL    string `json:"url"`
		Body   string `json:"body"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		HeadRefName string `json:"headRefName"`
		IsDraft     bool   `json:"isDraft"`
		Closing     []struct {
			Number int `json:"number"`
		} `json:"closingIssuesReferences"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}
	prs := make([]ghPR, len(raw))
	links := map[int]int{}
	for i, r := range raw {
		prs[i] = ghPR{
			Number: r.Number, Title: r.Title, State: r.State, URL: r.URL,
			Body: r.Body, Author: r.Author.Login, Branch: r.HeadRefName, Draft: r.IsDraft,
		}
		for _, c := range r.Closing {
			if _, ok := links[c.Number]; !ok {
				links[c.Number] = r.Number
			}
		}
	}
	return prs, links, nil
}

// ghCloseMsg reports the result of closing an issue.
type ghCloseMsg struct {
	number int
	err    error
}

// closeIssueCmd closes an issue via gh off the UI goroutine.
func closeIssueCmd(number int) tea.Cmd {
	return func() tea.Msg {
		_, err := ghRun("issue", "close", strconv.Itoa(number))
		return ghCloseMsg{number: number, err: err}
	}
}

// ghCheck is one CI status check on a pull request. Bucket categorizes State
// into pass/fail/pending/skipping/cancel (per `gh pr checks --json bucket`).
type ghCheck struct {
	Name     string
	State    string
	Bucket   string
	Workflow string
}

// ghChecksMsg delivers a PR's CI status to the model; pr is the PR number the
// checks belong to so a stale result for a since-closed detail is ignored.
type ghChecksMsg struct {
	pr     int
	checks []ghCheck
	err    error
}

// fetchChecksCmd gathers a PR's CI checks off the UI goroutine.
func fetchChecksCmd(pr int) tea.Cmd {
	return func() tea.Msg {
		checks, err := ghListChecks(pr)
		return ghChecksMsg{pr: pr, checks: checks, err: err}
	}
}

// ghListChecks returns the CI checks for a PR. `gh pr checks` exits non-zero
// while checks are pending or failing yet still prints the JSON, so a parseable
// stdout is trusted regardless of exit code; a "no checks" report is not an error.
func ghListChecks(pr int) ([]ghCheck, error) {
	stdout, stderr, err := ghRunRaw("pr", "checks", strconv.Itoa(pr),
		"--json", "name,state,bucket,workflow")
	if len(bytes.TrimSpace(stdout)) > 0 {
		if checks, perr := parseChecks(stdout); perr == nil {
			return checks, nil
		}
	}
	if strings.Contains(stderr, "no checks reported") {
		return nil, nil
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, err
	}
	return nil, nil
}

func parseChecks(data []byte) ([]ghCheck, error) {
	var raw []struct {
		Name     string `json:"name"`
		State    string `json:"state"`
		Bucket   string `json:"bucket"`
		Workflow string `json:"workflow"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	checks := make([]ghCheck, len(raw))
	for i, r := range raw {
		checks[i] = ghCheck{Name: r.Name, State: r.State, Bucket: r.Bucket, Workflow: r.Workflow}
	}
	return checks, nil
}

// developPrompt composes the conversation seed for developing an issue.
func developPrompt(is ghIssue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Implement GitHub issue #%d: %s\n\n", is.Number, is.Title)
	if body := strings.TrimSpace(is.Body); body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	b.WriteString("Explore the relevant code first to understand it, then implement the change. When you're done, summarize what you did.")
	return b.String()
}

// reviewPrompt composes the conversation seed for reviewing a pull request.
func reviewPrompt(pr ghPR) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review pull request #%d: %s", pr.Number, pr.Title)
	if pr.Branch != "" {
		fmt.Fprintf(&b, " (branch %s)", pr.Branch)
	}
	b.WriteString("\n\n")
	if body := strings.TrimSpace(pr.Body); body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Inspect the changes with `gh pr diff %d`, read the surrounding code, then give concrete, actionable review feedback.", pr.Number)
	return b.String()
}
