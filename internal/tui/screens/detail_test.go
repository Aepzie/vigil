package screens

import (
	"testing"

	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/tui/ui"
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
