package widgets

import (
	"math"

	"github.com/CraguS/vigil/internal/theme"
)

const (
	brailleBase = 0x2800
	// samplesPerCell is the horizontal resolution of LineChart: one
	// sample per braille dot column.
	samplesPerCell = 2
)

// brailleBits[row][col] is the dot bit for dot row 0..3 (top to bottom)
// and dot column 0..1 within a braille cell.
var brailleBits = [4][2]uint8{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

// LineChart renders series as braille dot lines (2×4 dots per cell) with
// the same axes and grid as AreaChart. Each braille dot column holds one
// sample, so a plot W cells wide shows the newest 2·W samples and the X
// axis labels account for two samples per column. Consecutive samples are
// joined by straight line segments; NaN values break the line. Where
// series share a cell, the later series wins its color. Output is exactly
// Width×Height.
func LineChart(t *theme.Theme, o ChartOpts) string {
	w := max0(o.Width)
	frame := newChartFrame(o, lineMax(o.Series, w*samplesPerCell))
	if frame.plotW < w {
		frame = newChartFrame(o, lineMax(o.Series, frame.plotW*samplesPerCell))
	}
	if frame.plotW <= 0 || frame.plotH <= 0 {
		return frame.render(t, o.Step, samplesPerCell, func(*painter, int) {})
	}
	g := newBrailleGrid(frame.plotW, frame.plotH)
	for i, s := range o.Series {
		g.plotSeries(s.Values, frame.top, i)
	}
	return frame.render(t, o.Step, samplesPerCell, func(p *painter, row int) {
		for c := range frame.plotW {
			idx := row*frame.plotW + c
			mask := g.mask[idx]
			if mask == 0 {
				if frame.gridRow[row] {
					p.fgCell(gridRune, t.P.Border)
					continue
				}
				p.space()
				continue
			}
			p.fgCell(rune(brailleBase+int(mask)), o.Series[g.owner[idx]].Color)
		}
	})
}

// lineMax is the largest finite value among the newest n samples.
func lineMax(series []Series, n int) float64 {
	peak := 0.0
	for _, s := range series {
		start := max(len(s.Values)-n, 0)
		for _, v := range s.Values[start:] {
			peak = max(peak, positive(v))
		}
	}
	return peak
}

// brailleGrid is a cols×rows cell canvas with 2×4 dots per cell.
type brailleGrid struct {
	cols, rows int
	mask       []uint8
	owner      []int
}

func newBrailleGrid(cols, rows int) *brailleGrid {
	return &brailleGrid{
		cols:  cols,
		rows:  rows,
		mask:  make([]uint8, cols*rows),
		owner: make([]int, cols*rows),
	}
}

// set lights the dot at dot column x and dot row y (0 is the bottom),
// ignoring dots outside the canvas.
func (g *brailleGrid) set(x, y, series int) {
	dotsW, dotsH := g.cols*2, g.rows*4
	if x < 0 || x >= dotsW || y < 0 || y >= dotsH {
		return
	}
	fromTop := dotsH - 1 - y
	idx := (fromTop/4)*g.cols + x/2
	g.mask[idx] |= brailleBits[fromTop%4][x%2]
	g.owner[idx] = series
}

// plotSeries draws values right-aligned on the canvas, joining
// consecutive finite samples with Bresenham lines.
func (g *brailleGrid) plotSeries(values []float64, top float64, series int) {
	dotsW, dotsH := g.cols*2, g.rows*4
	start := max(len(values)-dotsW-1, 0)
	scale := float64(dotsH-1) / top
	prevOK := false
	var px, py int
	for i := start; i < len(values); i++ {
		v := values[i]
		x := dotsW - (len(values) - i)
		if math.IsNaN(v) {
			prevOK = false
			continue
		}
		y := int(math.Round(math.Min(positive(v), top) * scale))
		if prevOK {
			g.line(px, py, x, y, series)
		} else {
			g.set(x, y, series)
		}
		px, py, prevOK = x, y, true
	}
}

// line draws a Bresenham line between two dots inclusive.
func (g *brailleGrid) line(x0, y0, x1, y1, series int) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		g.set(x0, y0, series)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
