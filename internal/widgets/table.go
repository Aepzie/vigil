package widgets

import (
	"strings"

	"github.com/Aepzie/vigil/internal/theme"
	"github.com/charmbracelet/x/ansi"
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
	Flex int
	// Max caps the width a flexible column grows to; 0 means unlimited.
	// Space a capped column cannot take goes to the other flexible
	// columns, or stays blank at the right when every one is capped.
	Max   int
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

// columnGap is the number of blank cells between table columns.
const columnGap = 2

// Table renders a header (dim, bold) and rows into exactly Width×Height
// cells. Columns are separated by two spaces; flexible columns absorb the
// remaining width and columns are dropped from the right when even their
// minimum widths do not fit.
func Table(t *theme.Theme, o TableOpts) string {
	w, h := max0(o.Width), max0(o.Height)
	if w == 0 || h == 0 {
		return ""
	}
	widths := ColumnWidths(o.Columns, w)
	lines := make([]string, 0, h)
	header := make([]string, len(o.Columns))
	for i, c := range o.Columns {
		header[i] = c.Title
	}
	lines = append(lines, ClipLine(t.TableHeader.Render(ansi.Strip(joinCells(o.Columns, widths, header, w))), w))
	body := h - 1
	if body == 0 {
		return lines[0]
	}
	if len(o.Rows) == 0 {
		lines = append(lines, emptyBody(t, o.Empty, w, body)...)
		return strings.Join(lines, "\n")
	}
	offset := ScrollOffset(len(o.Rows), body, o.Selected, o.Offset)
	textFG := t.P.Text.FG()
	selected := t.P.Surface2.BG() + t.P.Text.FG() + theme.Bold
	for i := range body {
		idx := offset + i
		if idx >= len(o.Rows) {
			lines = append(lines, strings.Repeat(" ", w))
			continue
		}
		line := joinCells(o.Columns, widths, o.Rows[idx], w)
		style := textFG
		if idx == o.Selected {
			style = selected
		}
		lines = append(lines, styleLine(line, style))
	}
	return strings.Join(lines, "\n")
}

// ScrollOffset returns the first visible row for a list of n rows shown
// in a window of visible rows, starting from offset and scrolled the
// minimal amount that keeps selected (if in range) on screen.
func ScrollOffset(n, visible, selected, offset int) int {
	if visible <= 0 || n <= visible {
		return 0
	}
	offset = min(max(offset, 0), n-visible)
	if selected < 0 || selected >= n {
		return offset
	}
	if selected < offset {
		return selected
	}
	if selected >= offset+visible {
		return selected - visible + 1
	}
	return offset
}

// ColumnWidths lays out columns into total cells with columnGap between
// them. It keeps the longest prefix of columns whose fixed widths and
// flexible minimums fit, then shares the leftover space among the kept
// flexible columns by Flex weight, never growing a column beyond its Max.
// Dropped columns get width 0. If not even the first column fits, it is
// narrowed to total. The widths may sum to less than total when every
// flexible column is capped.
func ColumnWidths(cols []Column, total int) []int {
	widths := make([]int, len(cols))
	if len(cols) == 0 || total <= 0 {
		return widths
	}
	base := func(c Column) int {
		if c.Width > 0 {
			return c.Width
		}
		if c.Min > 0 {
			return c.Min
		}
		return max(ansi.StringWidth(c.Title), 1)
	}
	used, kept := 0, 0
	for i, c := range cols {
		need := base(c)
		if i > 0 {
			need += columnGap
		}
		if used+need > total {
			break
		}
		used += need
		widths[i] = base(c)
		kept++
	}
	if kept == 0 {
		widths[0] = total
		return widths
	}
	weights := make([]int, kept)
	for i := range kept {
		if cols[i].Width > 0 {
			continue
		}
		weights[i] = max(cols[i].Flex, 1)
	}
	growFlexible(cols[:kept], widths[:kept], weights, total-used)
	return widths
}

// growFlexible shares extra cells among the columns with a positive
// weight. Columns whose proportional share would exceed their Max are
// set to Max and taken out of the sharing, which then repeats with the
// rest; the final shares are proportional with the rounding remainder
// handed out left to right.
func growFlexible(cols []Column, widths, weights []int, extra int) {
	room := func(i int) int {
		if cols[i].Max <= 0 {
			return -1
		}
		return max(cols[i].Max-widths[i], 0)
	}
	for extra > 0 {
		sum := 0
		for _, wt := range weights {
			sum += wt
		}
		if sum == 0 {
			return
		}
		capped := false
		for i, wt := range weights {
			if wt == 0 {
				continue
			}
			if r := room(i); r >= 0 && extra*wt/sum >= r {
				widths[i] += r
				extra -= r
				weights[i] = 0
				capped = true
			}
		}
		if capped {
			continue
		}
		given := 0
		for i, wt := range weights {
			share := extra * wt / sum
			widths[i] += share
			given += share
		}
		for i := 0; given < extra; i = (i + 1) % len(weights) {
			if weights[i] > 0 {
				widths[i]++
				given++
			}
		}
		return
	}
}

// joinCells aligns and truncates each cell to its column width and joins
// them with the column gap, padding the line to w cells.
func joinCells(cols []Column, widths []int, cells []string, w int) string {
	var sb strings.Builder
	first := true
	for i, c := range cols {
		if widths[i] <= 0 {
			continue
		}
		if !first {
			sb.WriteString(strings.Repeat(" ", columnGap))
		}
		first = false
		text := ""
		if i < len(cells) {
			text = singleLine(cells[i])
		}
		sb.WriteString(alignCell(text, widths[i], c.Align))
	}
	return ClipLine(sb.String(), w)
}

func alignCell(text string, w int, a Align) string {
	if ansi.StringWidth(text) > w {
		return FitLine(text, w)
	}
	switch a {
	case AlignRightCol:
		return AlignRight(text, w)
	case AlignCenterCol:
		return Center(text, w)
	default:
		return FitLine(text, w)
	}
}

// singleLine replaces line breaks so a cell never spans rows.
func singleLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
}

// styleLine applies the SGR prefix style to line and re-applies it after
// every reset inside the line so embedded cell styling cannot break a
// row-wide background.
func styleLine(line, style string) string {
	if strings.Contains(line, "\x1b[") {
		line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+style)
		line = strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+style)
	}
	return style + line + theme.Reset
}

func emptyBody(t *theme.Theme, text string, w, h int) []string {
	lines := make([]string, h)
	mid := (h - 1) / 2
	for i := range lines {
		if i == mid && text != "" {
			lines[i] = Center(t.Faint.Render(ansi.Truncate(singleLine(text), w, "…")), w)
			continue
		}
		lines[i] = strings.Repeat(" ", w)
	}
	return lines
}
