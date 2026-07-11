package widgets

import "strings"

// Sanitize drops C0/C1 control characters and DEL from untrusted text so it
// cannot emit terminal escape sequences (cursor moves, screen clears, OSC
// hyperlinks, title sets) when rendered. Newlines and tabs are kept.
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			return -1
		default:
			return r
		}
	}, s)
}
