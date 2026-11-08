package widgets

import (
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// logoLines spell "VIGIL" in the figlet "ANSI Shadow" font.
var logoLines = [...]string{
	"██╗   ██╗██╗ ██████╗ ██╗██╗     ",
	"██║   ██║██║██╔════╝ ██║██║     ",
	"██║   ██║██║██║  ███╗██║██║     ",
	"╚██╗ ██╔╝██║██║   ██║██║██║     ",
	" ╚████╔╝ ██║╚██████╔╝██║███████╗",
	"  ╚═══╝  ╚═╝ ╚═════╝ ╚═╝╚══════╝",
}

const (
	// logoRowWeight makes the gradient run diagonally: each row shifts the
	// color as much as this many columns.
	logoRowWeight = 2
	// logoShadow darkens the box-drawing "shadow" strokes of the font.
	logoShadow = 0.5
)

// Logo renders the app name in "ANSI Shadow" block letters (██╗ ╚═╝)
// with a diagonal gradient from Accent to Accent2. The solid blocks carry
// the gradient color and the shadow strokes a darker shade of it.
func Logo(t *theme.Theme) string {
	w, h := LogoSize()
	span := float64(w - 1 + (h-1)*logoRowWeight)
	p := newPainter(w * h * 20)
	for row, line := range logoLines {
		if row > 0 {
			p.newline()
		}
		for col, r := range []rune(line) {
			if r == ' ' {
				p.space()
				continue
			}
			c := t.P.Accent.Lerp(t.P.Accent2, float64(col+row*logoRowWeight)/span)
			if r != '█' {
				c = c.Darken(logoShadow)
			}
			p.fgCell(r, c)
		}
	}
	return p.String()
}

// LogoSize returns the cell size of Logo's output.
func LogoSize() (int, int) {
	return ansi.StringWidth(logoLines[0]), len(logoLines)
}

// Gradient colors each visible rune of a single line from one color to
// another, left to right. Existing escape sequences in text are dropped
// and line breaks become spaces.
func Gradient(text string, from, to theme.RGB) string {
	runes := []rune(singleLine(ansi.Strip(text)))
	if len(runes) == 0 {
		return ""
	}
	p := newPainter(len(runes) * 20)
	last := float64(max(len(runes)-1, 1))
	for i, r := range runes {
		if r == ' ' {
			p.b.WriteByte(' ')
			continue
		}
		p.fgCell(r, from.Lerp(to, float64(i)/last))
	}
	return p.String()
}
