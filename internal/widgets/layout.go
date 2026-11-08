// Package widgets contains pure render functions: data in, string out.
// Every block-producing function returns exactly height lines joined by
// "\n", each exactly width terminal cells wide (measured with
// ansi.StringWidth), so callers can compose them without drift.
package widgets

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// FitLine pads or truncates s to exactly w cells. Truncated text ends
// with "…".
func FitLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		s = ansi.Truncate(s, w, "…")
		sw = ansi.StringWidth(s)
	}
	if sw < w {
		return s + strings.Repeat(" ", w-sw)
	}
	return s
}

// ClipLine pads or truncates s to exactly w cells without an ellipsis.
func ClipLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		s = ansi.Truncate(s, w, "")
		sw = ansi.StringWidth(s)
	}
	if sw < w {
		return s + strings.Repeat(" ", w-sw)
	}
	return s
}

// Fit pads or truncates a multi-line block to exactly w×h cells.
func Fit(s string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	out := make([]string, h)
	for i := range h {
		if i < len(lines) {
			out[i] = ClipLine(lines[i], w)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(out, "\n")
}

// Blank returns a w×h block of spaces.
func Blank(w, h int) string {
	return Fit("", w, h)
}

// AlignRight pads s on the left to w cells (truncating if needed).
func AlignRight(s string, w int) string {
	sw := ansi.StringWidth(s)
	if sw >= w {
		return FitLine(s, w)
	}
	return strings.Repeat(" ", w-sw) + s
}

// Center pads s on both sides to w cells (truncating if needed).
func Center(s string, w int) string {
	sw := ansi.StringWidth(s)
	if sw >= w {
		return FitLine(s, w)
	}
	left := (w - sw) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", w-sw-left)
}

// SpreadLine renders left and right text on one line of w cells with the
// gap filled by spaces. If both do not fit, right is dropped first.
func SpreadLine(left, right string, w int) string {
	lw, rw := ansi.StringWidth(left), ansi.StringWidth(right)
	if lw+rw+1 > w {
		return FitLine(left, w)
	}
	return left + strings.Repeat(" ", w-lw-rw) + right
}

// JoinH places equally tall blocks side by side. Blocks are expected to
// already be fitted; shorter blocks are padded with blank lines of their
// first line's width.
func JoinH(blocks ...string) string {
	if len(blocks) == 0 {
		return ""
	}
	split := make([][]string, len(blocks))
	widths := make([]int, len(blocks))
	height := 0
	for i, b := range blocks {
		split[i] = strings.Split(b, "\n")
		if len(split[i]) > 0 {
			widths[i] = ansi.StringWidth(split[i][0])
		}
		height = max(height, len(split[i]))
	}
	var sb strings.Builder
	for row := range height {
		if row > 0 {
			sb.WriteByte('\n')
		}
		for i := range split {
			if row < len(split[i]) {
				sb.WriteString(split[i][row])
			} else {
				sb.WriteString(strings.Repeat(" ", widths[i]))
			}
		}
	}
	return sb.String()
}

// JoinV stacks blocks vertically.
func JoinV(blocks ...string) string {
	return strings.Join(blocks, "\n")
}

// SplitWidths divides total cells into n parts separated by gap cells,
// distributing the remainder to the leftmost parts.
func SplitWidths(total, n, gap int) []int {
	if n <= 0 {
		return nil
	}
	avail := max(total-gap*(n-1), 0)
	out := make([]int, n)
	base, rem := avail/n, avail%n
	for i := range out {
		out[i] = base
		if i < rem {
			out[i]++
		}
	}
	return out
}
