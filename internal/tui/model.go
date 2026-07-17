package tui

import (
	"context"
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/session"
	"github.com/syrull/pluto/internal/tui/widgets"
)

// eventMsg carries one agent Event to the UI, tagged with the id of the
// workspace that produced it so background runs update their own state. Its
// fields mirror agent.Event (see event()).
type eventMsg struct {
	Kind string
	Text string
	Tool string
	id   int
}

// event rebuilds the agent.Event an eventMsg carries.
func (e eventMsg) event() agent.Event {
	return agent.Event{Kind: e.Kind, Text: e.Text, Tool: e.Tool}
}

// doneMsg signals a workspace's Run finished; id names the workspace.
type doneMsg struct{ id int }

// labelMsg delivers an auto-generated label for a workspace after its first turn.
type labelMsg struct {
	id    int
	label string
}

type loginDoneMsg struct {
	status string
	err    error
}

// LoginHook wires the /login command to the OAuth flow.
//
// Authorize builds the browser authorization URL and returns a handle. Wait
// blocks (on a local callback server) until the redirect arrives, then
// exchanges the code and re-authenticates the live provider, returning a status
// line. Complete is the manual fallback for when the browser is on another
// machine: the user pastes the redirect URL or code.
type LoginHook struct {
	Authorize func() (url string, flow any, err error)
	Wait      func(flow any) (status string, err error)
	Complete  func(flow any, pastedInput string) (status string, err error)
}

// entry is one committed transcript block. An entry may be tied to a retained
// tool output (outputID > 0) so a click or ctrl+o reopens its full text in a
// modal, or to a retained code block (copyID > 0) so a click copies it.
type entry struct {
	text     string
	outputID int
	copyID   int
}

type model struct {
	agent     *agent.Agent
	login     *LoginHook
	loginFlow any                // pending OAuth flow awaiting manual code entry (paste fallback)
	lines     []entry            // committed transcript blocks
	input     textarea.Model     // current input buffer, multi-line with word wrap
	busy      bool               // agent running; input disabled
	events    chan eventMsg      // agent → UI stream for the active Run
	cancel    context.CancelFunc // aborts the in-flight Run; nil when idle
	width     int
	height    int

	// attachments are images staged (via /image) for the next message; sent with
	// the turn on submit, then cleared.
	attachments []llm.Attachment

	// ghContext holds GitHub issues and PRs staged (via the browser's [Add to
	// Context] action) as reference material for the next message; prepended to the
	// turn on submit, then cleared.
	ghContext []ghContextItem

	// history records submitted inputs (oldest→newest) so ctrl+p/ctrl+n recall them
	// like a shell prompt; histPos is the cursor into it, where histPos == len(history)
	// means "not navigating" — the buffer is a fresh, editable line.
	history []string
	histPos int

	// inlineCancel aborts a running inline `!` shell command; nil when none is
	// running. inlineEpoch fences its result so a canceled or superseded run's
	// late-arriving output is dropped.
	inlineCancel context.CancelFunc
	inlineEpoch  int

	vp    viewport.Model
	ready bool

	// mouse enables mouse capture (wheel scroll, click-to-open). Off by default so
	// the terminal keeps native text selection; PLUTO_MOUSE=on sets the initial
	// state and ctrl+t toggles it at runtime.
	mouse bool

	// Streaming accumulators: strings because Bubbletea copies the model by value and Builder panics when copied.
	streamText  string
	streamThink string

	md *glamour.TermRenderer // markdown renderer, rebuilt on resize

	picker     *widgets.ListPicker
	pickerKind pickerKind

	// cmdMenu is the open slash-command autocomplete popup, anchored above the
	// input; nil when the buffer isn't a bare slash command.
	cmdMenu *widgets.CommandMenu

	// outputs retains full tool results; pendingTool/pendingArgs carry the
	// in-flight tool call so its result can be titled and its content retained;
	// modal is the open full-output viewer, if any.
	outputs     []toolOutput
	pendingTool string
	pendingArgs string
	modal       *widgets.Modal
	// modalPath is the file the open modal is viewing (for ctrl+g "open in
	// editor"), and modalIsFile marks a file/diff modal so it can be refreshed
	// after an edit; both are empty/false for pathless outputs (bash/find).
	modalPath   string
	modalIsFile bool

	// codeBlocks retains fenced code blocks from assistant messages so they can
	// be copied to the clipboard; notice is the transient notifications-widget
	// message (raw text) shown above the status line, cleared on the next key or
	// mouse event.
	codeBlocks []codeBlock
	notice     string

	// store persists conversations, opened lazily on first /save or /resume;
	// sessionName is the id of the active saved conversation, for resave and
	// autosave.
	store       *session.Store
	sessionName string

	// showHome renders the launch dashboard in place of the transcript; it starts
	// true, dismisses on the first typed/submitted input, and reopens via /dash.
	// git/gitReady hold the async-gathered project state; tip is the launch hint.
	showHome bool
	git      gitInfo
	gitReady bool
	tip      string

	// orbitFrame advances the home-screen planet animation; orbitEpoch fences the
	// tick loop so reopening the dashboard doesn't leave two loops running.
	orbitFrame int
	orbitEpoch int

	// tree is the sidebar file explorer; changes is the second pane listing only
	// modified/created files (nil when the tree is clean); focus selects which
	// pane the keyboard drives.
	tree    *widgets.Tree
	changes *widgets.Tree
	focus   focusPane

	// finder is the open fuzzy file picker (from '/' in the Files pane), if any;
	// finderBase is the directory its repo-relative results resolve against.
	finder     *widgets.FuzzyPicker
	finderBase string

	// ghm is the open GitHub browser (issues/PRs), if any.
	ghm *ghModal

	// approver bridges an agent's blocking judge-error approval request to this UI;
	// approval is the request currently prompting the user, if any. Both are nil in
	// the bare/test model and for background/headless runs, which fall back to the
	// OnJudgeError policy instead of prompting.
	approver *Approver
	approval *approvalRequest

	// workspaces are the parallel agents. The model's per-agent fields above mirror
	// the active workspace (workspaces[active]); background workspaces keep their
	// own copy, kept in sync via stash/unstash. newAgent builds a fresh agent for a
	// spawned workspace (same provider/tools/config); nextID hands out ids.
	workspaces []*workspace
	active     int
	// swapped is true while onWorkspace has a background workspace temporarily
	// loaded into the live fields; syncViewport is a no-op then so a background
	// turn finishing never repaints the visible (real active) transcript.
	swapped  bool
	nextID   int
	newAgent func() *agent.Agent
	// summarize, when set, produces a short one-shot label for an agent after its
	// first completed turn; nil falls back to a label derived from the first message.
	summarize func(ctx context.Context, prompt string) (string, error)
	// agentsCursor is the highlighted row in the Agents pane; the row past the last
	// agent is the "new agent" action.
	agentsCursor int

	// collapse state for the sidebar panes, toggled with '-'; expanded by default.
	collapsedAgents  bool
	collapsedFiles   bool
	collapsedChanges bool
}

// workspace is one agent's conversation and its UI state. The model's matching
// fields mirror the active workspace; stash/unstash move state between the two.
type workspace struct {
	id       int
	label    string
	cwd      string
	worktree bool
	labeled  bool // an auto-label has been requested/applied

	agent  *agent.Agent
	busy   bool
	events chan eventMsg
	cancel context.CancelFunc
	unread bool // background progress since last viewed

	showHome    bool
	git         gitInfo
	gitReady    bool
	tree        *widgets.Tree
	changes     *widgets.Tree
	finder      *widgets.FuzzyPicker
	finderBase  string
	lines       []entry
	history     []string
	histPos     int
	outputs     []toolOutput
	codeBlocks  []codeBlock
	streamText  string
	streamThink string
	pendingTool string
	pendingArgs string
}

// focusPane identifies which pane currently has keyboard focus. Tab cycles
// through them; paneChat routes typing to the input and scrolls the transcript,
// while paneTree/paneChanges drive the sidebar.
type focusPane int

const (
	paneChat focusPane = iota
	paneTree
	paneChanges
	paneAgents
)

// pickerKind identifies which setting an open ListPicker edits.
type pickerKind int

const (
	pickerNone pickerKind = iota
	pickerModel
	pickerThink
	pickerResume
	pickerNewAgent
	pickerCloseAgent
)

// inputHeight is the fixed number of visible rows in the input box; longer
// input scrolls within it rather than growing the box.
const inputHeight = 3

// footerHeight is the fixed height of the footer pane: a notice line above a
// bordered box holding the status line and the input box (border + status +
// inputHeight).
const footerHeight = 1 + 2 + 1 + inputHeight

// newInput builds a word-wrapping, multi-line input box.
func newInput(width int) textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.Prompt = ""
	ta.Placeholder = ""
	s := ta.Styles()
	s.Focused.CursorLine = lipgloss.NewStyle()
	s.Blurred.CursorLine = lipgloss.NewStyle()
	ta.SetStyles(s)
	ta.SetPromptFunc(2, promptFunc("› ", stylePrompt))
	ta.SetHeight(inputHeight)
	ta.SetWidth(width)
	// Command+←/→ (super) jump words, alongside the default alt/emacs bindings.
	ta.KeyMap.WordBackward.SetKeys("alt+left", "alt+b", "super+left")
	ta.KeyMap.WordForward.SetKeys("alt+right", "alt+f", "super+right")
	ta.Focus()
	return ta
}

// promptFunc builds a textarea prompt: label (styled) on the first line, blank
// continuation on the rest.
func promptFunc(label string, style lipgloss.Style) func(textarea.PromptInfo) string {
	return func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return style.Render(label)
		}
		return "  "
	}
}

// inputView renders the input box, switching to the inline-bash affordance — a
// red `$` prompt and red text — while the buffer starts with `!`. The restyle
// is applied to the throwaway render copy so it always tracks the current buffer.
func (m model) inputView() string {
	if !strings.HasPrefix(strings.TrimSpace(m.input.Value()), "!") {
		return m.input.View()
	}
	s := m.input.Styles()
	s.Focused.Text = styleBashInput
	s.Blurred.Text = styleBashInput
	m.input.SetStyles(s)
	m.input.SetPromptFunc(2, promptFunc("$ ", styleBashPrompt))
	return m.input.View()
}

// New builds the Bubbletea program. a is the default (initial) agent; newAgent
// builds a fresh agent for each spawned workspace (same provider/tools/config);
// summarize is an optional one-shot auto-labeler (nil ⇒ derive labels locally);
// approver is the shared human-in-the-loop hook for judge-error approvals (nil ⇒
// no interactive approval).
func New(a *agent.Agent, newAgent func() *agent.Agent, summarize func(context.Context, string) (string, error), login *LoginHook, approver *Approver) *tea.Program {
	cwd, _ := os.Getwd()
	ws := &workspace{id: 0, cwd: cwd, agent: a, showHome: true}
	m := model{
		agent: a, login: login, md: newRenderer(80), input: newInput(80),
		mouse: mouseEnabled(), showHome: true, tip: pickTip(),
		workspaces: []*workspace{ws}, active: 0, nextID: 1,
		newAgent: newAgent, summarize: summarize, approver: approver,
	}
	return tea.NewProgram(m)
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{gatherGitCmd, orbitTick(m.orbitEpoch)}
	if c := listenApproval(m.approver); c != nil {
		cmds = append(cmds, c)
	}
	return tea.Batch(cmds...)
}

func scrollKeymap() viewport.KeyMap {
	return viewport.KeyMap{
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		PageDown:     key.NewBinding(key.WithKeys("pgdown")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
		Up:           key.NewBinding(key.WithKeys("up")),
		Down:         key.NewBinding(key.WithKeys("down")),
	}
}

// pushText appends a plain, non-clickable transcript block.
func (m *model) pushText(s string) {
	m.lines = append(m.lines, entry{text: s})
}

func (m model) transcript() string {
	parts := make([]string, len(m.lines))
	for i, e := range m.lines {
		parts[i] = e.text
	}
	body := strings.Join(parts, "\n")
	if live := m.liveRegion(); live != "" {
		body += "\n" + live
	}
	return body
}

func (m *model) syncViewport() {
	if !m.ready || m.swapped {
		return
	}
	atBottom := m.vp.AtBottom()
	m.vp.SetContent(m.transcript())
	if atBottom {
		m.vp.GotoBottom()
	}
}
