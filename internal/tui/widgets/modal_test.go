package widgets

import (
	"strings"
	"testing"
)

func TestModalHighlightRunsOnSanitizedContentAndKeepsRawCopy(t *testing.T) {
	var seen string
	m := NewModal("title", "a\x1b[31mred\x1b[0m", ModalStyle{})
	m.Highlight(func(s string) string {
		seen = s
		return "\x1b[32m" + s + "\x1b[0m"
	})

	if strings.ContainsRune(seen, 0x1b) {
		t.Fatalf("highlight must run on sanitized text with no ESC, got %q", seen)
	}
	if want := "a[31mred[0m"; m.Content() != want {
		t.Fatalf("Content() = %q, want the raw sanitized text %q", m.Content(), want)
	}
}

func TestModalContentUnaffectedWithoutHighlight(t *testing.T) {
	m := NewModal("title", "plain body", ModalStyle{})
	if m.Content() != "plain body" {
		t.Fatalf("Content() = %q, want %q", m.Content(), "plain body")
	}
}

func TestModalEditableHint(t *testing.T) {
	m := NewModal("title", "body", ModalStyle{})
	m.SetSize(40, 12)
	if strings.Contains(m.View(), "ctrl+g") {
		t.Fatal("edit hint should be absent by default")
	}
	m.SetEditable(true)
	if !strings.Contains(m.View(), "ctrl+g") {
		t.Fatalf("edit hint should appear once editable:\n%s", m.View())
	}
}
