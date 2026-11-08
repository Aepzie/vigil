package screens

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/CraguS/vigil/internal/format"
	"github.com/CraguS/vigil/internal/theme"
	"github.com/CraguS/vigil/internal/widgets"
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
		points int
		reduce reduceFunc
		want   []float64
	}{
		{"empty input", nil, 10, bucketMean, nil},
		{"zero points", ramp(5), 0, bucketMean, nil},
		{"fewer samples than points is not upsampled", ramp(3), 10, bucketMean, []float64{1, 2, 3}},
		{"equal length is copied", ramp(4), 4, bucketMax, []float64{1, 2, 3, 4}},
		{"even buckets mean", ramp(8), 4, bucketMean, []float64{1.5, 3.5, 5.5, 7.5}},
		{"even buckets max", ramp(8), 4, bucketMax, []float64{2, 4, 6, 8}},
		{"partial buckets mean", ramp(10), 4, bucketMean, []float64{1.5, 4, 6.5, 9}},
		{"partial buckets max", ramp(10), 4, bucketMax, []float64{2, 5, 7, 10}},
		{"single point", ramp(9), 1, bucketMean, []float64{5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := downsample(tt.values, tt.points, tt.reduce)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("downsample = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDownsampleLengthsAndAlignment(t *testing.T) {
	for _, n := range []int{1, 7, 59, 60, 61, 300, 899, 900} {
		for _, points := range []int{1, 2, 13, 56, 60, 151} {
			values := ramp(n)
			got := downsample(values, points, bucketMax)
			if want := min(n, points); len(got) != want {
				t.Fatalf("n=%d points=%d: len %d, want %d", n, points, len(got), want)
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
	out := downsample(values, 6, bucketMean)
	out[0] = 99
	if values[0] != 1 {
		t.Fatal("downsample aliased its input")
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

func TestWindowStep(t *testing.T) {
	tests := []struct {
		count, points int
		want          time.Duration
	}{
		{300, 60, 5 * time.Second},
		{300, 56, 5357142857 * time.Nanosecond},
		{900, 150, 6 * time.Second},
		{40, 60, time.Second},
		{60, 60, time.Second},
		{60, 0, time.Second},
	}
	for _, tt := range tests {
		if got := windowStep(time.Second, tt.count, tt.points); got != tt.want {
			t.Errorf("windowStep(1s, %d, %d) = %v, want %v", tt.count, tt.points, got, tt.want)
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
	if got := len(res.series[0].Values); got != cols {
		t.Fatalf("5m window: %d points, want plot width %d", got, cols)
	}
	if res.span != 5*time.Minute || res.step != 5*time.Minute/time.Duration(cols) {
		t.Fatalf("5m window: span %v step %v", res.span, res.step)
	}
	if last := res.series[0].Values[cols-1]; last != 900 {
		t.Fatalf("newest bucket %v, want 900", last)
	}

	line := applyWindow(opts, time.Second, time.Minute, 2, src)
	if got := len(line.series[0].Values); got != min(60, 2*cols) {
		t.Fatalf("1m window: %d points, want %d", got, min(60, 2*cols))
	}
	if line.step != time.Second {
		t.Fatalf("1m window without downsampling: step %v, want 1s", line.step)
	}

	short := applyWindow(opts, time.Second, 15*time.Minute, 1, []windowSeries{{name: "cpu", values: ramp(120), reduce: bucketMean}})
	if short.span != 2*time.Minute {
		t.Fatalf("clamped span %v, want 2m", short.span)
	}
}
