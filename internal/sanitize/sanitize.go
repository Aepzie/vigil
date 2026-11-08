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
//
// Characters whose rendered width differs between terminals are dropped as
// well: the line and paragraph separators U+2028/U+2029, the Hangul fillers
// U+115F, U+1160, U+3164 and U+FFA0, and combining marks that do not follow
// a visible base character (at the start, or after a space or newline).
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
	afterBase := false
	for _, r := range s {
		switch {
		case r == '\n' && keepNewlines:
			b.WriteRune('\n')
			afterBase = false
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(' ')
			afterBase = false
		case r == utf8.RuneError:
			b.WriteRune('�')
			afterBase = true
		case isDropped(r):
		case isCombiningMark(r):
			if afterBase {
				b.WriteRune(r)
			}
		default:
			b.WriteRune(r)
			afterBase = !unicode.IsSpace(r)
		}
	}
	return b.String()
}

// isDropped reports whether r is never rendered: control, format (bidi
// overrides, zero-width characters), private use, line/paragraph
// separators and Hangul fillers.
func isDropped(r rune) bool {
	switch r {
	case '\u115f', '\u1160', '\u3164', '\uffa0':
		return true
	}
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Co, unicode.Zl, unicode.Zp)
}

func isCombiningMark(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me)
}
