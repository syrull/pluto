package tui

import (
	"fmt"
	"strings"

	"github.com/syrull/pluto/internal/debug"
)

// ghContextItem is a GitHub issue or pull request staged as reference context
// for the next message. PR marks a pull request; otherwise it is an issue.
type ghContextItem struct {
	PR     bool
	Number int
	Title  string
	State  string
	Author string
	Labels []string
	Branch string
	URL    string
	Body   string
}

// ghRef identifies a staged item by kind and number so an issue and a PR that
// share a number stay distinct.
type ghRef struct {
	pr  bool
	num int
}

func (c ghContextItem) ref() ghRef { return ghRef{pr: c.PR, num: c.Number} }

// kind names the item type for prompts and notices.
func (c ghContextItem) kind() string {
	if c.PR {
		return "PR"
	}
	return "issue"
}

// issueContext adapts an issue into a staged context item.
func issueContext(is ghIssue) ghContextItem {
	return ghContextItem{
		Number: is.Number, Title: is.Title, State: is.State, Author: is.Author,
		Labels: is.Labels, URL: is.URL, Body: is.Body,
	}
}

// prContext adapts a pull request into a staged context item.
func prContext(pr ghPR) ghContextItem {
	return ghContextItem{
		PR: true, Number: pr.Number, Title: pr.Title, State: pr.State,
		Author: pr.Author, Branch: pr.Branch, URL: pr.URL, Body: pr.Body,
	}
}

// toggleGHContext stages a GitHub issue or PR as context for the next message,
// or removes it when it is already staged, and surfaces a notice describing the
// new state. Membership is keyed by kind + number so re-adding the same item is
// a no-op that toggles it off.
func (m *model) toggleGHContext(item ghContextItem) {
	for i, staged := range m.ghContext {
		if staged.ref() == item.ref() {
			m.ghContext = append(m.ghContext[:i], m.ghContext[i+1:]...)
			m.notice = fmt.Sprintf("✓ removed %s #%d from context — %d staged", item.kind(), item.Number, len(m.ghContext))
			debug.Info(dbgTUI, "gh context removed", "kind", item.kind(), "number", item.Number, "count", len(m.ghContext))
			return
		}
	}
	m.ghContext = append(m.ghContext, item)
	m.notice = fmt.Sprintf("✓ added %s #%d to context — %d staged, sent with your next message", item.kind(), item.Number, len(m.ghContext))
	debug.Info(dbgTUI, "gh context added", "kind", item.kind(), "number", item.Number, "count", len(m.ghContext))
}

// takeGHContext returns the staged items and clears them, so they ride exactly
// one turn.
func (m *model) takeGHContext() []ghContextItem {
	ctx := m.ghContext
	m.ghContext = nil
	return ctx
}

// ghContextRefs lists the kind+number of every item currently staged as context.
func (m *model) ghContextRefs() []ghRef {
	refs := make([]ghRef, len(m.ghContext))
	for i, item := range m.ghContext {
		refs[i] = item.ref()
	}
	return refs
}

// ghContextHeader introduces the reference block, naming the kinds actually
// staged so the wording stays accurate.
func ghContextHeader(items []ghContextItem) string {
	var hasIssue, hasPR bool
	for _, item := range items {
		if item.PR {
			hasPR = true
		} else {
			hasIssue = true
		}
	}
	switch {
	case hasIssue && hasPR:
		return "The following GitHub issues and pull requests are attached as context for this request:"
	case hasPR:
		return "The following GitHub pull requests are attached as context for this request:"
	default:
		return "The following GitHub issues are attached as context for this request:"
	}
}

// ghContextPrompt renders staged items into a reference block prepended to the
// user's message, giving the agent each item's number, title, metadata, and body.
func ghContextPrompt(items []ghContextItem) string {
	var b strings.Builder
	b.WriteString(ghContextHeader(items))
	b.WriteString("\n\n")
	for _, item := range items {
		if item.PR {
			fmt.Fprintf(&b, "## Pull Request #%d: %s\n", item.Number, item.Title)
		} else {
			fmt.Fprintf(&b, "## Issue #%d: %s\n", item.Number, item.Title)
		}
		var meta []string
		if item.State != "" {
			meta = append(meta, item.State)
		}
		if item.Branch != "" {
			meta = append(meta, "branch "+item.Branch)
		}
		if item.Author != "" {
			meta = append(meta, "@"+item.Author)
		}
		if len(item.Labels) > 0 {
			meta = append(meta, strings.Join(item.Labels, ", "))
		}
		if item.URL != "" {
			meta = append(meta, item.URL)
		}
		if len(meta) > 0 {
			b.WriteString(strings.Join(meta, " · "))
			b.WriteByte('\n')
		}
		if body := strings.TrimSpace(item.Body); body != "" {
			b.WriteByte('\n')
			b.WriteString(body)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// composeWithGHContext prepends the staged reference block to input, returning
// input unchanged when nothing is staged.
func composeWithGHContext(items []ghContextItem, input string) string {
	if len(items) == 0 {
		return input
	}
	prefix := strings.TrimRight(ghContextPrompt(items), "\n")
	if strings.TrimSpace(input) == "" {
		return prefix
	}
	return prefix + "\n\n" + input
}

// ghContextChip renders a compact indicator of staged context, e.g.
// "§ context #24, PR #12".
func ghContextChip(items []ghContextItem) string {
	if len(items) == 0 {
		return ""
	}
	labels := make([]string, len(items))
	for i, item := range items {
		if item.PR {
			labels[i] = fmt.Sprintf("PR #%d", item.Number)
		} else {
			labels[i] = fmt.Sprintf("#%d", item.Number)
		}
	}
	return "§ context " + strings.Join(labels, ", ")
}
