package screens

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/CraguS/vigil/internal/tui/ui"
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
