package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestEditorCommandHonorsVisualThenEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	cmd, ok := editorCommand("/tmp/x.go")
	if !ok || cmd == nil {
		t.Fatal("expected a command when EDITOR is set")
	}
	if cmd.Args[0] != "vim" || cmd.Args[len(cmd.Args)-1] != "/tmp/x.go" {
		t.Fatalf("unexpected args: %v", cmd.Args)
	}

	t.Setenv("VISUAL", "code -w")
	cmd, ok = editorCommand("/tmp/x.go")
	if !ok {
		t.Fatal("VISUAL should take precedence")
	}
	if cmd.Args[0] != "code" || cmd.Args[1] != "-w" || cmd.Args[len(cmd.Args)-1] != "/tmp/x.go" {
		t.Fatalf("flags in $VISUAL should be parsed: %v", cmd.Args)
	}
}

func TestEditorCommandUnavailable(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if _, ok := editorCommand("/tmp/x.go"); ok {
		t.Fatal("no editor configured should report unavailable")
	}
	t.Setenv("EDITOR", "vim")
	if _, ok := editorCommand(""); ok {
		t.Fatal("empty path should report unavailable")
	}
}

func TestOpenInEditorNilWithoutEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if openInEditor("/tmp/x.go") != nil {
		t.Fatal("openInEditor should be nil when no editor is configured")
	}
	t.Setenv("EDITOR", "vim")
	if openInEditor("/tmp/x.go") == nil {
		t.Fatal("openInEditor should return a command when an editor is configured")
	}
}

func TestModalCtrlGOpensEditor(t *testing.T) {
	t.Setenv("EDITOR", "true")
	dir := t.TempDir()
	p := dir + "/x.txt"
	m := &model{width: 80, height: 24}
	m.openFileDiff(p) // sets modal + modalPath
	if m.modal == nil {
		t.Fatal("modal not opened")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+g should return an ExecProcess command when an editor is set")
	}
}
