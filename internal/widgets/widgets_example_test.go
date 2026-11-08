package widgets

import (
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/Aepzie/vigil/internal/format"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// wave returns n smooth pseudo-random samples between lo and hi.
func wave(n int, lo, hi, phase float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		f := float64(i)
		v := 0.5 + 0.3*math.Sin(f/7+phase) + 0.15*math.Sin(f/2.3+phase*3) + 0.05*math.Cos(f*1.7)
		out[i] = lo + (hi-lo)*clamp01(v)
	}
	return out
}

// TestPreview prints every widget at a sample size when run with -v, for
// eyeballing in a truecolor terminal. Set VIGIL_PREVIEW_PLAIN=1 to strip
// colors.
func TestPreview(t *testing.T) {
	if !testing.Verbose() {
		t.Skip("run with -v to print the widget preview")
	}
	th := theme.Default()
	show := func(name, block string) {
		if os.Getenv("VIGIL_PREVIEW_PLAIN") != "" {
			block = ansi.Strip(block)
		}
		fmt.Printf("── %s ──\n%s\n\n", name, block)
	}
	cpu := ChartOpts{
		Width: 72, Height: 12, Step: time.Second, Max: 100,
		YLabel: func(v float64) string { return format.Percent(v) },
		Series: []Series{
			{Name: "user", Values: wave(120, 5, 55, 0), Color: th.P.Accent},
			{Name: "system", Values: wave(120, 2, 20, 2), Color: th.P.Accent2},
		},
	}
	show("AreaChart", AreaChart(th, cpu))
	net := cpu
	net.Max, net.YLabel = 0, format.Bytes
	net.Series = []Series{
		{Name: "rx", Values: wave(200, 1e5, 9e6, 1), Color: th.P.Teal},
		{Name: "tx", Values: wave(200, 1e5, 4e6, 4), Color: th.P.Warn},
	}
	show("LineChart", LineChart(th, net))
	few := cpu
	few.Width, few.Height = 40, 6
	few.Series = []Series{{Values: wave(14, 10, 80, 1), Color: th.P.Good}}
	show("AreaChart right-aligned", AreaChart(th, few))
	compact := cpu
	compact.Width, compact.Height, compact.HideAxes = 30, 4, true
	show("AreaChart HideAxes", AreaChart(th, compact))
	show("Sparkline", Sparkline(wave(60, 0, 1, 2), 40, 0, th.P.Accent2))
	show("Progress", JoinV(
		Progress(th, 30, 0.42, th.P.Good),
		Progress(th, 30, 0.77, th.P.Warn),
		Progress(th, 30, 0.95, th.P.Bad),
	))
	show("StackedBar", StackedBar(th, 30, 16, []Segment{
		{Value: 5, Color: th.P.Accent}, {Value: 3, Color: th.P.Accent2}, {Value: 2, Color: th.P.Teal},
	}))
	show("BigNumber", JoinV(
		BigNumber("0123456789", th.P.Text),
		BigNumber("12.4k 87% -3,5", th.P.Accent),
		BigNumber("1.2KMGTB 9MB/s", th.P.Accent2),
	))
	show("Table", Table(th, TableOpts{
		Width: 60, Height: 6, Selected: 2,
		Columns: []Column{
			{Title: "PID", Width: 6, Align: AlignRightCol},
			{Title: "COMMAND", Min: 10, Flex: 2},
			{Title: "CPU", Width: 6, Align: AlignRightCol},
			{Title: "MEM", Width: 6, Align: AlignRightCol},
			{Title: "USER", Min: 6},
		},
		Rows: [][]string{
			{"1", "/sbin/init splash", "0.1%", "12M", "root"},
			{"412", "postgres: checkpointer", "1.4%", "88M", "postgres"},
			{"977", th.Good.Render("vigil") + " --listen :2222", "3.2%", "41M", "vigil"},
			{"1201", "nginx: worker process with a very long command line", "0.4%", "9M", "www-data"},
			{"2210", "sshd: craig@pts/0", "0.0%", "6M", "craig"},
			{"3001", "node server.js", "12%", "310M", "app"},
		},
	}))
	show("Table empty", Table(th, TableOpts{
		Width: 40, Height: 4, Selected: -1, Empty: "no processes match",
		Columns: []Column{{Title: "NAME"}, {Title: "STATE", Width: 8}},
	}))
	show("Panel", JoinH(
		Panel(th, PanelOpts{Width: 36, Height: 6, Title: "CPU", Info: "8 cores", Body: Sparkline(wave(40, 0, 1, 0), 32, 0, th.P.Accent)}),
		Panel(th, PanelOpts{Width: 36, Height: 6, Title: "Memory", Info: "16G", Focused: true, Body: Progress(th, 32, 0.6, th.P.Accent2)}),
	))
	show("Badge", Badge(th, "OK", theme.ToneGood)+" "+Badge(th, "WARN", theme.ToneWarn)+" "+Badge(th, "CRIT", theme.ToneBad))
	show("Logo", Logo(th))
	show("Gradient", Gradient("vigil — server monitoring over ssh", th.P.Accent, th.P.Accent2))
	bg := Panel(th, PanelOpts{Width: 50, Height: 8, Title: "Background", Body: "some text that will be partly covered\nby the overlay box drawn on top"})
	fg := Panel(th, PanelOpts{Width: 20, Height: 4, Title: "Modal", Focused: true, Body: "hello"})
	show("Overlay", Overlay(bg, fg, 20, 3))
}

func BenchmarkAreaChart(b *testing.B) {
	th := theme.Default()
	o := ChartOpts{
		Width: 200, Height: 20, Step: time.Second,
		Series: []Series{
			{Values: wave(400, 0, 50, 0), Color: th.P.Accent},
			{Values: wave(400, 0, 30, 2), Color: th.P.Accent2},
		},
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = AreaChart(th, o)
	}
}

func BenchmarkLineChart(b *testing.B) {
	th := theme.Default()
	o := ChartOpts{
		Width: 200, Height: 20, Step: time.Second,
		Series: []Series{
			{Values: wave(400, 0, 50, 0), Color: th.P.Teal},
			{Values: wave(400, 0, 30, 2), Color: th.P.Warn},
		},
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = LineChart(th, o)
	}
}
