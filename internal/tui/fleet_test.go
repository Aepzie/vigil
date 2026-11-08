package tui

import (
	"testing"

	"github.com/CraguS/vigil/internal/alerts"
	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/store"
	"github.com/CraguS/vigil/internal/theme"
	"github.com/CraguS/vigil/internal/tui/ui"
)

func TestAlertTone(t *testing.T) {
	warn := alerts.Alert{Severity: alerts.SeverityWarn}
	crit := alerts.Alert{Severity: alerts.SeverityCrit}
	resolvedCrit := alerts.Alert{Severity: alerts.SeverityCrit, Resolved: true}
	tests := []struct {
		name      string
		active    []alerts.Alert
		wantTone  theme.Tone
		wantLabel string
	}{
		{"none", nil, theme.ToneGood, "OK"},
		{"warn only", []alerts.Alert{warn, warn}, theme.ToneWarn, "WARN"},
		{"any crit", []alerts.Alert{warn, crit}, theme.ToneBad, "ALARM"},
		{"resolved ignored", []alerts.Alert{resolvedCrit}, theme.ToneGood, "OK"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tone := alertTone(tt.active)
			if tone != tt.wantTone || statusLabel(tone) != tt.wantLabel {
				t.Fatalf("tone %v label %q, want %v %q", tone, statusLabel(tone), tt.wantTone, tt.wantLabel)
			}
		})
	}
}

func TestEventToast(t *testing.T) {
	tests := []struct {
		name     string
		ev       alerts.Event
		wantTone theme.Tone
		wantText string
	}{
		{
			name: "cpu warn fired",
			ev: alerts.Event{Type: alerts.EventFired, Alert: alerts.Alert{
				ServerName: "web-01", Kind: alerts.KindCPU, Severity: alerts.SeverityWarn, Value: 93, Threshold: 80,
			}},
			wantTone: theme.ToneWarn, wantText: "web-01 · CPU 93% ≥ 80%",
		},
		{
			name: "disk crit escalated",
			ev: alerts.Event{Type: alerts.EventEscalated, Alert: alerts.Alert{
				ServerName: "db-01", Kind: alerts.KindDisk, Subject: "/var", Severity: alerts.SeverityCrit, Value: 96, Threshold: 95,
			}},
			wantTone: theme.ToneBad, wantText: "db-01 · DISK /var 96% ≥ 95%",
		},
		{
			name: "memory",
			ev: alerts.Event{Type: alerts.EventFired, Alert: alerts.Alert{
				ServerName: "a", Kind: alerts.KindMem, Value: 90, Threshold: 85,
			}},
			wantTone: theme.ToneWarn, wantText: "a · MEM 90% ≥ 85%",
		},
		{
			name: "offline",
			ev: alerts.Event{Type: alerts.EventFired, Alert: alerts.Alert{
				ServerName: "edge", Kind: alerts.KindOffline, Severity: alerts.SeverityCrit,
			}},
			wantTone: theme.ToneBad, wantText: "edge · offline",
		},
		{
			name: "resolved",
			ev: alerts.Event{Type: alerts.EventResolved, Alert: alerts.Alert{
				ServerName: "web-01", Kind: alerts.KindCPU,
			}},
			wantTone: theme.ToneGood, wantText: "web-01 · cpu resolved",
		},
		{
			name: "sanitized name falls back to id",
			ev: alerts.Event{Type: alerts.EventResolved, Alert: alerts.Alert{
				ServerName: "\x1b[2J", ServerID: "srv-1", Kind: alerts.KindDisk, Subject: "/",
			}},
			wantTone: theme.ToneGood, wantText: "srv-1 · disk / resolved",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tone, text := eventToast(tt.ev)
			if tone != tt.wantTone || text != tt.wantText {
				t.Fatalf("got %v %q, want %v %q", tone, text, tt.wantTone, tt.wantText)
			}
		})
	}
}

func TestCountFleet(t *testing.T) {
	f := ui.Fleet{
		Servers: []store.Server{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}},
		Snaps: map[string]metrics.Snapshot{
			"a": {State: metrics.StateOnline},
			"b": {State: metrics.StateOnline},
			"c": {State: metrics.StateOffline},
		},
	}
	if got := countFleet(f); got != (fleetCounts{online: 2, offline: 1, pending: 1}) {
		t.Fatalf("counts = %+v", got)
	}
}

func TestTabIndexForKey(t *testing.T) {
	tests := []struct {
		key  string
		n    int
		want int
		ok   bool
	}{
		{"1", 5, 0, true},
		{"5", 5, 4, true},
		{"6", 5, 5, false},
		{"0", 5, 0, false},
		{"a", 5, 0, false},
		{"12", 5, 0, false},
	}
	for _, tt := range tests {
		got, ok := tabIndexForKey(tt.key, tt.n)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("tabIndexForKey(%q) = %d %v", tt.key, got, ok)
		}
	}
}
