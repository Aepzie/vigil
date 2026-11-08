// Package sanitize strips terminal control characters and escape sequences
// from untrusted text (user input, data read from remote servers) before it
// is rendered.
package sanitize

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Line returns s as a single printable line: escape sequences are removed,
// tabs and newlines become spaces, all other control and format characters
// are dropped, and invalid UTF-8 is replaced.
func Line(s string) string {
	return clean(s, false)
}

// Lines is like Line but keeps newlines.
func Lines(s string) string {
	return clean(s, true)
}

func clean(s string, keepNewlines bool) string {
	s = ansi.Strip(strings.ToValidUTF8(s, "�"))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' && keepNewlines:
			b.WriteRune('\n')
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(' ')
		case r == utf8.RuneError:
			b.WriteRune('�')
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Co, r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
