package screens

import (
	"testing"

	"github.com/CraguS/vigil/internal/tui/ui"
)

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
