package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Aepzie/vigil/internal/alerts"
	"github.com/Aepzie/vigil/internal/format"
	"github.com/Aepzie/vigil/internal/metrics"
	"github.com/Aepzie/vigil/internal/sanitize"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
)

const (
	// tickInterval is how often the fleet is rebuilt and broadcast.
	tickInterval = time.Second
	// recentAlerts is how many alerts (including resolved) a fleet holds.
	recentAlerts = 100
)

// tickMsg drives the once-per-second refresh and the header clock.
type tickMsg time.Time

// fleetBuiltMsg carries a fleet built off the update loop together with
// the alert events that happened after seq.
type fleetBuiltMsg struct {
	fleet  ui.Fleet
	events []alerts.Event
	seq    uint64
}

// tickCmd schedules the next tickMsg.
func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// fleetCmd builds the fleet and collects alert events newer than seq in a
// command, so the work never runs inside Update.
func fleetCmd(d *ui.Deps, seq uint64) tea.Cmd {
	return func() tea.Msg { return collectFleet(d, time.Now(), seq) }
}

// collectFleet builds the fleet and the alert events newer than seq.
func collectFleet(d *ui.Deps, now time.Time, seq uint64) fleetBuiltMsg {
	msg := fleetBuiltMsg{fleet: buildFleet(d, now), seq: seq}
	if d.Alerts != nil {
		msg.events, msg.seq = d.Alerts.EventsSince(seq)
	}
	return msg
}

// buildFleet copies everything a frame needs from the shared
// dependencies. Missing dependencies yield empty data.
func buildFleet(d *ui.Deps, now time.Time) ui.Fleet {
	f := ui.Fleet{
		Now:      now,
		Snaps:    map[string]metrics.Snapshot{},
		History:  map[string]metrics.Series{},
		Settings: store.DefaultSettings(),
	}
	if d.Store != nil {
		f.Servers = d.Store.Servers()
		f.Settings = d.Store.Settings()
	}
	if d.Source != nil {
		for _, srv := range f.Servers {
			if snap, ok := d.Source.Snapshot(srv.ID); ok {
				f.Snaps[srv.ID] = snap
			}
			if hist, ok := d.Source.History(srv.ID); ok {
				f.History[srv.ID] = hist
			}
		}
	}
	if d.Alerts != nil {
		f.Active = d.Alerts.Active()
		f.Recent = d.Alerts.Recent(recentAlerts)
	}
	return f
}

// fleetCounts summarizes server reachability for the header.
type fleetCounts struct {
	online  int
	offline int
	pending int
}

// countFleet classifies every server by its latest snapshot; servers
// without a snapshot count as pending.
func countFleet(f ui.Fleet) fleetCounts {
	var c fleetCounts
	for _, srv := range f.Servers {
		switch f.Snaps[srv.ID].State {
		case metrics.StateOnline:
			c.online++
		case metrics.StateOffline:
			c.offline++
		default:
			c.pending++
		}
	}
	return c
}

// alertTone is the overall fleet status: good without active alerts,
// warn with only warnings and bad with any critical alert.
func alertTone(active []alerts.Alert) theme.Tone {
	tone := theme.ToneGood
	for _, a := range active {
		if a.Resolved {
			continue
		}
		if a.Severity == alerts.SeverityCrit {
			return theme.ToneBad
		}
		tone = theme.ToneWarn
	}
	return tone
}

// activeCount counts unresolved alerts.
func activeCount(active []alerts.Alert) int {
	n := 0
	for _, a := range active {
		if !a.Resolved {
			n++
		}
	}
	return n
}

// statusLabel is the header badge text for a tone from alertTone.
func statusLabel(tone theme.Tone) string {
	switch tone {
	case theme.ToneBad:
		return "ALARM"
	case theme.ToneWarn:
		return "WARN"
	default:
		return "OK"
	}
}

// eventToast turns an alert event into a toast tone and text, e.g.
// "web-01 · CPU 93% ≥ 80%" or "web-01 · cpu resolved".
func eventToast(ev alerts.Event) (theme.Tone, string) {
	a := ev.Alert
	name := sanitize.Line(a.ServerName)
	if name == "" {
		name = sanitize.Line(a.ServerID)
	}
	subject := sanitize.Line(a.Subject)
	if ev.Type == alerts.EventResolved {
		return theme.ToneGood, joinWords(name+" ·", a.Kind.String(), subject, "resolved")
	}
	tone := theme.ToneWarn
	if a.Severity == alerts.SeverityCrit {
		tone = theme.ToneBad
	}
	if a.Kind == alerts.KindOffline {
		return tone, name + " · offline"
	}
	label := strings.ToUpper(a.Kind.String())
	if a.Kind == alerts.KindMem {
		label = "MEM"
	}
	cond := format.Percent(a.Value) + " ≥ " + format.Percent(a.Threshold)
	return tone, joinWords(name+" ·", label, subject, cond)
}

// joinWords joins the non-empty words with single spaces.
func joinWords(words ...string) string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if w != "" {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}
