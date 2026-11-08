package widgets

import (
	"math"

	"github.com/CraguS/vigil/internal/theme"
)

const (
	barRune     = '━'
	barHalfRune = '╸'
	// sparkDarken is how far the lowest sparkline level fades to black.
	sparkDarken = 0.45
)

// Sparkline renders the last width values as a one-line ▁..█ sparkline in
// color c. max ≤ 0 means auto-scale to the largest value. Output is exactly
// width cells. Lower bars are drawn slightly darker than taller ones;
// NaN values and the cells left of the oldest value are blank.
func Sparkline(values []float64, width int, max float64, c theme.RGB) string {
	width = max0(width)
	if width == 0 {
		return ""
	}
	start := len(values) - width
	top := max
	if !(top > 0) || math.IsInf(top, 1) {
		top = 0
		for _, v := range values[maxInt(start, 0):] {
			top = math.Max(top, positive(v))
		}
	}
	dark := c.Darken(sparkDarken)
	p := newPainter(width * 6)
	for col := range width {
		idx := start + col
		if idx < 0 || math.IsNaN(values[idx]) {
			p.space()
			continue
		}
		level := 0
		if top > 0 {
			level = int(math.Round(clamp01(positive(values[idx])/top) * 7))
		}
		p.fgCell(lowerBlocks[level+1], dark.Lerp(c, float64(level)/7))
	}
	return p.String()
}

// Progress renders a one-line bar of width cells: frac (0..1) filled with
// fill color on a dark track. The fill has half-cell resolution: a heavy
// left half line ╸ marks an odd half at the edge.
func Progress(t *theme.Theme, width int, frac float64, fill theme.RGB) string {
	width = max0(width)
	if width == 0 {
		return ""
	}
	halves := int(math.Round(clamp01(frac) * float64(width*2)))
	p := newPainter(width * 4)
	for col := range width {
		switch {
		case (col+1)*2 <= halves:
			p.fgCell(barRune, fill)
		case col*2+1 == halves:
			p.fgCell(barHalfRune, fill)
		default:
			p.fgCell(barRune, t.P.Border)
		}
	}
	return p.String()
}

// Segment is one part of a StackedBar.
type Segment struct {
	Value float64
	Color theme.RGB
}

// StackedBar renders a one-line bar of width cells where segments occupy
// proportional widths of total (total ≤ 0 means sum of segments); the
// remainder is drawn as dark track. Segment edges are placed by rounding
// cumulative sums so the parts never drift past the bar.
func StackedBar(t *theme.Theme, width int, total float64, segs []Segment) string {
	width = max0(width)
	if width == 0 {
		return ""
	}
	sum := 0.0
	for _, s := range segs {
		sum += positive(s.Value)
	}
	if !(total > 0) || math.IsInf(total, 1) {
		total = sum
	}
	p := newPainter(width * 4)
	col, cum := 0, 0.0
	if total > 0 {
		for _, s := range segs {
			cum += positive(s.Value)
			end := min(int(math.Round(cum/total*float64(width))), width)
			for ; col < end; col++ {
				p.fgCell(barRune, s.Color)
			}
		}
	}
	for ; col < width; col++ {
		p.fgCell(barRune, t.P.Border)
	}
	return p.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
