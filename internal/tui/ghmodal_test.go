package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func sampleGHModal() *ghModal {
	g := newGHModal()
	g.SetSize(100, 40)
	g.SetData(
		[]ghIssue{
			{Number: 24, Title: "plain issue", State: "OPEN"},
			{Number: 25, Title: "linked issue", State: "OPEN", LinkedPR: 12},
		},
		[]ghPR{
			{Number: 12, Title: "the pr", State: "OPEN", Branch: "fix-25"},
		},
	)
	return g
}

func TestGHModalTabSwitchAndClose(t *testing.T) {
	g := sampleGHModal()
	if g.tab != ghTabIssues {
		t.Fatal("should start on the issues tab")
	}
	if _, out := g.handleKey("tab"); out.kind != ghOutcomeNone || g.tab != ghTabPRs {
		t.Fatalf("tab should switch to PRs, got tab=%d out=%v", g.tab, out.kind)
	}
	if _, out := g.handleKey("esc"); out.kind != ghOutcomeClose {
		t.Fatalf("esc in list should request close, got %v", out.kind)
	}
}

func TestGHModalDevelopUnlinkedIssue(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("enter") // open detail on issue #24
	if g.pane != ghPaneDetail {
		t.Fatal("enter should open the detail pane")
	}
	handled, out := g.handleKey("d")
	if !handled || out.kind != ghOutcomeDevelop || out.issue.Number != 24 {
		t.Fatalf("d on unlinked issue should develop #24, got %+v", out)
	}
}

func TestGHModalLinkedIssueReviewsPR(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("down")  // move to issue #25 (linked)
	g.handleKey("enter") // open detail
	// Develop is not offered for a linked issue.
	if _, out := g.handleKey("d"); out.kind != ghOutcomeNone {
		t.Fatalf("d on a linked issue should do nothing, got %v", out.kind)
	}
	_, out := g.handleKey("r")
	if out.kind != ghOutcomeReview || out.pr.Number != 12 {
		t.Fatalf("r on a linked issue should review PR #12, got %+v", out)
	}
}

func TestGHModalReviewPR(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("tab")   // PRs tab
	g.handleKey("enter") // open detail on PR #12
	_, out := g.handleKey("r")
	if out.kind != ghOutcomeReview || out.pr.Number != 12 {
		t.Fatalf("r on a PR should review it, got %+v", out)
	}
}

func TestGHModalDetailScrollKeysForwarded(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("enter")
	if handled, _ := g.handleKey("down"); handled {
		t.Fatal("scroll keys in detail should be forwarded (handled=false)")
	}
	if handled, _ := g.handleKey("esc"); !handled || g.pane != ghPaneList {
		t.Fatal("esc in detail should return to the list")
	}
}

func TestGHModalNoLineSpill(t *testing.T) {
	g := sampleGHModal()
	g.SetSize(80, 30)
	g.prs[0].Body = strings.Repeat("wrap this body across many rows ", 40)
	g.SetChecks(12, []ghCheck{
		{Name: "build", Workflow: "CI", Bucket: "pass"},
		{Name: strings.Repeat("very-long-check-name-", 20), Bucket: "fail"},
	}, nil)
	g.tab = ghTabPRs
	g.handleKey("enter")

	cw := g.contentWidth()
	for _, view := range []string{g.detailView(cw), g.listView(cw)} {
		for i, ln := range strings.Split(view, "\n") {
			if w := lipgloss.Width(ln); w > cw {
				t.Fatalf("line %d width %d exceeds contentWidth %d: %q", i, w, cw, ln)
			}
		}
	}
}

func TestGHModalDetailFillsPage(t *testing.T) {
	g := sampleGHModal()
	g.SetSize(80, 30)
	g.issues[0].Body = "tiny" // a short body must still fill the page
	g.handleKey("enter")
	if got := len(strings.Split(g.detailView(g.contentWidth()), "\n")); got != g.boxHeight() {
		t.Fatalf("detail view height = %d, want full box height %d", got, g.boxHeight())
	}
}

func TestGHModalCloseIssueRequiresConfirm(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("enter") // open issue #24 detail

	// First [c] arms the confirm and shows the warning button; no outcome yet.
	if _, out := g.handleKey("c"); out.kind != ghOutcomeNone {
		t.Fatalf("first c should not close, got %v", out.kind)
	}
	if !g.confirmClose {
		t.Fatal("first c should arm confirmClose")
	}
	if !strings.Contains(g.detailView(g.contentWidth()), "confirm close") {
		t.Fatal("armed detail view should show the confirm-close button")
	}
	// Second [c] confirms and closes issue #24.
	_, out := g.handleKey("c")
	if out.kind != ghOutcomeCloseIssue || out.issue.Number != 24 {
		t.Fatalf("second c should close issue #24, got %+v", out)
	}
	if g.confirmClose {
		t.Fatal("confirmClose should reset after closing")
	}
}

func TestGHModalCloseDisarmsOnOtherKey(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("enter")
	g.handleKey("c") // arm
	if !g.confirmClose {
		t.Fatal("c should arm confirmClose")
	}
	g.handleKey("down") // any other key disarms
	if g.confirmClose {
		t.Fatal("a non-c key should disarm confirmClose")
	}
}

func TestGHModalCloseNotOfferedForPR(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("tab")   // PRs tab
	g.handleKey("enter") // open PR detail
	if _, out := g.handleKey("c"); out.kind != ghOutcomeNone {
		t.Fatalf("c on a PR should do nothing, got %v", out.kind)
	}
	if strings.Contains(g.detailView(g.contentWidth()), "Close") {
		t.Fatal("PR detail should not offer a Close button")
	}
}

func TestGHModalOpenURL(t *testing.T) {
	g := sampleGHModal()
	g.issues[0].URL = "https://example/24"
	_, out := g.handleKey("o")
	if out.kind != ghOutcomeOpenURL || out.url != "https://example/24" {
		t.Fatalf("o should open the selected URL, got %+v", out)
	}
}

func TestGHModalViewStates(t *testing.T) {
	g := newGHModal()
	g.SetSize(100, 40)
	if !strings.Contains(g.View(), "loading") {
		t.Fatalf("loading state should render a loading line:\n%s", g.View())
	}
	g.SetError(errString("boom"))
	if !strings.Contains(g.View(), "boom") {
		t.Fatalf("error state should render the error:\n%s", g.View())
	}
	g2 := sampleGHModal()
	view := g2.View()
	if !strings.Contains(view, "Issues (2)") || !strings.Contains(view, "PRs (1)") {
		t.Fatalf("list view should show tab counts:\n%s", view)
	}
	g2.handleKey("enter")
	detail := g2.View()
	if !strings.Contains(detail, "Develop") || !strings.Contains(detail, "Open in browser") {
		t.Fatalf("detail view should show action buttons:\n%s", detail)
	}
}
