package screens

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/format"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
)

func newTestOverview(t *testing.T) *overview {
	t.Helper()
	s := newOverview(testDeps(t))
	s.Update(ui.FleetMsg{Fleet: fakeFleet()})
	return s
}

func TestOverviewNavigationBounds(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want string
	}{
		{"first by name by default", nil, "s2"},
		{"up stays at top", []string{"k", "up"}, "s2"},
		{"down moves", []string{"j"}, "s3"},
		{"down clamps at bottom", []string{"j", "j", "j", "j", "down", "j"}, "s1"},
		{"end then up", []string{"G", "k"}, "s4"},
		{"home", []string{"j", "j", "g"}, "s2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestOverview(t)
			for _, k := range tt.keys {
				send(s, k)
			}
			if s.selected != tt.want {
				t.Fatalf("selected %q, want %q", s.selected, tt.want)
			}
		})
	}
}

func TestOverviewSortCycleKeepsSelection(t *testing.T) {
	s := newTestOverview(t)
	if s.selected != "s2" {
		t.Fatalf("selected %q", s.selected)
	}
	send(s, "s")
	if s.sort != sortCPU {
		t.Fatalf("sort %v, want cpu", s.sort)
	}
	if s.selected != "s2" {
		t.Fatalf("selection changed to %q after sort", s.selected)
	}
	rows := s.rows()
	if rows[0].Srv.ID != "s2" || rows[1].Srv.ID != "s1" {
		t.Fatalf("cpu order wrong: %s, %s", rows[0].Srv.ID, rows[1].Srv.ID)
	}
	for range sortKeyCount - 1 {
		send(s, "s")
	}
	if s.sort != sortName {
		t.Fatalf("sort did not cycle back to name: %v", s.sort)
	}
}

func TestOverviewEnterOpensShell(t *testing.T) {
	s := newTestOverview(t)
	send(s, "j")
	_, cmd := send(s, "enter")
	msg, ok := find[ui.OpenShellMsg](collect(cmd))
	if !ok || msg.ServerID != "s3" {
		t.Fatalf("want OpenShellMsg{s3}, got %#v", collect(cmd))
	}
}

func TestOverviewDetailKeys(t *testing.T) {
	for _, key := range []string{"d", "space", "right"} {
		t.Run(key, func(t *testing.T) {
			s := newTestOverview(t)
			_, cmd := send(s, key)
			msg, ok := find[ui.ShowDetailMsg](collect(cmd))
			if !ok || msg.ServerID != "s2" {
				t.Fatalf("want ShowDetailMsg{s2}, got %#v", collect(cmd))
			}
		})
	}
}

func TestOverviewTilesNavigation(t *testing.T) {
	s := newTestOverview(t)
	send(s, "v")
	if s.mode != overviewTiles {
		t.Fatal("v did not switch to tiles")
	}
	s.View(80, 21)
	if s.tileCols != 2 {
		t.Fatalf("tileCols = %d, want 2 at 80 columns", s.tileCols)
	}
	send(s, "l")
	if s.selected != "s3" {
		t.Fatalf("l: selected %q", s.selected)
	}
	send(s, "j")
	if s.selected != "s1" {
		t.Fatalf("j in tiles moves a grid row: selected %q, want s1", s.selected)
	}
	send(s, "h")
	send(s, "k")
	if s.selected != "s2" {
		t.Fatalf("h,k: selected %q, want s2", s.selected)
	}
	_, cmd := send(s, "right")
	if cmd != nil {
		t.Fatal("right in tiles must navigate, not open detail")
	}
	send(s, "v")
	if s.mode != overviewList {
		t.Fatal("v did not switch back to list")
	}
}

func TestOverviewFilterCapturing(t *testing.T) {
	s := newTestOverview(t)
	if s.Capturing() {
		t.Fatal("capturing before /")
	}
	send(s, "/")
	if !s.Capturing() {
		t.Fatal("not capturing after /")
	}
	typeText(s, "web")
	if got := len(s.rows()); got != 1 {
		t.Fatalf("filter web: %d rows, want 1", got)
	}
	if s.selected != "s1" {
		t.Fatalf("selection not moved into filtered rows: %q", s.selected)
	}
	_, cmd := send(s, "enter")
	if cmd != nil || s.Capturing() {
		t.Fatal("enter should apply the filter and stop capturing")
	}
	if s.filter.Value() != "web" {
		t.Fatalf("filter lost: %q", s.filter.Value())
	}
	send(s, "esc")
	if s.filter.Value() != "" || len(s.rows()) != 4 {
		t.Fatal("esc should clear an applied filter")
	}

	send(s, "/")
	typeText(s, "prod")
	if got := len(s.rows()); got != 2 {
		t.Fatalf("tag filter prod: %d rows, want 2", got)
	}
	send(s, "esc")
	if s.Capturing() || s.filter.Value() != "" {
		t.Fatal("esc while typing should clear and stop capturing")
	}
}

func TestOverviewPollAndPalette(t *testing.T) {
	d := testDeps(t)
	var polled []string
	d.PollNow = func(id string) { polled = append(polled, id) }
	s := newOverview(d)
	s.Update(ui.FleetMsg{Fleet: fakeFleet()})
	_, cmd := send(s, "r")
	if _, ok := find[ui.ToastMsg](collect(cmd)); !ok || len(polled) != 1 || polled[0] != "s2" {
		t.Fatalf("poll: %v", polled)
	}

	for _, c := range s.Commands() {
		if c.Title != "Toggle tiles/list" {
			continue
		}
		for _, m := range collect(c.Run()) {
			s.Update(m)
		}
	}
	if s.mode != overviewTiles {
		t.Fatal("palette toggle did not switch to tiles")
	}
}

func TestOverviewEmptyFleet(t *testing.T) {
	s := newOverview(testDeps(t))
	s.Update(ui.FleetMsg{Fleet: emptyFleet()})
	for _, key := range []string{"j", "k", "enter", "d", "l", "r"} {
		if _, cmd := send(s, key); cmd != nil {
			if msgs := collect(cmd); len(msgs) > 0 {
				t.Fatalf("%s on empty fleet produced %#v", key, msgs)
			}
		}
	}
	if hints := s.Hints(); len(hints) == 0 {
		t.Fatal("empty overview has no hints")
	}
}

func TestOverviewPaste(t *testing.T) {
	s := newTestOverview(t)
	send(s, "/")
	s.Update(tea.PasteMsg{Content: "web\x1b[31m"})
	if got := s.filter.Value(); got != "web" {
		t.Fatalf("pasted filter %q, want sanitized %q", got, "web")
	}
}

// largeFleet extends fakeFleet with online servers up to n servers.
func largeFleet(n int) ui.Fleet {
	f := fakeFleet()
	f.Snaps = maps.Clone(f.Snaps)
	f.History = maps.Clone(f.History)
	for i := len(f.Servers); i < n; i++ {
		id := "x" + strconv.Itoa(i)
		f.Servers = append(f.Servers, store.Server{ID: id, Name: "node-" + strconv.Itoa(i), Host: "10.9.0." + strconv.Itoa(i), Port: 22, User: "ops"})
		f.Snaps[id] = onlineSnap(id, 30, 4, 50)
		f.History[id] = series(300, 30, 50)
	}
	return f
}

func overviewWith(t *testing.T, fleet ui.Fleet, keys ...string) *overview {
	t.Helper()
	s := newOverview(testDeps(t))
	s.Update(ui.FleetMsg{Fleet: fleet})
	for _, k := range keys {
		send(s, k)
	}
	return s
}

func TestOverviewFleetChartsOnlyWhenTall(t *testing.T) {
	tests := []struct {
		name   string
		fleet  ui.Fleet
		keys   []string
		w, h   int
		charts bool
	}{
		{"list 80x20", fakeFleet(), nil, 80, 20, false},
		{"list 80x40 is too narrow", fakeFleet(), nil, 80, 40, false},
		{"list 150x40", fakeFleet(), nil, 150, 40, true},
		{"list 220x56", fakeFleet(), nil, 220, 56, true},
		{"list fills a short screen", largeFleet(30), nil, 150, 40, false},
		{"tiles 80x20", largeFleet(9), []string{"v"}, 80, 20, false},
		{"tiles 150x40", largeFleet(9), []string{"v"}, 150, 40, true},
		{"tiles 220x56", largeFleet(9), []string{"v"}, 220, 56, true},
		{"filtering keeps the layout", fakeFleet(), []string{"/", "w"}, 150, 40, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := overviewWith(t, tt.fleet, tt.keys...)
			out := s.View(tt.w, tt.h)
			checkSize(t, out, tt.w, tt.h)
			plain := ansi.Strip(out)
			for _, title := range []string{"Fleet CPU", "Fleet network"} {
				if got := strings.Contains(plain, title); got != tt.charts {
					t.Fatalf("%q shown = %v, want %v:\n%s", title, got, tt.charts, plain)
				}
			}
		})
	}
}

func TestOverviewListSizedToFleetWhenTall(t *testing.T) {
	s := newTestOverview(t)
	lines := strings.Split(ansi.Strip(s.View(150, 40)), "\n")
	top := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "Servers") })
	charts := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "Fleet CPU") })
	if top < 0 || charts < 0 {
		t.Fatalf("missing panels:\n%s", strings.Join(lines, "\n"))
	}
	if got, want := charts-top, listMinRows+3; got != want {
		t.Fatalf("servers panel is %d rows, want %d for 4 servers", got, want)
	}
}

// cellIndex is the cell column at which sub starts in the plain line.
func cellIndex(line, sub string) int {
	i := strings.Index(line, sub)
	if i < 0 {
		return -1
	}
	return ansi.StringWidth(line[:i])
}

// blockRuns returns the start cell and length of every run of sparkline
// blocks in line.
func blockRuns(line string) [][2]int {
	var runs [][2]int
	col, start := 0, -1
	for _, r := range line + " " {
		if strings.ContainsRune("▁▂▃▄▅▆▇█", r) {
			if start < 0 {
				start = col
			}
		} else if start >= 0 {
			runs = append(runs, [2]int{start, col - start})
			start = -1
		}
		col += ansi.StringWidth(string(r))
	}
	return runs
}

func TestOverviewListWideColumns(t *testing.T) {
	for _, w := range []int{150, 220, 320} {
		t.Run(strconv.Itoa(w), func(t *testing.T) {
			s := newTestOverview(t)
			lines := strings.Split(ansi.Strip(s.View(w, 40)), "\n")
			hi := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "NAME") })
			ri := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "web-01") })
			if hi < 0 || ri < 0 {
				t.Fatalf("no header or row:\n%s", strings.Join(lines, "\n"))
			}
			header := lines[hi]
			if nameW := cellIndex(header, "HOST") - cellIndex(header, "NAME") - 2; nameW > 24 {
				t.Errorf("NAME is %d wide, want at most 24", nameW)
			}
			if hostW := cellIndex(header, "STATUS") - cellIndex(header, "HOST") - 2; hostW > 28 {
				t.Errorf("HOST is %d wide, want at most 28", hostW)
			}
			cpuStart, netStart := cellIndex(header, colCPUSpark), cellIndex(header, colNetSpark)
			cpuW := cellIndex(header, "RAM") + 3 - 5 - 2 - cpuStart
			netW := cellIndex(header, "LOAD") + 4 - 5 - 2 - netStart
			want := [][2]int{{cpuStart, cpuW}, {netStart, netW}}
			if got := blockRuns(lines[ri]); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("sparkline runs %v, want %v (cells must fill their columns)\n%s\n%s", got, want, header, lines[ri])
			}
			if w >= 220 && (cpuW <= sparkMinW || netW <= sparkMinW) {
				t.Errorf("sparklines did not grow: cpu %d, net %d", cpuW, netW)
			}
			if cpuW > sparkMaxW || netW > sparkMaxW {
				t.Errorf("sparklines exceed their cap: cpu %d, net %d", cpuW, netW)
			}
		})
	}
}

func TestNetSparklineFixedScale(t *testing.T) {
	values := make([]float64, 60)
	for i := range values {
		values[i] = 10e6 + float64(i%5)*2.5e6
	}
	c := theme.Default().P.Accent2
	got := ansi.Strip(netSparkline(values, time.Second, 60, c))
	if ansi.StringWidth(got) != 60 {
		t.Fatalf("width %d", ansi.StringWidth(got))
	}
	if strings.Contains(got, "█") {
		t.Fatalf("the peak must leave headroom: %q", got)
	}
	if !strings.Contains(got, "▄") || !strings.Contains(got, "▇") {
		t.Fatalf("10–20Mb/s on a 0–25Mb/s scale should span ▄..▇: %q", got)
	}
	if strings.ContainsAny(got, "▁▂") {
		t.Fatalf("the scale starts at zero, so 10Mb/s is not near the floor: %q", got)
	}
	if flat := ansi.Strip(netSparkline(make([]float64, 60), time.Second, 10, c)); flat != strings.Repeat("▁", 10) {
		t.Fatalf("idle network = %q", flat)
	}
}

func TestSparkValuesWindow(t *testing.T) {
	full := make([]float64, 300)
	for i := range full {
		full[i] = float64(i)
	}
	if got := sparkValues(full, time.Second, 60); len(got) != 60 || got[0] != 240 || got[59] != 299 {
		t.Fatalf("one sample per column: len %d, %v…%v", len(got), got[0], got[len(got)-1])
	}
	if got := sparkValues(full, time.Second, 10); len(got) != 10 || got[0] != 242.5 || got[9] != 296.5 {
		t.Fatalf("60s into 10 columns: %v", got)
	}
	short := sparkValues([]float64{1, 2, 3}, time.Second, 20)
	if len(short) != 20 || !math.IsNaN(short[18]) || short[19] != 2 {
		t.Fatalf("short history keeps the time scale: %v", short)
	}
	if got := sparkValues(full, 2*time.Second, 60); len(got) != 30 {
		t.Fatalf("2s steps: %d values, want 30", len(got))
	}
}

func TestOverviewTilesScrollHint(t *testing.T) {
	s := overviewWith(t, largeFleet(9), "v")
	out := s.View(80, 20)
	checkSize(t, out, 80, 20)
	if !strings.Contains(ansi.Strip(out), "1–4 of 9 "+glyphDown) {
		t.Fatalf("missing scroll hint:\n%s", ansi.Strip(out))
	}
	send(s, "G")
	if out := ansi.Strip(s.View(80, 20)); !strings.Contains(out, glyphUp+" 7–9 of 9") {
		t.Fatalf("missing scroll hint at the end:\n%s", out)
	}
}

func TestOverviewTileFooterDropsWholeFacts(t *testing.T) {
	s := overviewWith(t, largeFleet(9), "v")
	lat, load, up := "lat 12ms", "load 0.42", "up "+format.Duration(49*time.Hour+12*time.Minute)
	valid := []string{lat, lat + glyphSep + load, lat + glyphSep + load + glyphSep + up}
	for _, w := range []int{80, 110, 150, 220} {
		for _, l := range strings.Split(ansi.Strip(s.View(w, 40)), "\n") {
			for _, seg := range strings.Split(l, "│") {
				seg = strings.TrimSpace(seg)
				if strings.HasPrefix(seg, "lat ") && !slices.Contains(valid, seg) {
					t.Fatalf("tile footer cut mid-fact at width %d: %q", w, seg)
				}
			}
		}
	}
}
