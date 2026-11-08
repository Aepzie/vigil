package widgets

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Aepzie/vigil/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

var (
	testWidths  = []int{-3, 0, 1, 2, 3, 4, 5, 7, 9, 13, 24, 40, 81, 200}
	testHeights = []int{-1, 0, 1, 2, 3, 4, 5, 8, 13, 20}
)

// assertBlock fails unless s is exactly w×h cells. Empty sizes must give
// an empty string.
func assertBlock(t *testing.T, name, s string, w, h int) {
	t.Helper()
	w, h = max0(w), max0(h)
	if w == 0 || h == 0 {
		if s != "" && ansi.StringWidth(s) != 0 {
			t.Fatalf("%s: %dx%d want empty, got %q", name, w, h, s)
		}
		return
	}
	lines := strings.Split(s, "\n")
	if len(lines) != h {
		t.Fatalf("%s: %dx%d got %d lines:\n%s", name, w, h, len(lines), ansi.Strip(s))
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != w {
			t.Fatalf("%s: %dx%d line %d is %d cells: %q", name, w, h, i, got, ansi.Strip(l))
		}
	}
}

func chartVariants(th *theme.Theme) map[string]ChartOpts {
	long := make([]float64, 500)
	for i := range long {
		long[i] = float64(i%37) * 3.3
	}
	odd := []float64{math.NaN(), -5, math.Inf(1), 3, math.Inf(-1), 0, 1e12, math.NaN(), 2}
	return map[string]ChartOpts{
		"empty":    {},
		"noValues": {Series: []Series{{Color: th.P.Accent}}},
		"single":   {Series: []Series{{Values: []float64{42}, Color: th.P.Accent}}, Step: time.Second},
		"stacked":  {Series: []Series{{Values: long, Color: th.P.Accent}, {Values: long[:120], Color: th.P.Accent2}}, Step: 2 * time.Second},
		"odd":      {Series: []Series{{Values: odd, Color: th.P.Bad}, {Values: odd, Color: th.P.Good}}, Step: time.Minute},
		"fixedMax": {Series: []Series{{Values: long, Color: th.P.Teal}}, Max: 7.5, Step: 500 * time.Millisecond},
		"hidden":   {Series: []Series{{Values: long, Color: th.P.Teal}}, HideAxes: true},
		"longLabels": {
			Series: []Series{{Values: long, Color: th.P.Blue}},
			YLabel: func(v float64) string { return fmt.Sprintf("%.3f units/sec", v) },
			Step:   time.Hour,
		},
		"zeros":     {Series: []Series{{Values: make([]float64, 30), Color: th.P.Warn}}, Step: time.Second},
		"fractions": {Series: []Series{{Values: []float64{0.01, 0.2, 0.05}, Color: th.P.Warn}}},
		"negMax":    {Series: []Series{{Values: long, Color: th.P.Warn}}, Max: -4},
	}
}

func TestChartsExactSize(t *testing.T) {
	th := theme.Default()
	for name, base := range chartVariants(th) {
		for _, w := range testWidths {
			for _, h := range testHeights {
				o := base
				o.Width, o.Height = w, h
				assertBlock(t, "AreaChart/"+name, AreaChart(th, o), w, h)
				assertBlock(t, "LineChart/"+name, LineChart(th, o), w, h)
			}
		}
	}
}

func TestAreaChartNewestAtRight(t *testing.T) {
	th := theme.Default()
	o := ChartOpts{
		Width: 10, Height: 2, Max: 10, HideAxes: true,
		Series: []Series{{Values: []float64{10, 10, 10}, Color: th.P.Accent}},
	}
	for _, line := range strings.Split(ansi.Strip(AreaChart(th, o)), "\n") {
		if !strings.HasSuffix(line, "███") || strings.Contains(strings.TrimSuffix(line, "███"), "█") {
			t.Fatalf("want three full cells at the right edge, got %q", line)
		}
	}
	o.Series[0].Values = make([]float64, 30)
	for i := range o.Series[0].Values {
		o.Series[0].Values[i] = 10
	}
	o.Series[0].Values[0] = 0
	if got := ansi.Strip(AreaChart(th, o)); strings.Contains(got, " ") {
		t.Fatalf("more samples than columns must show the newest only:\n%s", got)
	}
}

func TestAreaChartEighths(t *testing.T) {
	th := theme.Default()
	tests := []struct {
		value float64
		want  string
	}{
		{0, " "},
		{1, "▁"},
		{4, "▄"},
		{7, "▇"},
		{8, "█"},
	}
	for _, tc := range tests {
		o := ChartOpts{
			Width: 1, Height: 1, Max: 8, HideAxes: true,
			Series: []Series{{Values: []float64{tc.value}, Color: th.P.Accent}},
		}
		got := ansi.Strip(AreaChart(th, o))
		if tc.value == 0 {
			if got != " " && got != string(gridRune) {
				t.Errorf("value 0: got %q", got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("value %v: got %q want %q", tc.value, got, tc.want)
		}
	}
}

func TestAreaChartStackBoundaryUsesBackground(t *testing.T) {
	th := theme.Default()
	o := ChartOpts{
		Width: 1, Height: 1, Max: 8, HideAxes: true,
		Series: []Series{
			{Values: []float64{3}, Color: th.P.Accent},
			{Values: []float64{5}, Color: th.P.Accent2},
		},
	}
	got := AreaChart(th, o)
	if ansi.Strip(got) != "▃" {
		t.Fatalf("want lower band as ▃, got %q", ansi.Strip(got))
	}
	if !strings.Contains(got, "\x1b[48;2;") {
		t.Fatalf("upper band must be drawn as background: %q", got)
	}
}

func TestChartAxes(t *testing.T) {
	th := theme.Default()
	values := make([]float64, 100)
	for i := range values {
		values[i] = float64(i)
	}
	o := ChartOpts{Width: 70, Height: 10, Step: time.Second, Series: []Series{{Values: values, Color: th.P.Accent}}}
	lines := strings.Split(ansi.Strip(AreaChart(th, o)), "\n")
	axis := lines[len(lines)-1]
	for _, want := range []string{"now", "-10s", "-30s", "─"} {
		if !strings.Contains(axis, want) {
			t.Errorf("x axis %q lacks %q", axis, want)
		}
	}
	if !strings.HasSuffix(axis, "now") {
		t.Errorf("x axis must end with now: %q", axis)
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "100") {
		t.Errorf("top label should be the nice max 100: %q", lines[0])
	}
	if !strings.Contains(lines[0], string(gridRune)) {
		t.Errorf("top row should carry a grid line: %q", lines[0])
	}
	if !strings.HasPrefix(strings.TrimSpace(axis), "0") {
		t.Errorf("axis row should start with the 0 label: %q", axis)
	}

	line := strings.Split(ansi.Strip(LineChart(th, o)), "\n")
	if !strings.Contains(line[len(line)-1], "-1m") {
		t.Errorf("line chart covers two samples per column: %q", line[len(line)-1])
	}
}

func TestLineChartBraille(t *testing.T) {
	th := theme.Default()
	o := ChartOpts{
		Width: 2, Height: 1, Max: 3, HideAxes: true,
		Series: []Series{{Values: []float64{0, 1, 2, 3}, Color: th.P.Accent}},
	}
	got := []rune(ansi.Strip(LineChart(th, o)))
	want := []rune{rune(brailleBase + 0x40 + 0x20), rune(brailleBase + 0x02 + 0x08)}
	if string(got) != string(want) {
		t.Fatalf("got %q want %q", string(got), string(want))
	}
}

func TestLineChartLaterSeriesWins(t *testing.T) {
	th := theme.Default()
	o := ChartOpts{
		Width: 1, Height: 1, Max: 1, HideAxes: true,
		Series: []Series{
			{Values: []float64{1, 1}, Color: th.P.Good},
			{Values: []float64{0, 0}, Color: th.P.Bad},
		},
	}
	got := LineChart(th, o)
	if !strings.Contains(got, th.P.Bad.FG()) || strings.Contains(got, th.P.Good.FG()) {
		t.Fatalf("later series must own the shared cell: %q", got)
	}
}

func TestChartAvoidsRedundantSGR(t *testing.T) {
	th := theme.Default()
	o := ChartOpts{
		Width: 50, Height: 4, Max: 1, HideAxes: true,
		Series: []Series{{Values: slicesRepeat(1.0, 50), Color: th.P.Accent}},
	}
	for i, line := range strings.Split(AreaChart(th, o), "\n") {
		if n := strings.Count(line, "\x1b[38;2;"); n != 1 {
			t.Errorf("row %d has %d color sequences, want 1", i, n)
		}
	}
}

func slicesRepeat(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestNiceScale(t *testing.T) {
	tests := []struct {
		max      float64
		ticks    int
		wantMax  float64
		wantStep float64
	}{
		{87, 4, 100, 50},
		{100, 5, 100, 20},
		{3, 4, 3, 1},
		{0, 4, 1, 0.5},
		{-5, 2, 1, 0.5},
		{math.NaN(), 3, 1, 0.5},
		{math.Inf(1), 3, 1, 0.5},
		{0.7, 3, 1, 0.5},
		{1e6, 4, 1e6, 5e5},
		{1234, 5, 1500, 500},
		{9.5, 0, 10, 10},
		{0.042, 4, 0.06, 0.02},
	}
	for _, tc := range tests {
		gotMax, gotStep := NiceScale(tc.max, tc.ticks)
		if !near(gotMax, tc.wantMax) || !near(gotStep, tc.wantStep) {
			t.Errorf("NiceScale(%v, %d) = (%v, %v), want (%v, %v)", tc.max, tc.ticks, gotMax, gotStep, tc.wantMax, tc.wantStep)
		}
	}
	for _, m := range []float64{0.003, 0.9, 1, 7, 13, 99, 101, 555, 8e9} {
		for ticks := 1; ticks <= 8; ticks++ {
			top, step := NiceScale(m, ticks)
			if top < m || top/step > float64(ticks)+1e-6 {
				t.Errorf("NiceScale(%v, %d) = (%v, %v) violates bounds", m, ticks, top, step)
			}
			mant := step / math.Pow(10, math.Floor(math.Log10(step)))
			if !near(mant, 1) && !near(mant, 2) && !near(mant, 5) {
				t.Errorf("NiceScale(%v, %d) step %v is not 1/2/5×10^n", m, ticks, step)
			}
		}
	}
}

func near(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b))
}

func TestDefaultYLabel(t *testing.T) {
	tests := map[float64]string{
		0:                   "0",
		7.5:                 "7.5",
		0.30000000000000004: "0.3",
		0.05:                "0.05",
		250:                 "250",
		1000:                "1.0k",
		12400:               "12.4k",
		3e6:                 "3.0M",
	}
	for v, want := range tests {
		if got := defaultYLabel(v); got != want {
			t.Errorf("defaultYLabel(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestCompactDuration(t *testing.T) {
	tests := map[time.Duration]string{
		30 * time.Second:        "30s",
		60 * time.Second:        "60s",
		90 * time.Second:        "90s",
		2 * time.Minute:         "2m",
		90 * time.Minute:        "90m",
		2 * time.Hour:           "2h",
		48 * time.Hour:          "2d",
		1500 * time.Millisecond: "1.5s",
		200 * time.Millisecond:  "200ms",
	}
	for d, want := range tests {
		if got := compactDuration(d); got != want {
			t.Errorf("compactDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestXInterval(t *testing.T) {
	tests := []struct {
		span, total, want time.Duration
	}{
		{time.Second, 60 * time.Second, 10 * time.Second},
		{2 * time.Second, 2 * time.Minute, 30 * time.Second},
		{3 * time.Second, 2 * time.Minute, 30 * time.Second},
		{100 * time.Millisecond, 10 * time.Second, time.Second},
		{time.Minute, time.Hour, 10 * time.Minute},
		{3 * time.Second, 8 * time.Minute, time.Minute},
		{2 * time.Second, 5 * time.Minute, time.Minute},
		{10 * time.Second, 30 * time.Minute, 2 * time.Minute},
		{5 * time.Minute, 12 * time.Hour, time.Hour},
	}
	for _, tt := range tests {
		if got := xInterval(tt.span, tt.total); got != tt.want {
			t.Errorf("xInterval(%v, %v) = %v, want %v", tt.span, tt.total, got, tt.want)
		}
	}
}

func TestChartXLabelsUseMinutesOnLongSpans(t *testing.T) {
	th := theme.Default()
	values := make([]float64, 200)
	for i := range values {
		values[i] = float64(i % 50)
	}
	out := ansi.Strip(AreaChart(th, ChartOpts{
		Width: 200, Height: 12, Step: 2 * time.Second,
		Series: []Series{{Name: "cpu", Values: values, Color: th.P.Accent}},
	}))
	lines := strings.Split(out, "\n")
	axis := lines[len(lines)-1]
	for _, bad := range []string{"-270s", "-210s", "-150s", "-330s"} {
		if strings.Contains(axis, bad) {
			t.Fatalf("axis %q contains %q", axis, bad)
		}
	}
	if !strings.Contains(axis, "-2m") || !strings.Contains(axis, "now") {
		t.Fatalf("axis %q lacks minute labels", axis)
	}
}

func TestSparkline(t *testing.T) {
	c := theme.Default().P.Accent
	for _, w := range testWidths {
		for _, values := range [][]float64{nil, {1}, {0, 0}, {math.NaN(), -1, 5, math.Inf(1)}, slicesRepeat(3, 300)} {
			assertBlock(t, "Sparkline", Sparkline(values, w, 0, c), w, 1)
			assertBlock(t, "Sparkline max", Sparkline(values, w, 2, c), w, 1)
		}
	}
	if got := ansi.Strip(Sparkline([]float64{0, 4, 8}, 5, 0, c)); got != "  ▁▅█" {
		t.Errorf("right-aligned sparkline = %q", got)
	}
	if got := ansi.Strip(Sparkline([]float64{1, 2, 3, 4, 8}, 2, 8, c)); got != "▅█" {
		t.Errorf("newest values at right = %q", got)
	}
}

func TestSparklineRange(t *testing.T) {
	c := theme.Default().P.Accent
	inputs := [][]float64{nil, {1}, {0, 0}, {math.NaN(), -1, 5, math.Inf(1), math.Inf(-1)}, slicesRepeat(3, 300)}
	for _, w := range testWidths {
		for _, values := range inputs {
			assertBlock(t, "SparklineRange", SparklineRange(values, w, 0, 10, c), w, 1)
			assertBlock(t, "SparklineRange empty scale", SparklineRange(values, w, 5, 5, c), w, 1)
			assertBlock(t, "SparklineRange inverted", SparklineRange(values, w, 10, 0, c), w, 1)
		}
	}
	tests := []struct {
		name   string
		values []float64
		width  int
		lo, hi float64
		want   string
	}{
		{"fixed scale is not stretched", []float64{9, 10, 9, 10}, 4, 0, 20, "▄▅▄▅"},
		{"lo offset", []float64{10, 15, 20}, 3, 10, 20, "▁▅█"},
		{"clamped outside the scale", []float64{-5, 30}, 2, 0, 10, "▁█"},
		{"right-aligned with NaN gaps", []float64{0, math.NaN(), 8}, 5, 0, 8, "  ▁ █"},
		{"newest values kept", []float64{8, 8, 0, 4}, 2, 0, 8, "▁▅"},
		{"empty scale", []float64{3, 7}, 2, 5, 5, "▁▁"},
	}
	for _, tc := range tests {
		got := SparklineRange(tc.values, tc.width, tc.lo, tc.hi, c)
		if s := ansi.Strip(got); s != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, s, tc.want)
		}
		if w := ansi.StringWidth(got); w != tc.width {
			t.Errorf("%s: width %d, want %d", tc.name, w, tc.width)
		}
	}
}

func TestProgress(t *testing.T) {
	th := theme.Default()
	for _, w := range testWidths {
		for _, f := range []float64{math.NaN(), -1, 0, 0.01, 0.5, 0.999, 1, 7, math.Inf(1)} {
			assertBlock(t, "Progress", Progress(th, w, f, th.P.Good), w, 1)
		}
	}
	got := Progress(th, 4, 0.625, th.P.Good)
	if ansi.Strip(got) != "━━╸━" {
		t.Errorf("half cell edge: %q", ansi.Strip(got))
	}
	if strings.Count(got, th.P.Good.FG()) != 1 || strings.Count(got, th.P.Border.FG()) != 1 {
		t.Errorf("fill and track should each need one color sequence: %q", got)
	}
}

func TestStackedBar(t *testing.T) {
	th := theme.Default()
	segs := []Segment{{Value: 1, Color: th.P.Accent}, {Value: math.NaN(), Color: th.P.Bad}, {Value: 2, Color: th.P.Teal}, {Value: -3, Color: th.P.Warn}}
	for _, w := range testWidths {
		for _, total := range []float64{0, -1, 1, 3, 6, math.Inf(1)} {
			assertBlock(t, "StackedBar", StackedBar(th, w, total, segs), w, 1)
			assertBlock(t, "StackedBar nil", StackedBar(th, w, total, nil), w, 1)
		}
	}
	got := StackedBar(th, 6, 6, segs)
	want := th.P.Accent.FG() + "━" + th.P.Teal.FG() + "━━" + th.P.Border.FG() + "━━━" + theme.Reset
	if got != want {
		t.Errorf("StackedBar = %q, want %q", got, want)
	}
}

func TestBigNumber(t *testing.T) {
	c := theme.Default().P.Text
	inputs := []string{"", "0", "12.4k", "87%", "-3,5", "1.2KMGTB", "9MB/s", "ÿ?x", "  ", "...", "1 2", "100%", "3.14159"}
	for _, in := range inputs {
		got := BigNumber(in, c)
		assertBlock(t, "BigNumber "+in, got, BigNumberWidth(in), 3)
	}
	if w := BigNumberWidth("1.5"); w != 7 {
		t.Errorf("narrow '.' should not add gaps: width %d", w)
	}
	if w := BigNumberWidth("88"); w != 7 {
		t.Errorf("wide glyphs are separated by one column: width %d", w)
	}
	for r, g := range bigGlyphs {
		w := ansi.StringWidth(g[0])
		for _, row := range g {
			if ansi.StringWidth(row) != w {
				t.Errorf("glyph %q rows differ in width", r)
			}
		}
	}
	want := "┏━┓ ┏━┓\n┣━┫ ┃ ┃\n┗━┛ ┗━┛"
	if got := ansi.Strip(BigNumber("80", c)); got != want {
		t.Errorf("BigNumber(80) =\n%s\nwant\n%s", got, want)
	}
}

func TestTableExactSize(t *testing.T) {
	th := theme.Default()
	cols := []Column{
		{Title: "PID", Width: 6, Align: AlignRightCol},
		{Title: "NAME", Min: 8, Flex: 2},
		{Title: "STATE", Width: 7, Align: AlignCenterCol},
		{Title: "USER", Min: 4},
	}
	rows := [][]string{
		{"1", "init", "run", "root"},
		{"22", th.Good.Render("styled") + " cell", "sleep", "a"},
		{"333", "a very long process name that will not fit anywhere", "zombie", "nobody-at-all"},
		{"4444"},
		{"5", "multi\nline", "x", "y", "extra"},
	}
	for _, w := range testWidths {
		for _, h := range testHeights {
			for _, sel := range []int{-1, 0, 2, 4, 99} {
				o := TableOpts{Width: w, Height: h, Columns: cols, Rows: rows, Selected: sel, Offset: 3}
				assertBlock(t, "Table", Table(th, o), w, h)
				o.Rows = nil
				o.Empty = "nothing to show here"
				assertBlock(t, "Table empty", Table(th, o), w, h)
				o.Columns = nil
				assertBlock(t, "Table no columns", Table(th, o), w, h)
			}
		}
	}
}

func TestTableSelection(t *testing.T) {
	th := theme.Default()
	o := TableOpts{
		Width: 30, Height: 3, Selected: 1,
		Columns: []Column{{Title: "NAME"}, {Title: "VAL", Width: 5, Align: AlignRightCol}},
		Rows: [][]string{
			{"plain", "1"},
			{th.Bad.Render("red") + " tail", "2"},
		},
	}
	lines := strings.Split(Table(th, o), "\n")
	sel := lines[2]
	bg := th.P.Surface2.BG()
	if !strings.HasPrefix(sel, bg) {
		t.Fatalf("selected row must start with the selection background: %q", sel)
	}
	for _, reset := range []string{"\x1b[m", "\x1b[0m"} {
		parts := strings.Split(sel, reset)
		for _, p := range parts[1 : len(parts)-1] {
			if !strings.HasPrefix(p, bg) {
				t.Fatalf("selection style not re-applied after reset in %q", sel)
			}
		}
	}
	if strings.Contains(lines[1], bg) {
		t.Fatalf("unselected row must not be highlighted: %q", lines[1])
	}
	if got := ansi.Strip(lines[0]); !strings.HasPrefix(got, "NAME") || !strings.HasSuffix(got, "VAL") {
		t.Fatalf("header = %q", got)
	}
}

func TestTableEmptyAndTruncation(t *testing.T) {
	th := theme.Default()
	o := TableOpts{
		Width: 20, Height: 4, Selected: -1, Empty: "no rows",
		Columns: []Column{{Title: "NAME"}},
	}
	lines := strings.Split(ansi.Strip(Table(th, o)), "\n")
	if lines[2] != "      no rows       " || strings.TrimSpace(lines[1]+lines[3]) != "" {
		t.Fatalf("empty text not centered: %q", lines)
	}
	o.Rows = [][]string{{"abcdefghijklmnopqrstuvwxyz"}}
	got := ansi.Strip(strings.Split(Table(th, o), "\n")[1])
	if got != "abcdefghijklmnopqrs…" {
		t.Fatalf("truncation = %q", got)
	}
}

func TestColumnWidths(t *testing.T) {
	cols := []Column{
		{Title: "A", Width: 4},
		{Title: "B", Min: 6, Flex: 1},
		{Title: "C", Min: 6, Flex: 3},
		{Title: "D", Width: 10},
	}
	tests := []struct {
		total int
		want  []int
	}{
		{0, []int{0, 0, 0, 0}},
		{3, []int{3, 0, 0, 0}},
		{4, []int{4, 0, 0, 0}},
		{12, []int{4, 6, 0, 0}},
		{20, []int{4, 6, 6, 0}},
		{40, []int{4, 8, 12, 10}},
		{48, []int{4, 10, 18, 10}},
	}
	for _, tc := range tests {
		got := ColumnWidths(cols, tc.total)
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("ColumnWidths(%d) = %v, want %v", tc.total, got, tc.want)
		}
	}
}

func TestColumnWidthsMax(t *testing.T) {
	tests := []struct {
		name  string
		cols  []Column
		total int
		want  []int
	}{
		{
			"below the cap grows as before",
			[]Column{{Title: "A", Min: 4, Max: 20}, {Title: "B", Min: 4}},
			20, []int{9, 9},
		},
		{
			"capped column hands space to the others",
			[]Column{{Title: "A", Min: 4, Max: 10}, {Title: "B", Min: 4, Flex: 1}, {Title: "C", Width: 3}},
			50, []int{10, 33, 3},
		},
		{
			"caps cascade",
			[]Column{{Title: "A", Min: 4, Max: 6}, {Title: "B", Min: 4, Max: 12}, {Title: "C", Min: 4, Flex: 2}},
			60, []int{6, 12, 38},
		},
		{
			"every flexible column capped pads at the right",
			[]Column{{Title: "A", Min: 4, Max: 8}, {Title: "B", Width: 5}, {Title: "C", Min: 4, Max: 10}},
			60, []int{8, 5, 10},
		},
		{
			"min above max keeps min",
			[]Column{{Title: "A", Min: 9, Max: 5}, {Title: "B", Min: 2}},
			30, []int{9, 19},
		},
		{
			"weighted shares below every cap",
			[]Column{{Title: "N", Min: 10, Max: 24}, {Title: "H", Min: 12, Max: 28}, {Title: "S", Min: 10, Flex: 4, Max: 60}, {Title: "T", Min: 10, Flex: 4, Max: 60}},
			160, []int{22, 24, 54, 54},
		},
		{
			"heavy columns saturate first, light ones follow",
			[]Column{{Title: "N", Min: 10, Max: 24}, {Title: "H", Min: 12, Max: 28}, {Title: "S", Min: 10, Flex: 4, Max: 60}, {Title: "T", Min: 10, Flex: 4, Max: 60}},
			180, []int{24, 28, 60, 60},
		},
	}
	for _, tc := range tests {
		got := ColumnWidths(tc.cols, tc.total)
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s: ColumnWidths(%d) = %v, want %v", tc.name, tc.total, got, tc.want)
		}
		for i, c := range tc.cols {
			if c.Max > 0 && got[i] > max(c.Max, c.Min) {
				t.Errorf("%s: column %d width %d exceeds Max %d", tc.name, i, got[i], c.Max)
			}
		}
	}
}

func TestTableCappedColumnsPadRight(t *testing.T) {
	th := theme.Default()
	cols := []Column{{Title: "NAME", Min: 4, Max: 6}, {Title: "V", Width: 3}}
	out := Table(th, TableOpts{Width: 30, Height: 3, Columns: cols, Rows: [][]string{{"alpha", "1"}}, Selected: 0})
	assertBlock(t, "Table capped", out, 30, 3)
	lines := strings.Split(ansi.Strip(out), "\n")
	if want := "NAME    V" + strings.Repeat(" ", 21); lines[0] != want {
		t.Errorf("header = %q, want %q", lines[0], want)
	}
	if want := "alpha   1" + strings.Repeat(" ", 21); lines[1] != want {
		t.Errorf("row = %q, want %q", lines[1], want)
	}
}

func TestScrollOffset(t *testing.T) {
	tests := []struct {
		n, visible, selected, offset, want int
	}{
		{10, 20, 5, 3, 0},
		{100, 10, 50, 0, 41},
		{100, 10, 5, 20, 5},
		{100, 10, 25, 20, 20},
		{100, 10, -1, 95, 90},
		{100, 10, 200, -4, 0},
		{100, 0, 3, 3, 0},
	}
	for _, tc := range tests {
		if got := ScrollOffset(tc.n, tc.visible, tc.selected, tc.offset); got != tc.want {
			t.Errorf("ScrollOffset(%d,%d,%d,%d) = %d, want %d", tc.n, tc.visible, tc.selected, tc.offset, got, tc.want)
		}
	}
}

func TestPanel(t *testing.T) {
	th := theme.Default()
	bodies := []string{"", "one line", "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk", th.Bad.Render("styled body that is pretty long indeed") + "\nnext"}
	for _, w := range testWidths {
		for _, h := range testHeights {
			for _, body := range bodies {
				for _, focused := range []bool{false, true} {
					o := PanelOpts{Width: w, Height: h, Title: "Network Interfaces", Info: "eth0 · 1Gb/s", Focused: focused, Body: body}
					assertBlock(t, "Panel", Panel(th, o), w, h)
					o.Title, o.Info = "", ""
					assertBlock(t, "Panel bare", Panel(th, o), w, h)
				}
			}
		}
	}
	got := ansi.Strip(Panel(th, PanelOpts{Width: 24, Height: 3, Title: "CPU", Info: "8c", Body: "x"}))
	want := "╭─ CPU " + strings.Repeat("─", 11) + " 8c ─╮\n" +
		"│ x" + strings.Repeat(" ", 20) + "│\n" +
		"╰" + strings.Repeat("─", 22) + "╯"
	if got != want {
		t.Errorf("Panel =\n%s\nwant\n%s", got, want)
	}
	got = strings.Split(ansi.Strip(Panel(th, PanelOpts{Width: 12, Height: 2, Title: "Processes", Info: "312"})), "\n")[0]
	if got != "╭─ Proce… ─╮" {
		t.Errorf("narrow panel top = %q", got)
	}
	focused := Panel(th, PanelOpts{Width: 10, Height: 3, Focused: true})
	if !strings.Contains(focused, ansiFG(th.P.BorderFocus)) {
		t.Errorf("focused panel must use the focus border color")
	}
	if w, h := PanelInner(3, 1); w != 0 || h != 0 {
		t.Errorf("PanelInner(3,1) = %d,%d", w, h)
	}
}

// ansiFG is the lipgloss truecolor foreground sequence fragment for c.
func ansiFG(c theme.RGB) string {
	return fmt.Sprintf("38;2;%d;%d;%d", c.R, c.G, c.B)
}

func TestBadge(t *testing.T) {
	th := theme.Default()
	got := Badge(th, "WARN", theme.ToneWarn)
	if ansi.Strip(got) != " WARN " {
		t.Errorf("Badge text = %q", ansi.Strip(got))
	}
	if !strings.Contains(got, "48;2;") {
		t.Errorf("Badge needs a background: %q", got)
	}
}

func TestLogo(t *testing.T) {
	th := theme.Default()
	w, h := LogoSize()
	assertBlock(t, "Logo", Logo(th), w, h)
	if w != 32 || h != 6 {
		t.Errorf("LogoSize = %d×%d", w, h)
	}
	if !strings.Contains(Logo(th), th.P.Accent.FG()) {
		t.Errorf("logo gradient should start at the accent color")
	}
}

func TestGradient(t *testing.T) {
	from, to := theme.Hex("#000000"), theme.Hex("#ffffff")
	if Gradient("", from, to) != "" {
		t.Errorf("empty gradient should be empty")
	}
	got := Gradient("ab c", from, to)
	if ansi.Strip(got) != "ab c" {
		t.Errorf("Gradient text = %q", ansi.Strip(got))
	}
	if !strings.HasPrefix(got, from.FG()) || !strings.Contains(got, to.FG()+"c") {
		t.Errorf("Gradient must run from %q to %q: %q", from.FG(), to.FG(), got)
	}
	if ansi.StringWidth(Gradient("\x1b[31mred\x1b[0m\nx", from, to)) != 5 {
		t.Errorf("Gradient should strip escapes and flatten lines")
	}
}

func TestOverlay(t *testing.T) {
	bg := Fit("aaaaaaaa\nbbbbbbbb\ncccccccc", 8, 3)
	tests := []struct {
		name string
		fg   string
		x, y int
		want string
	}{
		{"inside", "XY\nZW", 2, 1, "aaaaaaaa\nbbXYbbbb\nccZWcccc"},
		{"clipRight", "XYZ", 6, 0, "aaaaaaXY\nbbbbbbbb\ncccccccc"},
		{"clipLeft", "XYZ", -1, 2, "aaaaaaaa\nbbbbbbbb\nYZcccccc"},
		{"clipBottom", "1\n2\n3", 0, 2, "aaaaaaaa\nbbbbbbbb\n1ccccccc"},
		{"clipTop", "1\n2\n3", 7, -2, "aaaaaaa3\nbbbbbbbb\ncccccccc"},
		{"outside", "XY", 20, 0, "aaaaaaaa\nbbbbbbbb\ncccccccc"},
		{"wideFG", "界", 3, 0, "aaa界aaa\nbbbbbbbb\ncccccccc"},
		{"wideCutRight", "界", 7, 0, "aaaaaaa \nbbbbbbbb\ncccccccc"},
		{"wideCutLeft", "界Q", -1, 0, " Qaaaaaa\nbbbbbbbb\ncccccccc"},
	}
	for _, tc := range tests {
		got := Overlay(bg, tc.fg, tc.x, tc.y)
		if ansi.Strip(got) != tc.want {
			t.Errorf("%s: got\n%s\nwant\n%s", tc.name, ansi.Strip(got), tc.want)
		}
		assertBlock(t, "Overlay "+tc.name, got, 8, 3)
	}
}

func TestOverlayWideBackground(t *testing.T) {
	bg := "界界界界"
	got := Overlay(bg, "X", 1, 0)
	if ansi.Strip(got) != " X界界界" {
		t.Fatalf("cut wide bg char = %q", ansi.Strip(got))
	}
	got = Overlay(bg, "XY", 2, 0)
	if ansi.Strip(got) != "界XY界界" {
		t.Fatalf("aligned wide bg = %q", ansi.Strip(got))
	}
}

func TestOverlayRestoresStyle(t *testing.T) {
	red, blue := "\x1b[31m", "\x1b[44m"
	bg := red + blue + "0123456789" + "\x1b[0m"
	got := Overlay(bg, "\x1b[1mXX", 3, 0)
	left, rest, ok := strings.Cut(got, "XX")
	if !ok {
		t.Fatalf("overlay text missing: %q", got)
	}
	if !strings.HasSuffix(left, theme.Reset+"\x1b[1m") {
		t.Errorf("SGR must be reset before the overlay: %q", left)
	}
	if !strings.HasPrefix(rest, theme.Reset+red+blue+"56789") {
		t.Errorf("bg SGR must be restored after the overlay: %q", rest)
	}
	reset := "\x1b[0m"
	bg = red + "01" + reset + "2345" + blue + "6789"
	got = Overlay(bg, "XX", 3, 0)
	_, rest, _ = strings.Cut(got, "XX")
	if !strings.HasPrefix(rest, "5"+blue+"6789") {
		t.Errorf("state after a reset must not be re-emitted: %q", rest)
	}
}

func TestLayoutHelpers(t *testing.T) {
	for _, w := range testWidths {
		for _, h := range testHeights {
			assertBlock(t, "Blank", Blank(w, h), w, h)
			assertBlock(t, "Fit", Fit("héllo 界界\nworld\n\n\nmore\nlines\nthan\nneeded", w, h), w, h)
		}
		if w > 0 {
			for _, s := range []string{"", "x", "界", "a long line of text", "\x1b[1mbold\x1b[0m"} {
				assertBlock(t, "FitLine", FitLine(s, w), w, 1)
				assertBlock(t, "ClipLine", ClipLine(s, w), w, 1)
				assertBlock(t, "AlignRight", AlignRight(s, w), w, 1)
				assertBlock(t, "Center", Center(s, w), w, 1)
				assertBlock(t, "SpreadLine", SpreadLine(s, "right", w), w, 1)
			}
		}
	}
}

func TestAxisDuration(t *testing.T) {
	tests := []struct {
		age, interval time.Duration
		want          string
	}{
		{time.Minute, time.Minute, "1m"},
		{4 * time.Minute, time.Minute, "4m"},
		{60 * time.Second, 30 * time.Second, "60s"},
		{90 * time.Second, 30 * time.Second, "90s"},
		{3 * time.Hour, time.Hour, "3h"},
	}
	for _, tt := range tests {
		if got := axisDuration(tt.age, tt.interval); got != tt.want {
			t.Errorf("axisDuration(%v, %v) = %q, want %q", tt.age, tt.interval, got, tt.want)
		}
	}
}
