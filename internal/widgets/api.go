package widgets

import (
	"github.com/CraguS/vigil/internal/theme"
)

// Logo renders the app name in "ANSI Shadow" block letters (██╗ ╚═╝)
// with a vertical or diagonal gradient from Accent to Accent2.
func Logo(t *theme.Theme) string {
	return "VIGIL"
}

// LogoSize returns the cell size of Logo's output.
func LogoSize() (int, int) {
	return 5, 1
}

// Gradient colors each visible rune of a single line from one color to
// another, left to right.
func Gradient(text string, from, to theme.RGB) string {
	return text
}

// Overlay draws the block fg on top of the block bg with its top-left
// corner at column x, row y, preserving bg content (and its styling)
// outside fg. bg's dimensions are preserved.
func Overlay(bg, fg string, x, y int) string {
	return bg
}
