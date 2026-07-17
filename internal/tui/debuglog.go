package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/debug"
)

// dbgTUI is the debug component tag for all TUI events.
const dbgTUI = "tui"

// msgSummary returns a short type tag and salient fields for a tea.Msg so an
// Update can be logged with enough detail to reconstruct what triggered it.
func msgSummary(msg tea.Msg) (string, []any) {
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		return "key", []any{"key", m.String()}
	case tea.WindowSizeMsg:
		return "resize", []any{"w", m.Width, "h", m.Height}
	case tea.MouseClickMsg:
		return "mouse-click", []any{"x", m.X, "y", m.Y, "button", m.String()}
	case tea.MouseReleaseMsg:
		return "mouse-release", []any{"x", m.X, "y", m.Y}
	case tea.MouseWheelMsg:
		return "mouse-wheel", []any{"x", m.X, "y", m.Y, "button", m.String()}
	case tea.MouseMotionMsg:
		return "mouse-motion", []any{"x", m.X, "y", m.Y}
	case tea.PasteMsg:
		return "paste", []any{"chars", len(m.Content)}
	case eventMsg:
		return "agent-event", []any{"kind", m.Kind, "tool", m.Tool, "id", m.id, "chars", len(m.Text)}
	case doneMsg:
		return "agent-done", []any{"id", m.id}
	case goalEvalMsg:
		return "goal-eval", []any{"id", m.id, "met", m.met, "err", errStr(m.err)}
	case approvalReqMsg:
		return "approval-req", []any{"tool", m.req.call.Name, "source", m.req.rr.Source}
	case labelMsg:
		return "label", []any{"id", m.id, "label", m.label}
	case loginDoneMsg:
		return "login-done", []any{"status", m.status, "err", errStr(m.err)}
	case gitInfoMsg:
		return "git-info", []any{"dir", m.dir, "repo", m.info.isRepo, "changes", len(m.info.status)}
	case bashInlineMsg:
		return "inline-bash", []any{"epoch", m.epoch}
	case orbitTickMsg:
		return "orbit-tick", []any{"epoch", m.epoch}
	case ghDataMsg:
		return "gh-data", []any{"issues", len(m.issues), "prs", len(m.prs), "err", errStr(m.err)}
	default:
		return fmt.Sprintf("%T", msg), nil
	}
}

// msgLevel picks the log level for an incoming message: cosmetic, high-frequency
// animation ticks live at TRACE so the default DEBUG log isn't flooded by an
// idle dashboard; everything else is a meaningful interaction at DEBUG.
func msgLevel(msg tea.Msg) debug.Level {
	switch msg.(type) {
	case orbitTickMsg:
		return debug.LevelTrace
	default:
		return debug.LevelDebug
	}
}

// focusName renders a focus pane for logging.
func focusName(f focusPane) string {
	switch f {
	case paneChat:
		return "chat"
	case paneTree:
		return "files"
	case paneChanges:
		return "changes"
	case paneAgents:
		return "agents"
	default:
		return "?"
	}
}

// overlayName reports which modal/overlay (if any) is currently capturing input.
func (m model) overlayName() string {
	switch {
	case m.approval != nil:
		return "approval"
	case m.ghm != nil:
		return "gh"
	case m.modal != nil:
		return "modal"
	case m.picker != nil:
		return "picker:" + pickerKindName(m.pickerKind)
	case m.finder != nil:
		return "finder"
	case m.cmdMenu != nil:
		return "cmdmenu"
	default:
		return "none"
	}
}

func pickerKindName(k pickerKind) string {
	switch k {
	case pickerModel:
		return "model"
	case pickerThink:
		return "think"
	case pickerResume:
		return "resume"
	case pickerNewAgent:
		return "newagent"
	case pickerCloseAgent:
		return "closeagent"
	default:
		return "none"
	}
}

// stateFingerprint captures the visible state compactly so consecutive identical
// frames can be coalesced in the frame-render log.
func (m model) stateFingerprint() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ready=%t home=%t focus=%s busy=%t w=%d h=%d overlay=%s",
		m.ready, m.showHome, focusName(m.focus), m.busy, m.width, m.height, m.overlayName())
	off, atBottom := 0, false
	if m.ready {
		off = m.vp.YOffset()
		atBottom = m.vp.AtBottom()
	}
	fmt.Fprintf(&b, " scroll=%d bottom=%t lines=%d stream=%d/%d notice=%q active=%d ws=%d orbit=%d",
		off, atBottom, len(m.lines), len(m.streamText), len(m.streamThink),
		m.notice, m.active, len(m.workspaces), m.orbitFrame)
	fmt.Fprintf(&b, " collapse=%t/%t/%t", m.collapsedAgents, m.collapsedFiles, m.collapsedChanges)
	return b.String()
}

// logFrame records a rendered frame (TRACE, coalesced) with a compact
// fingerprint; body is passed through for FramesFull dumping.
func (m model) logFrame(body string, dur time.Duration) {
	if !debug.FramesEnabled(dbgTUI) {
		return
	}
	debug.Frame(dbgTUI, m.stateFingerprint(), body,
		"focus", focusName(m.focus), "overlay", m.overlayName(),
		"w", m.width, "h", m.height, "home", m.showHome, "busy", m.busy, "dur", dur)
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
