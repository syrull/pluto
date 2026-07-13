package tui

import (
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// editorDoneMsg is delivered when the external editor exits.
type editorDoneMsg struct {
	path string
	err  error
}

// editorCommand builds the command to open path in the user's editor, honoring
// $VISUAL then $EDITOR (which may include flags, e.g. "code -w").
func editorCommand(path string) (*exec.Cmd, bool) {
	ed := strings.TrimSpace(os.Getenv("VISUAL"))
	if ed == "" {
		ed = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if ed == "" || path == "" {
		return nil, false
	}
	fields := strings.Fields(ed)
	return exec.Command(fields[0], append(fields[1:], path)...), true
}

// editorAvailable reports whether an editor is configured via $VISUAL/$EDITOR.
func editorAvailable() bool {
	return strings.TrimSpace(os.Getenv("VISUAL")) != "" || strings.TrimSpace(os.Getenv("EDITOR")) != ""
}

// openInEditor suspends the TUI and opens path in the configured editor,
// returning nil when no editor or path is available.
func openInEditor(path string) tea.Cmd {
	cmd, ok := editorCommand(path)
	if !ok {
		return nil
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorDoneMsg{path: path, err: err}
	})
}
