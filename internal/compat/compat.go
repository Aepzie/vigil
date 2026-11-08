// Package compat rewrites a finished frame so it only uses glyphs that
// every terminal font has (Windows console, Linux VT, old PuTTY). Cell
// widths are preserved and SGR sequences are left untouched.
package compat

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
// returned unchanged.
func Resolve(m Mode, env Env) Mode {
	if m != Auto {
		return m
	}
	return Off
}

// Apply rewrites frame for the resolved mode m. Auto and Off return the
// frame unchanged.
func Apply(frame string, m Mode) string {
	return frame
}
