package screens

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/metrics"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
)

func newTestDetail(t *testing.T) *detail {
	t.Helper()
	s := newDetail(testDeps(t))
	s.Update(ui.FleetMsg{Fleet: fakeFleet()})
	return s
}

func TestDetailSelection(t *testing.T) {
	tests := []struct {
		name string
		show string
		keys []string
		want string
	}{
		{"defaults to first by name", "", nil, "s2"},
		{"show detail selects", "s1", nil, "s1"},
		{"next", "s2", []string{"]"}, "s3"},
		{"prev wraps", "s2", []string{"["}, "s1"},
		{"next wraps", "s1", []string{"]"}, "s2"},
		{"unknown id falls back to first", "nope", nil, "s2"},
		{"step from unknown starts at first", "nope", []string{"]"}, "s3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestDetail(t)
			if tt.show != "" {
				s.Update(ui.ShowDetailMsg{ServerID: tt.show})
			}
			for _, k := range tt.keys {
				send(s, k)
			}
			if got := s.selectedID(); got != tt.want {
				t.Fatalf("selected %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetailEnterOpensShell(t *testing.T) {
	s := newTestDetail(t)
	s.Update(ui.ShowDetailMsg{ServerID: "s3"})
	_, cmd := send(s, "enter")
	msg, ok := find[ui.OpenShellMsg](collect(cmd))
	if !ok || msg.ServerID != "s3" {
		t.Fatalf("want OpenShellMsg{s3}, got %#v", collect(cmd))
	}
}

func TestDetailKeepsLastKnownValues(t *testing.T) {
	s := newTestDetail(t)
	fleet := fakeFleet()
	offline := fleet.Snaps["s1"]
	offline.State = metrics.StateOffline
	offline.Err = "i/o timeout"
	offline.CPU = metrics.CPU{}
	fleet.Snaps = map[string]metrics.Snapshot{"s1": offline}
	s.Update(ui.FleetMsg{Fleet: fleet})
	s.Update(ui.ShowDetailMsg{ServerID: "s1"})

	row, _ := s.current()
	v := s.resolve(row)
	if !v.metrics || !v.dim {
		t.Fatalf("want stale metrics, got metrics=%v dim=%v", v.metrics, v.dim)
	}
	if v.snap.CPU.Total != 23.5 {
		t.Fatalf("stale CPU %v, want last known 23.5", v.snap.CPU.Total)
	}
	checkSize(t, s.View(80, 21), 80, 21)

	fleet.Servers = fleet.Servers[1:]
	s.Update(ui.FleetMsg{Fleet: fleet})
	if _, ok := s.lastGood["s1"]; ok {
		t.Fatal("cache kept a removed server")
	}
}

func TestDetailNoMetricsForNeverPolled(t *testing.T) {
	s := newTestDetail(t)
	s.Update(ui.ShowDetailMsg{ServerID: "s4"})
	row, _ := s.current()
	if v := s.resolve(row); v.metrics {
		t.Fatal("never polled server must not claim metrics")
	}
}

// detailSizes are content areas (terminal minus four chrome rows) of
// common terminal sizes: 80×24, 100×30, 150×44 and 220×60.
var detailSizes = [][2]int{{80, 20}, {100, 26}, {150, 40}, {220, 56}}

// longHistoryFleet is fakeFleet with 15 minutes of 1s history for the
// online servers.
func longHistoryFleet() ui.Fleet {
	f := fakeFleet()
	f.History["s1"] = series(900, 23, 41)
	f.History["s2"] = series(900, 90, 88)
	return f
}

func TestDetailViewExactSizePopulated(t *testing.T) {
	for _, id := range []string{"s1", "s2", "s3", "s4"} {
		for _, keys := range [][]string{nil, {"w"}, {"w", "w"}, {"c"}} {
			s := newDetail(testDeps(t))
			s.Update(ui.FleetMsg{Fleet: longHistoryFleet()})
			s.Update(ui.ShowDetailMsg{ServerID: id})
			for _, k := range keys {
				send(s, k)
			}
			for _, sz := range append(detailSizes, [2]int{60, 14}, [2]int{40, 10}, [2]int{120, 32}) {
				t.Run(fmt.Sprintf("%s/%v/%dx%d", id, keys, sz[0], sz[1]), func(t *testing.T) {
					checkSize(t, s.View(sz[0], sz[1]), sz[0], sz[1])
				})
			}
		}
	}
}

func TestDetailShowsAllChartsAtMinimumSize(t *testing.T) {
	for _, id := range []string{"s1", "s2"} {
		s := newTestDetail(t)
		s.Update(ui.ShowDetailMsg{ServerID: id})
		text := ansi.Strip(s.View(80, 20))
		for _, title := range []string{"╭─ Now ", "╭─ CPU ", "╭─ Memory ", "╭─ Network ", "╭─ Disks "} {
			if !strings.Contains(text, title) {
				t.Errorf("%s at 80×20: missing panel %q in\n%s", id, title, text)
			}
		}
		if s.cycling {
			t.Errorf("%s at 80×20: all charts fit, chart cycling must be off", id)
		}
	}
}

func TestDetailLayoutGivesChartsTheRest(t *testing.T) {
	tests := []struct {
		w, h          int
		minChartH     int
		wantProcesses bool
	}{
		{220, 52, 38, true},
		{150, 36, 22, true},
		{100, 22, 14, true},
		{80, 18, 14, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dx%d", tt.w, tt.h), func(t *testing.T) {
			s := newTestDetail(t)
			s.Update(ui.ShowDetailMsg{ServerID: "s1"})
			row, _ := s.current()
			p := s.plan(s.resolve(row), tt.w, tt.h)
			if p.chartH+p.res.height != tt.h {
				t.Fatalf("charts %d + resources %d != %d", p.chartH, p.res.height, tt.h)
			}
			if p.chartH < tt.minChartH {
				t.Errorf("chart band %d rows, want at least %d", p.chartH, tt.minChartH)
			}
			if p.chartRows == 0 {
				t.Error("all three charts should fit")
			}
			if p.res.disks == 0 {
				t.Error("disks summary dropped")
			}
			if got := p.res.procs > 0; got != tt.wantProcesses {
				t.Errorf("processes shown = %v, want %v", got, tt.wantProcesses)
			}
			if p.res.procs > len(row.Snap.Procs)+3 && p.res.cores+p.res.disks < p.res.procs {
				t.Errorf("processes panel %d rows for %d processes", p.res.procs, len(row.Snap.Procs))
			}
		})
	}
}

func TestDetailWindowCycling(t *testing.T) {
	s := newDetail(testDeps(t))
	s.Update(ui.FleetMsg{Fleet: longHistoryFleet()})
	s.Update(ui.ShowDetailMsg{ServerID: "s1"})
	for _, want := range []string{"5m", "15m", "1m", "5m"} {
		if got := windowLabel(historyWindows[s.window]); got != want {
			t.Fatalf("window %s, want %s", got, want)
		}
		text := ansi.Strip(s.View(150, 40))
		if !strings.Contains(text, glyphSep+want+" ─") {
			t.Fatalf("window %s not shown in the chart info:\n%s", want, text)
		}
		send(s, "w")
	}
}

func TestDetailWindowClampedToHistory(t *testing.T) {
	s := newTestDetail(t)
	s.Update(ui.ShowDetailMsg{ServerID: "s1"})
	send(s, "w")
	if historyWindows[s.window] != 15*time.Minute {
		t.Fatalf("window %v, want 15m", historyWindows[s.window])
	}
	text := ansi.Strip(s.View(150, 40))
	if strings.Contains(text, "15m") || !strings.Contains(text, glyphSep+"5m ─") {
		t.Fatalf("15m over 5m of history must show the available 5m:\n%s", text)
	}
}

func TestDetailWindowCommands(t *testing.T) {
	s := newTestDetail(t)
	for i, d := range historyWindows {
		title := "Chart window " + windowLabel(d)
		var run func() tea.Cmd
		for _, c := range s.Commands() {
			if c.Title == title {
				run = c.Run
			}
		}
		if run == nil {
			t.Fatalf("missing palette command %q", title)
		}
		for _, msg := range collect(run()) {
			s.Update(msg)
		}
		if s.window != i {
			t.Fatalf("%q selected window %d, want %d", title, s.window, i)
		}
	}
}

func hintKeys(hints []ui.Hint) []string {
	keys := make([]string, len(hints))
	for i, h := range hints {
		keys[i] = h.Key
	}
	return keys
}

func TestDetailChartCycling(t *testing.T) {
	s := newTestDetail(t)
	s.Update(ui.ShowDetailMsg{ServerID: "s1"})
	for _, want := range []string{"CPU 1/3", "Memory 2/3", "Network 3/3", "CPU 1/3"} {
		text := ansi.Strip(s.View(60, 14))
		if !strings.Contains(text, "╭─ "+want+" ") {
			t.Fatalf("want single chart %q:\n%s", want, text)
		}
		if !slices.Contains(hintKeys(s.Hints()), "c") {
			t.Fatalf("single chart layout must hint c, got %v", hintKeys(s.Hints()))
		}
		send(s, "c")
	}
	s.View(150, 40)
	if slices.Contains(hintKeys(s.Hints()), "c") {
		t.Fatal("c hint shown although all charts fit")
	}
	if !slices.Contains(hintKeys(s.Hints()), "w") {
		t.Fatal("missing w hint")
	}
}

func TestFitFields(t *testing.T) {
	th := theme.Default()
	fields := []rankedField{{"alpha", 0}, {"bravo", 3}, {"charlie", 1}, {"", 0}, {"delta", 2}}
	sep := ansi.StringWidth(glyphSep)
	tests := []struct {
		w    int
		want string
	}{
		{100, "alpha" + glyphSep + "bravo" + glyphSep + "charlie" + glyphSep + "delta"},
		{5 + 7 + 5 + 2*sep, "alpha" + glyphSep + "charlie" + glyphSep + "delta"},
		{5 + 7 + sep, "alpha" + glyphSep + "charlie"},
		{6, "alpha"},
		{4, "alp…"},
	}
	for _, tt := range tests {
		got := ansi.Strip(fitFields(th, tt.w, fields))
		if got != tt.want {
			t.Errorf("fitFields(%d) = %q, want %q", tt.w, got, tt.want)
		}
		if ansi.StringWidth(got) > tt.w {
			t.Errorf("fitFields(%d) is %d cells wide", tt.w, ansi.StringWidth(got))
		}
	}
}

func TestDetailHeaderNeverHardClips(t *testing.T) {
	s := newTestDetail(t)
	s.Update(ui.ShowDetailMsg{ServerID: "s2"})
	for _, w := range []int{80, 90, 100, 120} {
		for _, h := range []int{20, 26} {
			lines := strings.Split(ansi.Strip(s.View(w, h)), "\n")
			header := lines[:2]
			if h >= detailFullHeaderMinH {
				header = lines[1:3]
			}
			for _, l := range header {
				if strings.Contains(l, "polle ") || strings.HasSuffix(strings.TrimRight(l, " │"), "·") {
					t.Errorf("%d×%d: header line clipped: %q", w, h, l)
				}
			}
		}
	}
}

func TestCoreRows(t *testing.T) {
	tests := []struct {
		n, iw, maxRows, want int
	}{
		{4, 60, 10, 2},
		{32, 60, 20, 11},
		{32, 60, 10, 10},
		{32, 60, 4, 4},
		{1, 10, 0, 1},
		{0, 60, 5, 1},
	}
	for _, tt := range tests {
		if got := coreRows(tt.n, tt.iw, tt.maxRows); got != tt.want {
			t.Errorf("coreRows(%d, %d, %d) = %d, want %d", tt.n, tt.iw, tt.maxRows, got, tt.want)
		}
	}
}
