package screens

import (
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/format"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/widgets"
)

// historyWindows are the selectable time spans of the Detail charts,
// shortest first. The "w" key cycles through them.
var historyWindows = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

// defaultHistoryWindow indexes historyWindows: 5 minutes.
const defaultHistoryWindow = 1

// nextWindow returns the index of the window after i, wrapping around.
func nextWindow(i int) int {
	return (i + 1) % len(historyWindows)
}

// windowLabel renders a time span compactly in its largest whole unit:
// "45s", "5m", "2h".
func windowLabel(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d < time.Minute:
		return strconv.Itoa(int(math.Round(d.Seconds()))) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(math.Round(d.Minutes()))) + "m"
	default:
		return strconv.Itoa(int(math.Round(d.Hours()))) + "h"
	}
}

// windowCount is how many of the newest n samples, taken every step,
// cover window. It is clamped to the available history: at least one
// sample when n > 0 and never more than n.
func windowCount(n int, step, window time.Duration) int {
	if n <= 0 {
		return 0
	}
	if step <= 0 || window <= 0 {
		return n
	}
	return clamp(int(window/step), 1, n)
}

// axisFriendlySpans are the time spans per chart column whose time axis
// gets labels at round times about 10–25 columns apart (10s, 30s, 1m,
// 2m, 5m, 10m …). Other spans, such as 7s or 16s per column, make the
// widgets pick a label interval that can lie beyond the whole plot.
var axisFriendlySpans = []time.Duration{
	time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second,
	5 * time.Second, 6 * time.Second, 8 * time.Second, 10 * time.Second,
	12 * time.Second, 15 * time.Second, 20 * time.Second, 24 * time.Second,
	30 * time.Second, 40 * time.Second, time.Minute, 2 * time.Minute,
}

// bucketSize is the number of consecutive samples per plotted value so
// that n samples fit into at most points values; 1 when they already fit.
// Of the sizes that fit, up to twice the minimum ⌈n/points⌉, it prefers
// the smallest whose
// column span (step × size × perCol values per column) is axis friendly;
// otherwise it returns the minimum.
func bucketSize(n, points, perCol int, step time.Duration) int {
	if n <= 0 || points <= 0 {
		return 1
	}
	need := (n + points - 1) / points
	if need == 1 {
		return 1
	}
	for k := need; k <= 2*need; k++ {
		if slices.Contains(axisFriendlySpans, step*time.Duration(k*max(perCol, 1))) {
			return k
		}
	}
	return need
}

// reduceFunc folds one bucket of consecutive samples into one value.
type reduceFunc func(bucket []float64) float64

// bucketMean averages the finite values of bucket. It is used for rates
// and memory, where the average is the honest summary of a bucket. It
// returns NaN when no value is finite, which charts draw as a gap.
func bucketMean(bucket []float64) float64 {
	sum, n := 0.0, 0
	for _, v := range bucket {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		sum += v
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	return sum / float64(n)
}

// bucketMax is the largest finite value of bucket. It is used for CPU so
// that a short saturation spike stays visible in long windows instead of
// being averaged away. It returns NaN when no value is finite.
func bucketMax(bucket []float64) float64 {
	peak, ok := 0.0, false
	for _, v := range bucket {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if !ok || v > peak {
			peak, ok = v, true
		}
	}
	if !ok {
		return math.NaN()
	}
	return peak
}

// downsample reduces values (oldest first) to ⌈n/k⌉ values, one per
// bucket of k consecutive samples. Buckets are aligned to the newest
// value, which always ends the last bucket, so the result stays
// right-aligned like the charts draw it; only the oldest bucket may be
// partial. With k ≤ 1 the values are copied unchanged: a series is never
// upsampled. The input is not modified.
func downsample(values []float64, k int, reduce reduceFunc) []float64 {
	n := len(values)
	if n == 0 {
		return nil
	}
	if k <= 1 {
		out := make([]float64, n)
		copy(out, values)
		return out
	}
	out := make([]float64, (n+k-1)/k)
	hi := n
	for i := len(out) - 1; i >= 0; i-- {
		lo := max(hi-k, 0)
		out[i] = reduce(values[lo:hi])
		hi = lo
	}
	return out
}

// windowSeries is one chart series before windowing.
type windowSeries struct {
	name   string
	values []float64
	color  theme.RGB
	reduce reduceFunc
}

// windowResult is a set of series cut to a time window and reduced to the
// plot width.
type windowResult struct {
	series []widgets.Series
	// step is the effective time between plotted values.
	step time.Duration
	// span is the time the plotted values cover.
	span time.Duration
}

// applyWindow cuts every source to the newest window of history (clamped
// to what is available), downsamples it to fit the plot width of o and
// returns the series with their effective step: the sampling step times
// the bucket size. perCol is the number of values a chart draws per cell
// column: 1 for AreaChart, 2 for LineChart.
func applyWindow(o widgets.ChartOpts, step, window time.Duration, perCol int, sources []windowSeries) windowResult {
	n := 0
	for _, src := range sources {
		n = max(n, len(src.values))
	}
	count := windowCount(n, step, window)
	cut := make([][]float64, len(sources))
	for i, src := range sources {
		cut[i] = tail(src.values, count)
	}
	perCol = max(perCol, 1)
	k := 1
	reduceTo := func(cols int) [][]float64 {
		k = bucketSize(count, cols*perCol, perCol, step)
		out := make([][]float64, len(sources))
		for i, src := range sources {
			out[i] = downsample(cut[i], k, src.reduce)
		}
		return out
	}
	cols := plotWidth(o, peakOf(cut))
	reduced := reduceTo(cols)
	if o.Max <= 0 {
		if again := plotWidth(o, peakOf(reduced)); again != cols {
			reduced = reduceTo(again)
		}
	}
	res := windowResult{
		step: step * time.Duration(k),
		span: step * time.Duration(count),
	}
	for i, src := range sources {
		res.series = append(res.series, widgets.Series{Name: src.name, Values: reduced[i], Color: src.color})
	}
	return res
}

// peakOf is the largest finite, positive value of all series.
func peakOf(series [][]float64) float64 {
	peak := 0.0
	for _, values := range series {
		for _, v := range values {
			if v > peak && !math.IsInf(v, 1) {
				peak = v
			}
		}
	}
	return peak
}

// plotMinWidth mirrors the narrowest plot that still gets a Y axis in the
// widgets package.
const plotMinWidth = 6

// plotWidth is the number of cell columns AreaChart and LineChart give to
// the plot for o when the largest value is dataMax: the width minus the Y
// axis gutter (widest tick label plus one space). It mirrors the axis
// layout of the widgets package so a downsampled series fills the plot
// exactly, without a blank strip on the left and without its oldest
// values being cut off.
func plotWidth(o widgets.ChartOpts, dataMax float64) int {
	w := max(o.Width, 0)
	if o.HideAxes || o.Height <= 0 {
		return w
	}
	showX := o.Height >= 3
	plotH := o.Height
	if showX {
		plotH--
	}
	ticks := min(max(plotH/3, 1), 5)
	top := o.Max
	var tick float64
	if top > 0 && !math.IsInf(top, 0) {
		_, tick = widgets.NiceScale(top, ticks)
	} else {
		top, tick = widgets.NiceScale(dataMax, ticks)
	}
	label := o.YLabel
	if label == nil {
		label = func(v float64) string {
			if math.Abs(v) >= 1000 {
				return format.Number(v)
			}
			return strconv.FormatFloat(math.Round(v*1e6)/1e6, 'f', -1, 64)
		}
	}
	taken := map[int]bool{}
	widest := 0
	rowOf := func(v float64) int { return plotH - int(math.Round(v/top*float64(plotH))) }
	add := func(v float64, row int) {
		if row < 0 || row > plotH || (row == plotH && !showX) || taken[row] {
			return
		}
		taken[row] = true
		widest = max(widest, ansi.StringWidth(label(v)))
	}
	lastRow := plotH + 1
	for k := 0; k <= 50; k++ {
		v := float64(k) * tick
		if v > top*(1+1e-9) {
			break
		}
		row := rowOf(v)
		add(v, row)
		lastRow = row
	}
	if lastRow >= 2 {
		add(top, 0)
	}
	if widest > 0 && w-widest-1 >= plotMinWidth {
		return w - widest - 1
	}
	return w
}
