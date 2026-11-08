package screens

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/format"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/widgets"
)

func ramp(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = float64(i + 1)
	}
	return out
}

func TestDownsample(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		k      int
		reduce reduceFunc
		want   []float64
	}{
		{"empty input", nil, 3, bucketMean, nil},
		{"bucket of one copies", ramp(4), 1, bucketMax, []float64{1, 2, 3, 4}},
		{"non-positive bucket copies", ramp(3), 0, bucketMean, []float64{1, 2, 3}},
		{"even buckets mean", ramp(8), 2, bucketMean, []float64{1.5, 3.5, 5.5, 7.5}},
		{"even buckets max", ramp(8), 2, bucketMax, []float64{2, 4, 6, 8}},
		{"oldest bucket partial mean", ramp(10), 3, bucketMean, []float64{1, 3, 6, 9}},
		{"oldest bucket partial max", ramp(10), 4, bucketMax, []float64{2, 6, 10}},
		{"bucket larger than input", ramp(5), 60, bucketMean, []float64{3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := downsample(tt.values, tt.k, tt.reduce)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("downsample = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDownsampleLengthsAndAlignment(t *testing.T) {
	for _, n := range []int{1, 7, 59, 60, 61, 300, 899, 900} {
		for _, points := range []int{1, 2, 13, 56, 60, 151, 400} {
			values := ramp(n)
			k := bucketSize(n, points, 1, time.Second)
			got := downsample(values, k, bucketMax)
			if want := (n + k - 1) / k; len(got) != want {
				t.Fatalf("n=%d points=%d k=%d: len %d, want %d", n, points, k, len(got), want)
			}
			if len(got) > points {
				t.Fatalf("n=%d points=%d: %d values do not fit", n, points, len(got))
			}
			if n <= points && len(got) != n {
				t.Fatalf("n=%d points=%d: upsampled or dropped to %d", n, points, len(got))
			}
			if got[len(got)-1] != values[n-1] {
				t.Fatalf("n=%d points=%d: newest %v, want %v (right alignment)", n, points, got[len(got)-1], values[n-1])
			}
			if !slices.IsSorted(got) {
				t.Fatalf("n=%d points=%d: buckets out of order: %v", n, points, got)
			}
		}
	}
}

func TestDownsampleKeepsInput(t *testing.T) {
	values := ramp(6)
	out := downsample(values, 1, bucketMean)
	out[0] = 99
	if values[0] != 1 {
		t.Fatal("downsample aliased its input")
	}
}

func TestBucketSize(t *testing.T) {
	tests := []struct {
		n, points, perCol int
		step              time.Duration
		want              int
	}{
		{300, 171, 1, time.Second, 2},
		{300, 300, 1, time.Second, 1},
		{60, 192, 2, time.Second, 1},
		{900, 171, 1, time.Second, 6},
		{300, 43, 1, time.Second, 8},
		{300, 86, 2, time.Second, 4},
		{900, 120, 2, time.Second, 10},
		{900, 64, 1, time.Second, 15},
		{301, 30, 1, time.Second, 12},
		{900, 130, 1, 7 * time.Second, 7},
		{5000, 10, 1, time.Second, 500},
		{0, 10, 1, time.Second, 1},
		{10, 0, 1, time.Second, 1},
	}
	for _, tt := range tests {
		got := bucketSize(tt.n, tt.points, tt.perCol, tt.step)
		if got != tt.want {
			t.Errorf("bucketSize(%d, %d, %d, %v) = %d, want %d", tt.n, tt.points, tt.perCol, tt.step, got, tt.want)
		}
		if tt.n > 0 && tt.points > 0 && (tt.n+got-1)/got > tt.points {
			t.Errorf("bucketSize(%d, %d) = %d does not fit", tt.n, tt.points, got)
		}
	}
}

func TestBucketReducersSkipNonFinite(t *testing.T) {
	bucket := []float64{math.NaN(), 2, math.Inf(1), 4}
	if got := bucketMean(bucket); got != 3 {
		t.Errorf("bucketMean = %v, want 3", got)
	}
	if got := bucketMax(bucket); got != 4 {
		t.Errorf("bucketMax = %v, want 4", got)
	}
	if got := bucketMax([]float64{-3, -1}); got != -1 {
		t.Errorf("bucketMax of negatives = %v, want -1", got)
	}
	if !math.IsNaN(bucketMean([]float64{math.NaN()})) || !math.IsNaN(bucketMax(nil)) {
		t.Error("all non-finite bucket must reduce to NaN")
	}
}

func TestWindowCount(t *testing.T) {
	tests := []struct {
		n      int
		step   time.Duration
		window time.Duration
		want   int
	}{
		{900, time.Second, 5 * time.Minute, 300},
		{900, time.Second, 15 * time.Minute, 900},
		{300, time.Second, 15 * time.Minute, 300},
		{900, 2 * time.Second, time.Minute, 30},
		{900, 2 * time.Minute, time.Minute, 1},
		{0, time.Second, time.Minute, 0},
		{50, 0, time.Minute, 50},
	}
	for _, tt := range tests {
		if got := windowCount(tt.n, tt.step, tt.window); got != tt.want {
			t.Errorf("windowCount(%d, %v, %v) = %d, want %d", tt.n, tt.step, tt.window, got, tt.want)
		}
	}
}

func TestWindowCycleAndLabels(t *testing.T) {
	if historyWindows[defaultHistoryWindow] != 5*time.Minute {
		t.Fatalf("default window %v, want 5m", historyWindows[defaultHistoryWindow])
	}
	var labels []string
	i := defaultHistoryWindow
	for range historyWindows {
		i = nextWindow(i)
		labels = append(labels, windowLabel(historyWindows[i]))
	}
	if want := []string{"15m", "1m", "5m"}; !slices.Equal(labels, want) {
		t.Fatalf("cycle %v, want %v", labels, want)
	}
	for d, want := range map[time.Duration]string{0: "0s", 40 * time.Second: "40s", 299 * time.Second: "5m", 2 * time.Hour: "2h"} {
		if got := windowLabel(d); got != want {
			t.Errorf("windowLabel(%v) = %q, want %q", d, got, want)
		}
	}
}

// filledColumns counts the plot cells of the bottom plot row of an area
// chart whose values are all positive.
func filledColumns(chart string, height int) int {
	rows := strings.Split(ansi.Strip(chart), "\n")
	return strings.Count(rows[height-2], "█")
}

func TestPlotWidthMatchesCharts(t *testing.T) {
	th := theme.Default()
	tests := []struct {
		name string
		opts widgets.ChartOpts
		peak float64
	}{
		{"percent", widgets.ChartOpts{Width: 50, Height: 10, Max: 100, YLabel: format.Percent}, 60},
		{"percent short", widgets.ChartOpts{Width: 40, Height: 4, Max: 100, YLabel: format.Percent}, 60},
		{"bits", widgets.ChartOpts{Width: 70, Height: 12, YLabel: format.Bits}, 37e6},
		{"bits small", widgets.ChartOpts{Width: 36, Height: 5, YLabel: format.Bits}, 812e3},
		{"default labels", widgets.ChartOpts{Width: 30, Height: 8}, 4321},
		{"too narrow for axis", widgets.ChartOpts{Width: 9, Height: 6, Max: 100, YLabel: format.Percent}, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := plotWidth(tt.opts, tt.peak)
			o := tt.opts
			values := make([]float64, want)
			for i := range values {
				values[i] = tt.peak
			}
			o.Series = []widgets.Series{{Name: "v", Values: values, Color: th.P.Accent}}
			if got := filledColumns(widgets.AreaChart(th, o), o.Height); got != want {
				t.Fatalf("chart plots %d columns, plotWidth says %d", got, want)
			}
		})
	}
}

func TestApplyWindow(t *testing.T) {
	th := theme.Default()
	opts := widgets.ChartOpts{Width: 65, Height: 10, Max: 100, YLabel: format.Percent}
	cols := plotWidth(opts, 0)
	src := []windowSeries{{name: "cpu", values: ramp(900), color: th.P.Accent, reduce: bucketMax}}

	res := applyWindow(opts, time.Second, 5*time.Minute, 1, src)
	k := bucketSize(300, cols, 1, time.Second)
	values := res.series[0].Values
	if got, want := len(values), (300+k-1)/k; got != want || got > cols {
		t.Fatalf("5m window: %d points, want %d within plot width %d", got, want, cols)
	}
	if res.span != 5*time.Minute || res.step != time.Duration(k)*time.Second {
		t.Fatalf("5m window: span %v step %v, want 5m and %ds", res.span, res.step, k)
	}
	if last := values[len(values)-1]; last != 900 {
		t.Fatalf("newest bucket %v, want 900", last)
	}

	line := applyWindow(opts, time.Second, time.Minute, 2, src)
	if got := len(line.series[0].Values); got != 60 {
		t.Fatalf("1m window: %d points, want 60", got)
	}
	if line.step != time.Second {
		t.Fatalf("1m window without downsampling: step %v, want 1s", line.step)
	}

	short := applyWindow(opts, time.Second, 15*time.Minute, 1, []windowSeries{{name: "cpu", values: ramp(120), reduce: bucketMean}})
	if short.span != 2*time.Minute {
		t.Fatalf("clamped span %v, want 2m", short.span)
	}
}
