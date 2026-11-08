package widgets

import (
	"github.com/CraguS/vigil/internal/theme"
)

// Align controls horizontal alignment of table cells.
type Align int

const (
	AlignLeft Align = iota
	AlignRightCol
	AlignCenterCol
)

// Column describes one table column.
type Column struct {
	Title string
	// Width fixes the column width; 0 makes the column flexible.
	Width int
	// Min is the minimum width of a flexible column.
	Min int
	// Flex weights how extra space is shared among flexible columns
	// (0 is treated as 1).
	Flex  int
	Align Align
}

// TableOpts configures Table.
type TableOpts struct {
	Width   int
	Height  int
	Columns []Column
	// Rows hold cell strings. Cells may contain SGR sequences; they are
	// measured with ansi.StringWidth and truncated with "…".
	Rows [][]string
	// Selected is the highlighted row index, -1 for none. The selected row
	// gets the Surface2 background and bold text across its full width.
	Selected int
	// Offset is the first visible row; the table also scrolls on its own
	// to keep Selected visible.
	Offset int
	// Empty is shown centered when there are no rows.
	Empty string
}

// Table renders a header (dim, bold) and rows into exactly Width×Height
// cells. Columns are separated by two spaces; flexible columns absorb the
// remaining width and columns are dropped from the right when even their
// minimum widths do not fit.
func Table(t *theme.Theme, o TableOpts) string {
	return Blank(o.Width, o.Height)
}

// PanelOpts configures Panel.
type PanelOpts struct {
	Width   int
	Height  int
	Title   string
	Info    string
	Focused bool
	// Body is fitted into the inner area (Width-4)×(Height-2): one column
	// of padding inside each vertical border.
	Body string
}

// Panel draws a rounded box (╭╮╰╯) with the title embedded in the top
// border on the left and the info text on the right. The border is
// violet when focused. Output is exactly Width×Height.
func Panel(t *theme.Theme, o PanelOpts) string {
	return Blank(o.Width, o.Height)
}

// PanelInner returns the body size available inside a panel of w×h.
func PanelInner(w, h int) (int, int) {
	return max0(w - 4), max0(h - 2)
}

// Badge renders text as a bold black-on-color pill for the tone.
func Badge(t *theme.Theme, text string, tone theme.Tone) string {
	return " " + text + " "
}

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
