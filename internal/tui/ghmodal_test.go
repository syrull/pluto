package tui

import (
	"strings"
	"testing"
	"time"

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

func TestGHModalMergePRRequiresConfirm(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("tab")   // PRs tab
	g.handleKey("enter") // open PR #12 detail

	// First [m] arms the confirm and shows the warning button; no outcome yet.
	if _, out := g.handleKey("m"); out.kind != ghOutcomeNone {
		t.Fatalf("first m should not merge, got %v", out.kind)
	}
	if !g.confirmMerge {
		t.Fatal("first m should arm confirmMerge")
	}
	if !strings.Contains(g.detailView(g.contentWidth()), "confirm merge") {
		t.Fatal("armed detail view should show the confirm-merge button")
	}
	// Second [m] confirms and merges PR #12.
	_, out := g.handleKey("m")
	if out.kind != ghOutcomeMergePR || out.pr.Number != 12 {
		t.Fatalf("second m should merge PR #12, got %+v", out)
	}
	if g.confirmMerge {
		t.Fatal("confirmMerge should reset after merging")
	}
}

func TestGHModalMergeDisarmsOnOtherKey(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("tab")
	g.handleKey("enter")
	g.handleKey("m") // arm
	if !g.confirmMerge {
		t.Fatal("m should arm confirmMerge")
	}
	g.handleKey("down") // any other key disarms
	if g.confirmMerge {
		t.Fatal("a non-m key should disarm confirmMerge")
	}
}

func TestGHModalMergeDraftPRNotArmed(t *testing.T) {
	g := sampleGHModal()
	g.prs[0].Draft = true
	g.handleKey("tab")
	g.handleKey("enter")
	// A draft passes the outcome straight through without arming so the model
	// can surface a notice.
	_, out := g.handleKey("m")
	if out.kind != ghOutcomeMergePR || out.pr.Number != 12 {
		t.Fatalf("m on a draft should still yield a merge outcome, got %+v", out)
	}
	if g.confirmMerge {
		t.Fatal("m on a draft should not arm confirmMerge")
	}
	if !strings.Contains(g.detailView(g.contentWidth()), "Merge (draft)") {
		t.Fatal("draft PR detail should show the disabled merge label")
	}
}

func TestGHModalMergeNotOfferedForIssue(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("enter") // open issue #24 detail
	if _, out := g.handleKey("m"); out.kind != ghOutcomeNone {
		t.Fatalf("m on an issue should do nothing, got %v", out.kind)
	}
	if strings.Contains(g.detailView(g.contentWidth()), "Merge") {
		t.Fatal("issue detail should not offer a Merge button")
	}
}

func TestGHModalAddToContext(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("enter") // open issue #24 detail
	if !strings.Contains(g.detailView(g.contentWidth()), "Add to Context") {
		t.Fatal("issue detail should offer the Add to Context button")
	}

	handled, out := g.handleKey("a")
	if !handled || out.kind != ghOutcomeAddContext || out.ctx.PR || out.ctx.Number != 24 {
		t.Fatalf("a should add issue #24 to context, got %+v", out)
	}
	if !g.added[ghRef{num: 24}] {
		t.Fatal("a should mark issue #24 as added")
	}
	if !strings.Contains(g.detailView(g.contentWidth()), "In context") {
		t.Fatal("an added issue should show the In context button")
	}

	// Pressing a again toggles it back off.
	_, out = g.handleKey("a")
	if out.kind != ghOutcomeAddContext || g.added[ghRef{num: 24}] {
		t.Fatalf("second a should toggle issue #24 off, added=%v out=%+v", g.added[ghRef{num: 24}], out)
	}
}

func TestGHModalAddToContextLinkedIssue(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("down")  // move to linked issue #25
	g.handleKey("enter") // open detail
	_, out := g.handleKey("a")
	if out.kind != ghOutcomeAddContext || out.ctx.PR || out.ctx.Number != 25 {
		t.Fatalf("a on a linked issue should still add it, got %+v", out)
	}
}

func TestGHModalAddToContextPR(t *testing.T) {
	g := sampleGHModal()
	g.handleKey("tab")   // PRs tab
	g.handleKey("enter") // open PR #12 detail
	if !strings.Contains(g.detailView(g.contentWidth()), "Add to Context") {
		t.Fatal("PR detail should offer the Add to Context button")
	}

	handled, out := g.handleKey("a")
	if !handled || out.kind != ghOutcomeAddContext || !out.ctx.PR || out.ctx.Number != 12 {
		t.Fatalf("a should add PR #12 to context, got %+v", out)
	}
	if !g.added[ghRef{pr: true, num: 12}] {
		t.Fatal("a should mark PR #12 as added")
	}
	if !strings.Contains(g.detailView(g.contentWidth()), "In context") {
		t.Fatal("an added PR should show the In context button")
	}

	// Pressing a again toggles it back off.
	_, out = g.handleKey("a")
	if out.kind != ghOutcomeAddContext || g.added[ghRef{pr: true, num: 12}] {
		t.Fatalf("second a should toggle PR #12 off, added=%v out=%+v", g.added[ghRef{pr: true, num: 12}], out)
	}
}

func TestGHModalIssueActionsPackWithinWidth(t *testing.T) {
	g := sampleGHModal()
	g.SetSize(80, 30) // narrow: the four issue actions can't fit one row
	g.handleKey("enter")
	assertDetailFits(t, g)
}

func TestGHModalPRActionsPackWithinWidth(t *testing.T) {
	g := sampleGHModal()
	g.SetSize(80, 30) // narrow: the four PR actions can't fit one row
	g.handleKey("tab")
	g.handleKey("enter")
	assertDetailFits(t, g)
}

func assertDetailFits(t *testing.T, g *ghModal) {
	t.Helper()
	cw := g.contentWidth()
	lines := strings.Split(g.detailView(cw), "\n")
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w > cw {
			t.Fatalf("detail line %d width %d exceeds contentWidth %d", i, w, cw)
		}
	}
	if got := len(lines); got != g.boxHeight() {
		t.Fatalf("detail view height = %d, want full box height %d", got, g.boxHeight())
	}
}

func TestGHModalSetContextSeedsButton(t *testing.T) {
	g := sampleGHModal()
	g.SetContext([]ghRef{{num: 24}, {pr: true, num: 12}})

	g.handleKey("enter") // open issue #24 detail
	if !strings.Contains(g.detailView(g.contentWidth()), "In context") {
		t.Fatal("a pre-staged issue should open showing the In context button")
	}

	g.handleKey("esc")
	g.handleKey("tab")   // PRs tab
	g.handleKey("enter") // open PR #12 detail
	if !strings.Contains(g.detailView(g.contentWidth()), "In context") {
		t.Fatal("a pre-staged PR should open showing the In context button")
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

func TestGHModalDetailShowsOpenedTime(t *testing.T) {
	g := sampleGHModal()
	g.issues[0].CreatedAt = time.Now().Add(-3 * 24 * time.Hour)
	g.handleKey("enter") // open issue #24 detail
	if got := g.detailView(g.contentWidth()); !strings.Contains(got, "opened 3d ago") {
		t.Fatalf("issue detail should show opened time:\n%s", got)
	}

	g.handleKey("esc")
	g.handleKey("tab")
	g.prs[0].CreatedAt = time.Now().Add(-2 * time.Hour)
	g.handleKey("enter") // open PR #12 detail
	if got := g.detailView(g.contentWidth()); !strings.Contains(got, "opened 2h ago") {
		t.Fatalf("PR detail should show opened time:\n%s", got)
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
