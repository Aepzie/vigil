package widgets

import (
	"strings"
	"time"

	"github.com/CraguS/vigil/internal/theme"
)

// Series is one data series for charts. Values are ordered oldest to
// newest; the newest value is drawn at the right edge.
type Series struct {
	Name   string
	Values []float64
	Color  theme.RGB
}

// ChartOpts configures AreaChart and LineChart.
type ChartOpts struct {
	Width  int
	Height int
	// Series are stacked bottom to top for AreaChart and overlaid for
	// LineChart.
	Series []Series
	// Max fixes the top of the Y scale. Zero means auto: the "nice"
	// (1/2/5 × 10^n) ceiling of the largest stacked value.
	Max float64
	// Step is the sampling interval between values, used for X labels
	// such as "-60s", "-30s", "now".
	Step time.Duration
	// YLabel formats Y axis values; nil uses format.Number.
	YLabel func(float64) string
	// HideAxes drops the Y and X axis (used for compact panels).
	HideAxes bool
}

// AreaChart renders stacked bands of ▁▂▃▄▅▆▇█ with 1/8-cell vertical
// resolution, a light-at-top to darker-at-base gradient per band, dotted
// ┈ grid lines, a nice-scaled Y axis and a time X axis. Output is exactly
// Width×Height cells.
func AreaChart(t *theme.Theme, o ChartOpts) string {
	return Blank(o.Width, o.Height)
}

// LineChart renders series as braille dot lines (2×4 dots per cell) with
// the same axes and grid as AreaChart. Output is exactly Width×Height.
func LineChart(t *theme.Theme, o ChartOpts) string {
	return Blank(o.Width, o.Height)
}

// NiceScale returns a "nice" axis maximum ≥ maxValue and the tick step,
// using 1/2/5 × 10^n steps and roughly the requested number of ticks.
func NiceScale(maxValue float64, ticks int) (niceMax, step float64) {
	return maxValue, maxValue
}

// Sparkline renders the last width values as a one-line ▁..█ sparkline in
// color c. max ≤ 0 means auto-scale to the largest value. Output is exactly
// width cells.
func Sparkline(values []float64, width int, max float64, c theme.RGB) string {
	return strings.Repeat(" ", max0(width))
}

// Progress renders a one-line bar of width cells: frac (0..1) filled with
// fill color on a dark track.
func Progress(t *theme.Theme, width int, frac float64, fill theme.RGB) string {
	return strings.Repeat(" ", max0(width))
}

// Segment is one part of a StackedBar.
type Segment struct {
	Value float64
	Color theme.RGB
}

// StackedBar renders a one-line bar of width cells where segments occupy
// proportional widths of total (total ≤ 0 means sum of segments); the
// remainder is drawn as dark track.
func StackedBar(t *theme.Theme, width int, total float64, segs []Segment) string {
	return strings.Repeat(" ", max0(width))
}

// BigNumber renders text as 3-line tall 7-segment digits drawn with heavy
// box characters (┏━┓ ┃ ┃ ┗━┛). Supported runes: 0-9 . , - % k K M G T B
// / s and space; others render blank. Output lines all have width
// BigNumberWidth(text).
func BigNumber(text string, c theme.RGB) string {
	return Blank(BigNumberWidth(text), 3)
}

// BigNumberWidth is the cell width BigNumber will produce for text.
func BigNumberWidth(text string) int {
	return len([]rune(text)) * 3
}

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

func max0(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
