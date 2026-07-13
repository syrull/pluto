package tui

import (
	"strings"
	"testing"
)

func TestHighlightSourceColorsKnownLanguage(t *testing.T) {
	src := "package main\n\nfunc main() {}\n"
	got := highlightSource(src, "internal/tui/main.go")
	if got == src {
		t.Fatal("expected .go content to be highlighted")
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected SGR codes in highlighted output, got %q", got)
	}
	if stripSGR(got) != src {
		t.Fatalf("highlighting must not change the underlying text: %q vs %q", stripSGR(got), src)
	}
}

func TestHighlightSourcePlainForUnknownLanguage(t *testing.T) {
	src := "arbitrary bytes with no language"
	if got := highlightSource(src, "data.unknownext"); got != src {
		t.Fatalf("unknown extension should stay plain, got %q", got)
	}
}

func TestHighlightSourcePlainWithoutPath(t *testing.T) {
	if got := highlightSource("x := 1", ""); got != "x := 1" {
		t.Fatalf("empty path should stay plain, got %q", got)
	}
}

// stripSGR removes SGR escape sequences so the underlying text can be compared.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
