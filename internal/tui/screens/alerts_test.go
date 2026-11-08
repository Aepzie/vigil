package screens

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/alerts"
	"github.com/Aepzie/vigil/internal/tui/ui"
)

// alertsFleet adds an active disk alert with a mount subject to fakeFleet.
func alertsFleet() ui.Fleet {
	f := fakeFleet()
	f.Active = append(slices.Clone(f.Active), alerts.Alert{
		ID: "a3", ServerID: "s1", ServerName: "web-01", Kind: alerts.KindDisk, Subject: "/data",
		Severity: alerts.SeverityWarn, Message: "Disk /data 91% ≥ 90%", Value: 91, Threshold: 90,
		Since: testNow.Add(-time.Minute),
	})
	return f
}

// headerCells returns the column titles of the first table header line
// that contains SEV.
func headerCells(t *testing.T, view string) []string {
	t.Helper()
	for _, l := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(l, "SEV") {
			return strings.Fields(strings.Trim(l, "│ "))
		}
	}
	t.Fatalf("no table header in:\n%s", ansi.Strip(view))
	return nil
}

func TestAlertsColumnsByWidth(t *testing.T) {
	tests := []struct {
		width int
		want  []string
	}{
		{150, []string{"SEV", "SERVER", "KIND", "MESSAGE", "VALUE", "SINCE", "ACK"}},
		{80, []string{"SEV", "SERVER", "KIND", "MESSAGE", "SINCE", "ACK"}},
		{60, []string{"SEV", "SERVER", "MESSAGE"}},
	}
	for _, tt := range tests {
		s := newAlerts(testDeps(t))
		s.Update(ui.FleetMsg{Fleet: alertsFleet()})
		view := s.View(tt.width, 21)
		checkSize(t, view, tt.width, 21)
		if got := headerCells(t, view); !slices.Equal(got, tt.want) {
			t.Errorf("width %d: columns %v, want %v", tt.width, got, tt.want)
		}
	}
}

func TestAlertsTableAt80(t *testing.T) {
	s := newAlerts(testDeps(t))
	s.Update(ui.FleetMsg{Fleet: alertsFleet()})
	plain := ansi.Strip(s.View(80, 21))
	for _, want := range []string{"unreachable: connection refused", "Disk /data 91% ≥ 90%", "CPU at 97% for 2m"} {
		if !strings.Contains(plain, want) {
			t.Errorf("message %q truncated:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "disk /data") {
		t.Errorf("KIND repeats the subject:\n%s", plain)
	}
	if !strings.Contains(plain, " disk ") {
		t.Errorf("KIND missing:\n%s", plain)
	}
}

func TestAlertsRecentResolvedTable(t *testing.T) {
	for _, size := range [][2]int{{80, 21}, {150, 41}} {
		s := newTestAlerts(t)
		view := s.View(size[0], size[1])
		checkSize(t, view, size[0], size[1])
		plain := ansi.Strip(view)
		for _, want := range []string{"Recent / resolved", "RESOLVED", "LASTED", "disk / at 86%", "50m ago", "10m"} {
			if !strings.Contains(plain, want) {
				t.Errorf("%dx%d: resolved table lacks %q:\n%s", size[0], size[1], want, plain)
			}
		}
		if strings.Contains(plain, "Nothing resolved recently") {
			t.Errorf("%dx%d: resolved alert shown as empty", size[0], size[1])
		}
	}
}

func TestAlertValue(t *testing.T) {
	tests := []struct {
		value, threshold float64
		want             string
	}{
		{93.4, 80, "93% ≥ 80%"},
		{80, 80, "80% ≥ 80%"},
		{77.2, 80, "77% < 80%"},
		{79.6, 80, "79% < 80%"},
		{85.58, 85.5, "85.5% ≥ 85.5%"},
		{85.46, 85.5, "85.4% < 85.5%"},
	}
	for _, tt := range tests {
		a := alerts.Alert{Kind: alerts.KindCPU, Value: tt.value, Threshold: tt.threshold}
		if got := alertValue(a); got != tt.want {
			t.Errorf("alertValue(%v, %v) = %q, want %q", tt.value, tt.threshold, got, tt.want)
		}
	}
	if got := alertValue(alerts.Alert{Kind: alerts.KindOffline}); got != "" {
		t.Errorf("offline value = %q", got)
	}
}

func newTestAlerts(t *testing.T) *alertsScreen {
	t.Helper()
	s := newAlerts(testDeps(t))
	s.Update(ui.FleetMsg{Fleet: fakeFleet()})
	return s
}

func TestAlertsNavigation(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want string
	}{
		{"first active", nil, "a:a1"},
		{"up clamps", []string{"k"}, "a:a1"},
		{"second active", []string{"j"}, "a:a2"},
		{"into resolved", []string{"j", "j"}, "r:a0"},
		{"down clamps", []string{"j", "j", "j", "j"}, "r:a0"},
		{"end", []string{"G"}, "r:a0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestAlerts(t)
			for _, k := range tt.keys {
				send(s, k)
			}
			if s.selected != tt.want {
				t.Fatalf("selected %q, want %q", s.selected, tt.want)
			}
		})
	}
}

func TestAlertsAck(t *testing.T) {
	s := newTestAlerts(t)
	_, cmd := send(s, "a")
	msg, ok := find[alertAckedMsg](collect(cmd))
	if !ok || msg.count != 1 || msg.label != "cpu on db-01" {
		t.Fatalf("got %#v", collect(cmd))
	}
	_, cmd = s.Update(msg)
	out := collect(cmd)
	if _, ok := find[ui.RefreshMsg](out); !ok {
		t.Fatal("no refresh after ack")
	}
	if toast, ok := find[ui.ToastMsg](out); !ok || toast.Text != "Acknowledged cpu on db-01" {
		t.Fatalf("toast %#v", toast)
	}

	send(s, "j")
	if _, cmd := send(s, "a"); cmd != nil {
		t.Fatal("acked alert must not be acknowledged again")
	}
	send(s, "j")
	if _, cmd := send(s, "a"); cmd != nil {
		t.Fatal("resolved alert cannot be acknowledged")
	}

	_, cmd = send(s, "A")
	all, ok := find[alertAckedMsg](collect(cmd))
	if !ok || all.count != 1 {
		t.Fatalf("ack all: %#v", collect(cmd))
	}
}

func TestAlertsEnterShowsServer(t *testing.T) {
	s := newTestAlerts(t)
	send(s, "j")
	send(s, "j")
	_, cmd := send(s, "enter")
	msg, ok := find[ui.ShowDetailMsg](collect(cmd))
	if !ok || msg.ServerID != "s1" {
		t.Fatalf("want ShowDetailMsg{s1}, got %#v", collect(cmd))
	}
}

func TestAlertsSelectionFollowsID(t *testing.T) {
	s := newTestAlerts(t)
	send(s, "j")
	fleet := fakeFleet()
	fleet.Active[0], fleet.Active[1] = fleet.Active[1], fleet.Active[0]
	s.Update(ui.FleetMsg{Fleet: fleet})
	if s.selected != "a:a2" {
		t.Fatalf("selection jumped to %q", s.selected)
	}
	fleet.Active = fleet.Active[1:]
	s.Update(ui.FleetMsg{Fleet: fleet})
	if s.selected != "a:a1" {
		t.Fatalf("vanished selection should fall back to first, got %q", s.selected)
	}
}

func TestAlertsEmpty(t *testing.T) {
	s := newAlerts(testDeps(t))
	s.Update(ui.FleetMsg{Fleet: emptyFleet()})
	for _, k := range []string{"j", "a", "enter"} {
		if _, cmd := send(s, k); cmd != nil {
			t.Fatalf("%s on empty alerts returned a command", k)
		}
	}
	if s.Hints() != nil {
		t.Fatal("no hints expected without alerts")
	}
}
