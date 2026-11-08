package widgets

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/CraguS/vigil/internal/format"
	"github.com/CraguS/vigil/internal/theme"
	"github.com/charmbracelet/x/ansi"
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

const (
	// minXLabelGap is the minimum distance in columns between X labels.
	minXLabelGap = 10
	// minPlotWidth is the narrowest plot that still gets a Y axis.
	minPlotWidth = 6
	// bandDarken is how far a band fades toward black at its base.
	bandDarken = 0.55
)

// NiceScale returns a "nice" axis maximum ≥ maxValue and the tick step,
// using 1/2/5 × 10^n steps and at most the requested number of ticks.
// Non-positive or non-finite maxima yield the unit scale (1, 1/ticks
// rounded to a nice step).
func NiceScale(maxValue float64, ticks int) (niceMax, step float64) {
	ticks = max(ticks, 1)
	if !(maxValue > 0) || math.IsInf(maxValue, 0) {
		maxValue = 1
	}
	step = niceCeil(maxValue / float64(ticks))
	niceMax = math.Ceil(maxValue/step-1e-9) * step
	if niceMax < step {
		niceMax = step
	}
	return niceMax, step
}

// niceCeil rounds x > 0 up to the nearest 1, 2 or 5 × 10^n.
func niceCeil(x float64) float64 {
	exp := math.Floor(math.Log10(x))
	pow := math.Pow(10, exp)
	f := x / pow
	const tol = 1e-9
	switch {
	case f <= 1+tol:
		return pow
	case f <= 2+tol:
		return 2 * pow
	case f <= 5+tol:
		return 5 * pow
	default:
		return 10 * pow
	}
}

// chartFrame is the shared axis layout of AreaChart and LineChart.
type chartFrame struct {
	width, height int
	gutter        int
	plotW, plotH  int
	showX         bool
	top           float64
	gridRow       []bool
	// yLabels maps a row (plotH is the X axis row) to its label.
	yLabels map[int]string
}

func newChartFrame(o ChartOpts, dataMax float64) chartFrame {
	f := chartFrame{width: max0(o.Width), height: max0(o.Height)}
	f.showX = !o.HideAxes && f.height >= 3
	f.plotH = f.height
	if f.showX {
		f.plotH--
	}
	ticks := min(max(f.plotH/3, 1), 5)
	var step float64
	if o.Max > 0 && finite(o.Max) {
		f.top = o.Max
		_, step = NiceScale(o.Max, ticks)
	} else {
		f.top, step = NiceScale(dataMax, ticks)
	}
	f.gridRow = make([]bool, f.plotH)
	f.yLabels = make(map[int]string)
	label := yLabeler(o.YLabel, step)
	labelW := 0
	addTick := func(v float64, row int) {
		if row < 0 || row > f.plotH || (row == f.plotH && !f.showX) {
			return
		}
		if _, taken := f.yLabels[row]; taken {
			return
		}
		f.yLabels[row] = label(v)
		labelW = max(labelW, ansi.StringWidth(f.yLabels[row]))
		if row < f.plotH && v > 0 {
			f.gridRow[row] = true
		}
	}
	lastRow := f.plotH + 1
	for k := 0; k <= 50; k++ {
		v := float64(k) * step
		if v > f.top*(1+1e-9) {
			break
		}
		row := f.valueRow(v)
		addTick(v, row)
		lastRow = row
	}
	if lastRow >= 2 {
		addTick(f.top, 0)
	}
	f.plotW = f.width
	if !o.HideAxes && labelW > 0 && f.width-labelW-1 >= minPlotWidth {
		f.gutter = labelW + 1
		f.plotW = f.width - f.gutter
	} else {
		f.yLabels = nil
	}
	return f
}

// valueRow is the plot row whose top edge sits at value v.
func (f chartFrame) valueRow(v float64) int {
	return f.plotH - int(math.Round(v/f.top*float64(f.plotH)))
}

// yLabeler returns the Y label formatter. The default keeps enough
// decimals for fractional steps, which format.Number would round away.
func yLabeler(custom func(float64) string, step float64) func(float64) string {
	if custom != nil {
		return custom
	}
	if step >= 1 {
		return format.Number
	}
	decimals := int(math.Ceil(-math.Log10(step) - 1e-9))
	return func(v float64) string { return strconv.FormatFloat(v, 'f', decimals, 64) }
}

// render draws the gutter, delegates every plot row to drawRow and adds
// the X axis.
func (f chartFrame) render(t *theme.Theme, step time.Duration, samplesPerCol int, drawRow func(p *painter, row int)) string {
	if f.width == 0 || f.height == 0 {
		return ""
	}
	p := newPainter(f.width * f.height * 8)
	for row := range f.plotH {
		if row > 0 {
			p.newline()
		}
		f.gutterCell(p, t, row)
		drawRow(p, row)
	}
	if f.showX {
		if f.plotH > 0 {
			p.newline()
		}
		f.gutterCell(p, t, f.plotH)
		f.xAxis(p, t, step, samplesPerCol)
	}
	return p.String()
}

func (f chartFrame) gutterCell(p *painter, t *theme.Theme, row int) {
	if f.gutter == 0 {
		return
	}
	label, ok := f.yLabels[row]
	if !ok {
		p.spaces(f.gutter)
		return
	}
	text := FitLine(label, f.gutter-1)
	pad := f.gutter - 1 - ansi.StringWidth(text)
	p.spaces(pad)
	p.fgStr(text, t.P.Faint)
	p.space()
}

// xAxis draws a border-colored rule with time labels embedded in it.
func (f chartFrame) xAxis(p *painter, t *theme.Theme, step time.Duration, samplesPerCol int) {
	w := f.plotW
	if w <= 0 {
		return
	}
	cells := []rune(strings.Repeat("─", w))
	isLabel := make([]bool, w)
	place := func(padded string, end int) bool {
		runes := []rune(padded)
		start := end - len(runes) + 1
		if start < 0 || end >= w {
			return false
		}
		for i := start; i <= end; i++ {
			if isLabel[i] {
				return false
			}
		}
		for i, r := range runes {
			cells[start+i] = r
			isLabel[start+i] = true
		}
		return true
	}
	if !place(" now", w-1) && w >= 3 {
		place("now", w-1)
	}
	colSpan := step * time.Duration(max(samplesPerCol, 1))
	if colSpan > 0 {
		interval := xInterval(colSpan)
		for k := 1; ; k++ {
			age := interval * time.Duration(k)
			col := w - 1 - int(math.Round(float64(age)/float64(colSpan)))
			if col < 0 {
				break
			}
			padded := " -" + compactDuration(age) + " "
			place(padded, col+len([]rune(padded))/2)
		}
	}
	for i, r := range cells {
		if isLabel[i] {
			if r == ' ' {
				p.space()
				continue
			}
			p.fgCell(r, t.P.Faint)
			continue
		}
		p.fgCell(r, t.P.Border)
	}
}

var niceIntervals = []time.Duration{
	100 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond,
	time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second,
	15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute,
	5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
	24 * time.Hour, 48 * time.Hour, 7 * 24 * time.Hour,
}

// xInterval picks the smallest nice duration that spaces labels at least
// minXLabelGap columns apart.
func xInterval(colSpan time.Duration) time.Duration {
	minGap := colSpan * minXLabelGap
	for _, d := range niceIntervals {
		if d >= minGap && d%colSpan == 0 {
			return d
		}
	}
	for _, d := range niceIntervals {
		if d >= minGap {
			return d
		}
	}
	return minGap
}

// compactDuration renders d in the single largest unit that divides it
// evenly while keeping a value of at least 2: 90s, 2m, 6h, 2d.
func compactDuration(d time.Duration) string {
	day := 24 * time.Hour
	switch {
	case d >= 2*day && d%day == 0:
		return strconv.FormatInt(int64(d/day), 10) + "d"
	case d >= 2*time.Hour && d%time.Hour == 0:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	case d >= 2*time.Minute && d%time.Minute == 0:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	case d%time.Second == 0:
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	case d >= time.Second:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
	default:
		return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
	}
}

// sampleAt returns the value of s shown at column col of a plot that is
// cols wide with the newest value in the last column. ok is false for
// columns left of the oldest value.
func sampleAt(values []float64, col, cols int) (float64, bool) {
	idx := len(values) - cols + col
	if idx < 0 || idx >= len(values) {
		return 0, false
	}
	return values[idx], true
}

// positive maps NaN, ±Inf and negatives to zero.
func positive(v float64) float64 {
	if !(v > 0) || math.IsInf(v, 1) {
		return 0
	}
	return v
}
