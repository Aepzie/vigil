// Package compat rewrites a finished frame so it only uses glyphs that
// every terminal font has (Windows console, Linux VT, old PuTTY). Cell
// widths are preserved and SGR sequences are left untouched.
package compat

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Mode is the glyph compatibility level. The F2 key cycles
// Auto → Blocks → ASCII → Off → Auto.
type Mode int

const (
	// Auto picks Blocks, ASCII or Off from the terminal environment.
	Auto Mode = iota
	// Blocks replaces braille, eighth blocks and rounded corners with
	// full/half blocks, shades and square box corners.
	Blocks
	// ASCII replaces every non-ASCII glyph with printable ASCII.
	ASCII
	// Off renders the full glyph set unchanged.
	Off
)

// String returns the mode name shown in the UI: auto, blocks, ascii, off.
func (m Mode) String() string {
	switch m {
	case Blocks:
		return "blocks"
	case ASCII:
		return "ascii"
	case Off:
		return "off"
	default:
		return "auto"
	}
}

// Next returns the next mode in the F2 cycle.
func (m Mode) Next() Mode {
	return (m + 1) % 4
}

// Parse converts a mode name back to a Mode; unknown names give Auto.
func Parse(s string) Mode {
	for _, m := range []Mode{Auto, Blocks, ASCII, Off} {
		if m.String() == s {
			return m
		}
	}
	return Auto
}

// Env describes the client terminal for Auto resolution.
type Env struct {
	Term        string
	ColorTerm   string
	TermProgram string
	// LCAll / LCCtype / Lang are the locale variables; non-UTF-8 locales
	// force ASCII.
	LCAll   string
	LCCtype string
	Lang    string
	// WindowsConsole is true for a local legacy Windows console host.
	WindowsConsole bool
}

// Resolve turns Auto into a concrete mode for env; other modes are
// returned unchanged. Auto resolves to ASCII for a non-UTF-8 locale
// (LC_ALL, else LC_CTYPE, else LANG; all empty counts as UTF-8) or a
// dumb/vt100/vt220 terminal, to Blocks for the Linux VT, a legacy Windows
// console, PuTTY and cons25, and to Off otherwise.
func Resolve(m Mode, env Env) Mode {
	if m != Auto {
		return m
	}
	term := strings.ToLower(strings.TrimSpace(env.Term))
	if !utf8Locale(env) {
		return ASCII
	}
	switch term {
	case "dumb", "vt100", "vt220":
		return ASCII
	case "linux":
		return Blocks
	}
	if env.WindowsConsole || strings.HasPrefix(term, "putty") || strings.HasPrefix(term, "cons25") {
		return Blocks
	}
	return Off
}

// utf8Locale reports whether the effective locale uses UTF-8.
func utf8Locale(env Env) bool {
	locale := env.LCAll
	if locale == "" {
		locale = env.LCCtype
	}
	if locale == "" {
		locale = env.Lang
	}
	if locale == "" {
		return true
	}
	normalized := strings.ReplaceAll(strings.ToLower(locale), "-", "")
	return strings.Contains(normalized, "utf8")
}

// Apply rewrites frame for the resolved mode m. Auto and Off return the
// frame unchanged. Escape sequences are copied verbatim and every
// replacement has the display width of the glyph it replaces.
func Apply(frame string, m Mode) string {
	switch m {
	case Blocks:
		return rewrite(frame, blocksTable, false)
	case ASCII:
		return rewrite(frame, asciiTable, true)
	default:
		return frame
	}
}

// rewrite replaces mapped glyphs outside escape sequences. In strict
// mode unmapped non-ASCII clusters become '?' per cell.
func rewrite(frame string, table map[rune]string, strict bool) string {
	first := firstNonASCII(frame)
	if first < 0 {
		return frame
	}
	var b strings.Builder
	b.Grow(len(frame))
	b.WriteString(frame[:first])
	for i := first; i < len(frame); {
		c := frame[i]
		if c == ansi.ESC {
			end := skipEscape(frame, i)
			b.WriteString(frame[i:end])
			i = end
			continue
		}
		if c < utf8.RuneSelf {
			b.WriteByte(c)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(frame[i:])
		if !extendedAt(frame, i+size) {
			if repl, ok := table[r]; ok {
				b.WriteString(repl)
				i += size
				continue
			}
			if !strict {
				b.WriteString(frame[i : i+size])
				i += size
				continue
			}
		}
		cluster, width := ansi.FirstGraphemeCluster(frame[i:], ansi.GraphemeWidth)
		if cluster == "" {
			cluster, width = frame[i:i+size], 1
		}
		b.WriteString(replaceCluster(cluster, width, table, strict))
		i += len(cluster)
	}
	return b.String()
}

// replaceCluster handles a multi-rune grapheme cluster or an unmapped
// rune in strict mode.
func replaceCluster(cluster string, width int, table map[rune]string, strict bool) string {
	r, _ := utf8.DecodeRuneInString(cluster)
	if repl, ok := table[r]; ok {
		return fitWidth(repl, width)
	}
	if strict {
		return strings.Repeat("?", width)
	}
	return cluster
}

// extendedAt reports whether the rune starting at s[i] continues the
// previous grapheme cluster (combining marks, joiners, variation
// selectors, emoji modifiers and tags).
func extendedAt(s string, i int) bool {
	if i >= len(s) || s[i] < utf8.RuneSelf {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	switch {
	case r == 0x200D, r >= 0xFE00 && r <= 0xFE0F, r >= 0x1F3FB && r <= 0x1F3FF, r >= 0xE0020 && r <= 0xE007F:
		return true
	default:
		return unicode.In(r, unicode.Mn, unicode.Me, unicode.Mc)
	}
}

func firstNonASCII(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return i
		}
	}
	return -1
}

// skipEscape returns the index just past the escape sequence at s[i].
func skipEscape(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return len(s)
	}
	switch s[j] {
	case '[':
		for j++; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j + 1
			}
		}
		return len(s)
	case ']', 'P', '_', '^', 'X':
		for j++; j < len(s); j++ {
			if s[j] == ansi.BEL {
				return j + 1
			}
			if s[j] == ansi.ESC && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
		return len(s)
	default:
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
			j++
		}
		return min(j+1, len(s))
	}
}

// fitWidth pads or cuts repl to exactly w cells.
func fitWidth(repl string, w int) string {
	rw := ansi.StringWidth(repl)
	switch {
	case rw == w:
		return repl
	case rw < w:
		return repl + strings.Repeat(" ", w-rw)
	default:
		return ansi.Truncate(repl, w, "")
	}
}
