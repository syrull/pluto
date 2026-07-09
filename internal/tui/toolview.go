package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// maxResultPreviewLines caps how many lines of a multi-line tool result are
// shown inline; the rest are summarized as "… +N more line(s)".
const maxResultPreviewLines = 8

// defaultWrapWidth is used when no terminal width is known yet.
const defaultWrapWidth = 80

// wrapBody wraps body to fit within width after accounting for prefix,
// indenting any continuation lines under the prefix so nothing runs off screen.
func wrapBody(prefix, body string, style lipgloss.Style, width int) string {
	if width <= 0 {
		width = defaultWrapWidth
	}
	pw := lipgloss.Width(prefix)
	w := width - pw
	if w < 10 {
		w = 10
	}
	wrapped := style.Width(w).Render(body)
	lines := strings.Split(wrapped, "\n")
	indent := strings.Repeat(" ", pw)
	for i, ln := range lines {
		if i == 0 {
			lines[i] = prefix + ln
		} else {
			lines[i] = indent + ln
		}
	}
	return strings.Join(lines, "\n")
}

// renderToolCall renders a tool invocation with human-readable arguments
// instead of raw JSON, e.g. "→ read(main.go:10-59)", wrapping long args.
func renderToolCall(width int, toolName, argsJSON string) string {
	prefix := styleToolName.Render("→ "+toolName) + styleToolArgs.Render("(")
	args := formatToolCallArgs(toolName, argsJSON) + ")"
	return wrapBody(prefix, args, styleToolArgs, width)
}

func formatToolCallArgs(toolName, raw string) string {
	switch toolName {
	case "read":
		return formatReadArgs(raw)
	case "write", "edit":
		return formatPathArg(raw)
	case "bash":
		return formatBashArgs(raw)
	case "find":
		return formatFindArgs(raw)
	default:
		return formatGenericArgs(raw)
	}
}

func formatReadArgs(raw string) string {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(raw), &a); err != nil || a.Path == "" {
		return raw
	}
	switch {
	case a.Offset > 0 && a.Limit > 0:
		return fmt.Sprintf("%s:%d-%d", a.Path, a.Offset, a.Offset+a.Limit-1)
	case a.Offset > 0:
		return fmt.Sprintf("%s:%d+", a.Path, a.Offset)
	case a.Limit > 0:
		return fmt.Sprintf("%s (first %d lines)", a.Path, a.Limit)
	default:
		return a.Path
	}
}

func formatPathArg(raw string) string {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(raw), &a); err != nil || a.Path == "" {
		return raw
	}
	return a.Path
}

func formatBashArgs(raw string) string {
	var a struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal([]byte(raw), &a); err != nil || a.Command == "" {
		return raw
	}
	cmd := oneLine(a.Command)
	if a.Timeout > 0 {
		return fmt.Sprintf("%s [timeout %ds]", cmd, a.Timeout)
	}
	return cmd
}

func formatFindArgs(raw string) string {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Glob    string `json:"glob"`
	}
	if err := json.Unmarshal([]byte(raw), &a); err != nil || a.Pattern == "" {
		return raw
	}
	s := a.Pattern
	if a.Path != "" && a.Path != "." {
		s += " in " + a.Path
	}
	if a.Glob != "" {
		s += " --glob " + a.Glob
	}
	return s
}

// formatGenericArgs is the fallback for tools with no dedicated formatter: a
// compact "key: value, …" summary instead of raw JSON braces and quoting.
func formatGenericArgs(raw string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil || len(m) == 0 {
		return raw
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		var v any
		_ = json.Unmarshal(m[k], &v)
		parts[i] = fmt.Sprintf("%s: %s", k, oneLine(fmt.Sprint(v)))
	}
	return strings.Join(parts, ", ")
}

// renderToolResult renders a tool's output: a diff for file-mutating tools,
// otherwise a line-count summary with a bounded preview of the content, all
// wrapped to width so nothing runs off screen.
func renderToolResult(width int, toolName, text string) string {
	if toolName == "write" || toolName == "edit" {
		return renderDiffResult(width, toolName, text)
	}

	label := styleToolResult.Render("← " + toolName + ": ")
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return label + styleHint.Render("(no output)")
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) == 1 {
		return wrapBody(label, lines[0], lipgloss.NewStyle(), width)
	}

	shown, more := lines, 0
	if len(shown) > maxResultPreviewLines {
		more = len(shown) - maxResultPreviewLines
		shown = shown[:maxResultPreviewLines]
	}

	var b strings.Builder
	b.WriteString(label + resultSummary(toolName, len(lines)))
	for _, ln := range shown {
		b.WriteString("\n" + wrapBody("  ", ln, styleToolBody, width))
	}
	if more > 0 {
		b.WriteString(fmt.Sprintf("\n  %s", styleHint.Render(fmt.Sprintf("… +%d more line(s)", more))))
	}
	return b.String()
}

func resultSummary(toolName string, n int) string {
	switch toolName {
	case "find":
		return styleHint.Render(fmt.Sprintf("%d match line(s)", n))
	case "bash":
		return styleHint.Render(fmt.Sprintf("%d line(s) of output", n))
	default:
		return styleHint.Render(fmt.Sprintf("%d line(s)", n))
	}
}

// renderDiffResult renders a write/edit result, whose body (if any) is
// already a unified-style diff produced by the tool itself, wrapped to width.
func renderDiffResult(width int, toolName, result string) string {
	header, body, hasBody := strings.Cut(result, "\n")
	label := styleTool.Render("← " + toolName + ": ")
	if !hasBody {
		return wrapBody(label, header, styleDiffHdr, width)
	}
	var b strings.Builder
	b.WriteString(wrapBody(label, header, styleDiffHdr, width))
	for _, ln := range strings.Split(body, "\n") {
		b.WriteByte('\n')
		b.WriteString(renderDiffLine(width, ln))
	}
	return b.String()
}
