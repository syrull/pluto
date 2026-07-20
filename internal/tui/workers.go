package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/worker"
)

// workersStatus renders the /workers block: a live snapshot of every dispatched
// worker sub-agent with its state, current tool, budget burn, and the concise
// structured results it has produced — never its full transcript. A hint points
// at /workers <id> to inspect one worker's live transcript.
func (m *model) workersStatus() string {
	if m.pool == nil {
		return styleHint.Render("parallel workers are not available in this build")
	}
	statuses := m.pool.Snapshot()
	debug.Info(dbgTUI, "workers status shown", "count", len(statuses))
	if len(statuses) == 0 {
		return styleHint.Render("no workers dispatched yet — the agent fans work out to parallel workers via the workers tool")
	}
	running := 0
	for _, s := range statuses {
		if s.State == worker.StateRunning || s.State == worker.StatePending {
			running++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", styleReview.Render(fmt.Sprintf("◎ workers (%d · %d active)", len(statuses), running)))
	for _, s := range statuses {
		b.WriteString(m.workerRow(s))
		b.WriteByte('\n')
	}
	b.WriteString(styleHint.Render("inspect a worker's live transcript with /workers <id>"))
	return b.String()
}

// workerRow renders one worker's status line plus an indented results/summary
// line when it has produced anything.
func (m *model) workerRow(s worker.Status) string {
	glyph, gstyle := workerGlyph(s.State)
	head := fmt.Sprintf("%s %s %s", gstyle.Render(glyph), stylePrompt.Render(s.ID), styleHint.Render(string(s.State)))
	meta := workerMeta(s)
	if meta != "" {
		head += " " + styleHint.Render("· "+meta)
	}
	lines := []string{head}
	if detail := workerResults(s); detail != "" {
		lines = append(lines, "  "+styleHint.Render(truncCells(detail, m.contentWidth()-2)))
	}
	if sum := oneLine(s.Results.Summary); sum != "" {
		lines = append(lines, "  "+styleModel.Render(truncCells(sum, m.contentWidth()-2)))
	}
	return strings.Join(lines, "\n")
}

// workerGlyph maps a worker state to a monochrome glyph and its style, reusing
// the TUI's shared vocabulary (● busy, ✓ done, ✗ failed, ⚠ canceled).
func workerGlyph(state worker.State) (string, interface{ Render(...string) string }) {
	switch state {
	case worker.StateRunning:
		return "●", styleWorking
	case worker.StateDone:
		return "✓", styleDone
	case worker.StateFailed:
		return "✗", styleErr
	case worker.StateCanceled:
		return "⚠", styleReview
	default: // pending
		return "●", styleHint
	}
}

// workerMeta renders the compact activity line: current tool (or stop reason),
// tool-call count, token spend, and elapsed time.
func workerMeta(s worker.Status) string {
	var parts []string
	if s.CurrentTool != "" {
		parts = append(parts, "tool "+s.CurrentTool)
	} else if s.StopReason != "" && s.StopReason != "completed" {
		parts = append(parts, s.StopReason)
	}
	parts = append(parts, fmt.Sprintf("%d calls", s.ToolCalls))
	if s.Tokens > 0 {
		parts = append(parts, formatTokens(s.Tokens)+" tok")
	}
	if s.Elapsed > 0 {
		parts = append(parts, s.Elapsed.Round(100*time.Millisecond).String())
	}
	if s.Err != "" {
		parts = append(parts, "err: "+oneLine(s.Err))
	}
	return strings.Join(parts, " · ")
}

// workerResults renders a one-line digest of a worker's structured findings.
func workerResults(s worker.Status) string {
	r := s.Results
	var parts []string
	add := func(label string, vals []string) {
		if len(vals) > 0 {
			parts = append(parts, fmt.Sprintf("%s: %s", label, strings.Join(vals, ", ")))
		}
	}
	add("flags", r.Flags)
	add("creds", r.Creds)
	add("footholds", r.Footholds)
	add("vulns", r.Vulns)
	add("hosts", r.Hosts)
	add("services", r.Services)
	add("notes", r.Notes)
	return strings.Join(parts, " · ")
}

// workerTranscript renders one worker's live transcript for inspection, coalesced
// per event so a long run stays readable. Returns a hint when the id is unknown.
func (m *model) workerTranscript(id string) string {
	if m.pool == nil {
		return styleHint.Render("parallel workers are not available in this build")
	}
	events, ok := m.pool.Transcript(id)
	if !ok {
		return styleErr.Render("✗ no worker with id " + id + " — run /workers to list them")
	}
	debug.Info(dbgTUI, "worker transcript shown", "id", id, "events", len(events))
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", styleReview.Render("◎ worker "+id+" transcript"))
	if len(events) == 0 {
		b.WriteString(styleHint.Render("(no activity recorded yet)"))
		return b.String()
	}
	w := m.contentWidth()
	for _, ev := range events {
		b.WriteString(renderWorkerEvent(w, ev))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderWorkerEvent renders one transcript event with the same glyph vocabulary
// as the main conversation.
func renderWorkerEvent(width int, ev worker.Event) string {
	switch ev.Kind {
	case "thinking":
		return styleThink.Render(truncCells(oneLine(ev.Text), width))
	case "tool_call":
		return styleToolName.Render("→ "+ev.Tool) + " " + styleToolArgs.Render(truncCells(oneLine(ev.Text), width-len(ev.Tool)-4))
	case "tool_result":
		return styleToolResult.Render(truncCells(oneLine(ev.Text), width))
	case "error":
		return styleErr.Render("✗ " + truncCells(oneLine(ev.Text), width-2))
	default: // text
		return styleModel.Render(truncCells(oneLine(ev.Text), width))
	}
}
