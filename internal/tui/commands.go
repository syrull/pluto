package tui

import (
	"strings"

	"github.com/syrull/pluto/internal/tui/widgets"
)

// slashCommands is the registry backing the autocomplete popup. Every entry must
// have a matching case in handleCommand (guarded by TestSlashCommandsMatchDispatch).
var slashCommands = []widgets.Command{
	{Name: "/new", Desc: "start a new conversation"},
	{Name: "/close", Desc: "close the active agent"},
	{Name: "/dash", Desc: "open the dashboard"},
	{Name: "/save", Args: "[name]", Desc: "save the current conversation"},
	{Name: "/resume", Args: "[id|--all]", Desc: "resume a conversation from this folder (--all: any folder)"},
	{Name: "/login", Desc: "authenticate with Anthropic"},
	{Name: "/model", Args: "[name]", Desc: "switch the active model"},
	{Name: "/think", Args: "[level]", Desc: "set the extended-thinking level"},
	{Name: "/auto", Args: "[on|off]", Desc: "toggle auto mode"},
	{Name: "/goal", Args: "[condition|clear]", Desc: "keep working until a condition is met"},
	{Name: "/learn", Args: "[on|off]", Desc: "toggle learn mode — explains Go and the codebase as it works"},
	{Name: "/gh", Desc: "open the GitHub browser"},
	{Name: "/image", Args: "<path>", Desc: "attach an image to your next message"},
}

// backgroundCommands are the slash commands safe to run while the agent is
// working: they act on TUI state or the review gate (thread-safe) without
// touching the running turn's transcript, so they're dispatched instead of
// rejected. Every entry must also appear in slashCommands (guarded by
// TestBackgroundCommandsAreRegistered). All other commands wait until idle.
var backgroundCommands = map[string]bool{
	"/gh":   true, // opens the GitHub browser; never enters the conversation
	"/auto": true, // toggles the judge/review gate, which is concurrency-safe
}

// runsInBackground reports whether the slash-command line may be dispatched
// while the agent is busy rather than deferred until the turn finishes.
func runsInBackground(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	return backgroundCommands[fields[0]]
}

// cmdMenuStyle styles the slash-command autocomplete popup: magenta command
// names tied to the '/' prompt, dim descriptions, and a magenta-bordered box.
func cmdMenuStyle() widgets.ListStyle {
	return widgets.ListStyle{
		Title:    styleHint,
		Selected: stylePickSel,
		Item:     stylePrompt,
		Box:      styleCmdMenuBox,
	}
}

// commandQuery reports the slash-command word being typed and whether the
// autocomplete popup should be open: true only when the buffer's first non-space
// rune is '/' and no space or newline (argument entry) has been typed yet.
func commandQuery(value string) (string, bool) {
	t := strings.TrimLeft(value, " \t")
	if !strings.HasPrefix(t, "/") {
		return "", false
	}
	if strings.ContainsAny(t, " \t\n") {
		return "", false
	}
	return t, true
}

// refreshCommandMenu opens, updates, or closes the autocomplete popup to match
// the current input. It stays closed unless the chat pane is focused and the
// buffer holds a bare slash-command word with at least one matching command.
func (m *model) refreshCommandMenu() {
	if m.focus != paneChat {
		m.cmdMenu = nil
		return
	}
	query, ok := commandQuery(m.input.Value())
	if !ok {
		m.cmdMenu = nil
		return
	}
	if m.cmdMenu == nil {
		m.cmdMenu = widgets.NewCommandMenu(slashCommands, cmdMenuStyle())
	}
	m.cmdMenu.Filter(query)
	if m.cmdMenu.Len() == 0 {
		m.cmdMenu = nil
	}
}

// completeCommand fills the input with the highlighted command (plus a trailing
// space to begin argument entry) and closes the popup without submitting.
func (m *model) completeCommand() {
	cmd, ok := m.cmdMenu.Selected()
	if ok {
		m.input.SetValue(cmd.Name + " ")
		m.input.MoveToEnd()
	}
	m.cmdMenu = nil
}
