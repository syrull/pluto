package tui

import (
	"bytes"
	"os"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// highlightSource colorizes already-sanitized src using a chroma lexer inferred
// from path. It returns src unchanged when no language can be inferred or on any
// error, so unknown content stays plain rather than mis-highlighted. src must be
// sanitized first: the only escape sequences added are the trusted SGR codes
// chroma emits.
func highlightSource(src, path string) string {
	if path == "" || src == "" {
		return src
	}
	lexer := lexers.Match(path)
	if lexer == nil {
		return src
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, src)
	if err != nil {
		return src
	}
	var buf bytes.Buffer
	if err := formatters.TTY256.Format(&buf, codeStyle(), it); err != nil {
		return src
	}
	return buf.String()
}

// codeStyle picks the chroma theme for modal highlighting, honoring
// PLUTO_CODE_STYLE for parity with PLUTO_MD_STYLE.
func codeStyle() *chroma.Style {
	if name := os.Getenv("PLUTO_CODE_STYLE"); name != "" {
		return styles.Get(name)
	}
	return styles.Get("monokai")
}
