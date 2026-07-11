package widgets

import (
	"strings"
	"testing"
)

func TestSanitizeDropsControlCharacters(t *testing.T) {
	payload := "\x1b]8;;file:///etc/passwd\x1b\\click\x1b]8;;\x1b\\" +
		"\x1b[2J\x1b[H\x1b]0;PWNED\x07\x1b[31m\rvisible\x7f\x9btext"

	got := Sanitize(payload)

	for _, r := range got {
		if r < 0x20 && r != '\n' && r != '\t' {
			t.Fatalf("C0 control byte %q survived Sanitize: %q", r, got)
		}
		if r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("control byte %q survived Sanitize: %q", r, got)
		}
	}
	if !strings.Contains(got, "visible") || !strings.Contains(got, "text") {
		t.Fatalf("Sanitize dropped visible text: %q", got)
	}
}

func TestSanitizeKeepsNewlinesAndTabs(t *testing.T) {
	got := Sanitize("a\tb\nc")
	if got != "a\tb\nc" {
		t.Fatalf("Sanitize altered newlines/tabs: %q", got)
	}
}

func TestModalSanitizesTitleAndContent(t *testing.T) {
	m := NewModal("\x1b]0;PWNED\x07title", "\x1b[2Jbody\x1b]8;;evil\x1b\\", ModalStyle{})
	m.SetSize(80, 24)
	if strings.ContainsRune(m.View(), 0x1b) {
		t.Fatalf("ESC byte survived modal render:\n%q", m.View())
	}
}
