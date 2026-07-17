package tui

import (
	"fmt"
	"strings"

	"github.com/syrull/pluto/internal/debug"
)

// toggleGHContext stages a GitHub issue as context for the next message, or
// removes it when it is already staged, and surfaces a notice describing the new
// state. Membership is keyed by issue number so re-adding the same issue is a
// no-op that toggles it off.
func (m *model) toggleGHContext(is ghIssue) {
	for i, staged := range m.ghContext {
		if staged.Number == is.Number {
			m.ghContext = append(m.ghContext[:i], m.ghContext[i+1:]...)
			m.notice = fmt.Sprintf("✓ removed issue #%d from context — %d staged", is.Number, len(m.ghContext))
			debug.Info(dbgTUI, "gh context removed", "issue", is.Number, "count", len(m.ghContext))
			return
		}
	}
	m.ghContext = append(m.ghContext, is)
	m.notice = fmt.Sprintf("✓ added issue #%d to context — %d staged, sent with your next message", is.Number, len(m.ghContext))
	debug.Info(dbgTUI, "gh context added", "issue", is.Number, "count", len(m.ghContext))
}

// takeGHContext returns the staged issues and clears them, so they ride exactly
// one turn.
func (m *model) takeGHContext() []ghIssue {
	ctx := m.ghContext
	m.ghContext = nil
	return ctx
}

// ghContextNumbers lists the issue numbers currently staged as context.
func (m *model) ghContextNumbers() []int {
	nums := make([]int, len(m.ghContext))
	for i, is := range m.ghContext {
		nums[i] = is.Number
	}
	return nums
}

// ghContextPrompt renders staged issues into a reference block prepended to the
// user's message, giving the agent each issue's number, title, metadata, and body.
func ghContextPrompt(issues []ghIssue) string {
	var b strings.Builder
	b.WriteString("The following GitHub issues are attached as context for this request:\n\n")
	for _, is := range issues {
		fmt.Fprintf(&b, "## Issue #%d: %s\n", is.Number, is.Title)
		var meta []string
		if is.State != "" {
			meta = append(meta, is.State)
		}
		if is.Author != "" {
			meta = append(meta, "@"+is.Author)
		}
		if len(is.Labels) > 0 {
			meta = append(meta, strings.Join(is.Labels, ", "))
		}
		if is.URL != "" {
			meta = append(meta, is.URL)
		}
		if len(meta) > 0 {
			b.WriteString(strings.Join(meta, " · "))
			b.WriteByte('\n')
		}
		if body := strings.TrimSpace(is.Body); body != "" {
			b.WriteByte('\n')
			b.WriteString(body)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// composeWithGHContext prepends the staged-issue reference block to input,
// returning input unchanged when nothing is staged.
func composeWithGHContext(issues []ghIssue, input string) string {
	if len(issues) == 0 {
		return input
	}
	prefix := strings.TrimRight(ghContextPrompt(issues), "\n")
	if strings.TrimSpace(input) == "" {
		return prefix
	}
	return prefix + "\n\n" + input
}

// ghContextChip renders a compact indicator of staged issue context, e.g.
// "🔗 context #24, #25".
func ghContextChip(issues []ghIssue) string {
	if len(issues) == 0 {
		return ""
	}
	nums := make([]string, len(issues))
	for i, is := range issues {
		nums[i] = fmt.Sprintf("#%d", is.Number)
	}
	return "🔗 context " + strings.Join(nums, ", ")
}
