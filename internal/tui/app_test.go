package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Aepzie/vigil/internal/alerts"
	"github.com/Aepzie/vigil/internal/compat"
	"github.com/Aepzie/vigil/internal/metrics"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
)

func TestGlobalKeys(t *testing.T) {
	tests := []struct {
		name       string
		keys       []string
		wantActive int
		wantQuit   bool
	}{
		{name: "tab next", keys: []string{"tab"}, wantActive: 1},
		{name: "tab wraps", keys: []string{"5", "tab"}, wantActive: 0},
		{name: "shift+tab wraps", keys: []string{"shift+tab"}, wantActive: 4},
		{name: "digit", keys: []string{"4"}, wantActive: 3},
		{name: "digit out of range", keys: []string{"2", "9"}, wantActive: 1},
		{name: "q quits", keys: []string{"q"}, wantQuit: true},
		{name: "ctrl+c quits", keys: []string{"ctrl+c"}, wantQuit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, Options{SkipSplash: true})
			var cmd tea.Cmd
			for _, k := range tt.keys {
				cmd = f.send(key(k))
			}
			if f.m.active != tt.wantActive {
				t.Errorf("active = %d, want %d", f.m.active, tt.wantActive)
			}
			if isQuit(cmd) != tt.wantQuit {
				t.Errorf("quit = %v, want %v", isQuit(cmd), tt.wantQuit)
			}
		})
	}
}

func TestUnboundKeysGoToActiveScreenOnly(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	f.send(key("2"), key("x"))
	if got := received[tea.KeyPressMsg](f.recs[1]); len(got) != 1 || got[0].String() != "x" {
		t.Fatalf("servers screen got %v", got)
	}
	for i, rec := range f.recs {
		if i != 1 && len(received[tea.KeyPressMsg](rec)) > 0 {
			t.Errorf("screen %d received a key", i)
		}
	}
}

func TestCapturingScreenReceivesEverything(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	scr := f.m.screens[0].(fakeScreen)
	scr.capturing = true
	f.m.screens[0] = scr
	for _, k := range []string{"q", "tab", ":", "?", "f2", "3", "esc"} {
		if cmd := f.send(key(k)); isQuit(cmd) {
			t.Fatalf("%q quit while capturing", k)
		}
	}
	if f.m.active != 0 || f.m.palette.open || f.m.help || f.m.compat != compat.Auto {
		t.Fatalf("global state changed while capturing: active %d palette %v help %v compat %v",
			f.m.active, f.m.palette.open, f.m.help, f.m.compat)
	}
	if got := received[tea.KeyPressMsg](f.recs[0]); len(got) != 7 {
		t.Fatalf("capturing screen got %d keys, want 7", len(got))
	}
	if !isQuit(f.send(key("ctrl+c"))) {
		t.Fatal("ctrl+c must quit while capturing")
	}
}

func TestCompatCycle(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	want := []compat.Mode{compat.Blocks, compat.ASCII, compat.Off, compat.Auto}
	for _, mode := range want {
		f.send(key("f2"))
		if f.m.compat != mode {
			t.Fatalf("compat = %v, want %v", f.m.compat, mode)
		}
		items := f.m.toasts.items
		if last := items[len(items)-1]; last.text != "compat: "+mode.String() || last.tone != theme.ToneInfo {
			t.Fatalf("toast = %+v", last)
		}
	}
}

func TestHelpOverlay(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	f.send(key("?"))
	if !f.m.help {
		t.Fatal("help not opened")
	}
	if isQuit(f.send(key("q"))) || f.m.help {
		t.Fatal("q should close help without quitting")
	}
	f.send(key("?"), key("3"))
	if f.m.help || f.m.active != 2 {
		t.Fatalf("help %v active %d: other keys close help and still apply", f.m.help, f.m.active)
	}
}

func TestSplash(t *testing.T) {
	f := newFixture(t, Options{})
	if !f.m.splash {
		t.Fatal("splash not shown")
	}
	f.send(splashMsg{frame: 3})
	if !f.m.splash || f.m.splashFrame != 3 {
		t.Fatal("splash closed early")
	}
	f.send(key("x"))
	if f.m.splash {
		t.Fatal("key did not dismiss the splash")
	}
	if len(received[tea.KeyPressMsg](f.recs[0])) != 0 {
		t.Fatal("dismissing key leaked to the screen")
	}

	f = newFixture(t, Options{})
	if cmd := f.send(splashMsg{frame: splashFrames}); cmd != nil || f.m.splash {
		t.Fatal("splash did not close after its duration")
	}
}

func TestShowDetail(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	f.send(ui.ShowDetailMsg{ServerID: "web"})
	for i, rec := range f.recs {
		if got := received[ui.ShowDetailMsg](rec); len(got) != 1 || got[0].ServerID != "web" {
			t.Errorf("screen %d got %v", i, got)
		}
	}
	if f.m.screens[f.m.active].Tab() != ui.TabDetail {
		t.Fatalf("active tab = %v, want detail", f.m.screens[f.m.active].Tab())
	}
}

func TestSwitchTabAndUnknownMessages(t *testing.T) {
	type custom struct{}
	f := newFixture(t, Options{SkipSplash: true})
	f.send(ui.SwitchTabMsg{Tab: ui.TabSettings}, custom{})
	if f.m.active != 4 {
		t.Fatalf("active = %d", f.m.active)
	}
	for i, rec := range f.recs {
		if len(received[custom](rec)) != 1 {
			t.Errorf("screen %d missed the unknown message", i)
		}
	}
}

func TestFleetBuiltBroadcastsAndToastsEventsOnce(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	ev := alerts.Event{Seq: 5, Type: alerts.EventFired, Alert: alerts.Alert{
		ServerName: "web-01", Kind: alerts.KindCPU, Severity: alerts.SeverityCrit, Value: 97, Threshold: 95,
	}}
	now := time.Now()
	f.send(fleetBuiltMsg{fleet: ui.Fleet{Now: now}, events: []alerts.Event{ev}, seq: 5})
	f.send(fleetBuiltMsg{fleet: ui.Fleet{Now: now.Add(time.Second)}, events: []alerts.Event{ev}, seq: 5})
	if n := len(f.m.toasts.items); n != 1 {
		t.Fatalf("toasts = %d, want 1", n)
	}
	if got := f.m.toasts.items[0]; got.tone != theme.ToneBad || got.text != "web-01 · CPU 97% ≥ 95%" {
		t.Fatalf("toast = %+v", got)
	}
	for i, rec := range f.recs {
		if got := received[ui.FleetMsg](rec); len(got) != 2 {
			t.Errorf("screen %d got %d FleetMsg, want 2", i, len(got))
		}
	}
	f.send(fleetBuiltMsg{fleet: ui.Fleet{Now: now}, seq: 5})
	if got := received[ui.FleetMsg](f.recs[0]); len(got) != 2 {
		t.Error("an older fleet must not be broadcast")
	}
}

func TestBuildFleetWithoutDeps(t *testing.T) {
	f := buildFleet(&ui.Deps{}, time.Unix(10, 0))
	if f.Snaps == nil || f.History == nil || len(f.Servers) != 0 {
		t.Fatalf("fleet = %+v", f)
	}
	if f.Settings != store.DefaultSettings() {
		t.Fatal("settings should default")
	}
}

func TestOpenShell(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	f.send(ui.FleetMsg{Fleet: ui.Fleet{Servers: []store.Server{{ID: "a", Name: "web-01"}}}})

	f.send(ui.OpenShellMsg{ServerID: "a"})
	if last := f.m.toasts.items[len(f.m.toasts.items)-1]; last.text != "shell not available in this mode" {
		t.Fatalf("toast = %+v", last)
	}

	f.send(ui.OpenShellMsg{ServerID: "missing"})
	if last := f.m.toasts.items[len(f.m.toasts.items)-1]; last.tone != theme.ToneBad {
		t.Fatalf("toast = %+v", last)
	}

	var opened store.Server
	f.m.deps.Shell = func(srv store.Server) tea.ExecCommand {
		opened = srv
		return nil
	}
	f.send(ui.OpenShellMsg{ServerID: "a"})
	if opened.Name != "web-01" {
		t.Fatalf("shell opened for %+v", opened)
	}
}

func TestOpenShellRefusesOfflineServer(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	f.send(ui.FleetMsg{Fleet: ui.Fleet{
		Now:     time.Unix(100, 0),
		Servers: []store.Server{{ID: "a", Name: "web-01"}},
		Snaps:   map[string]metrics.Snapshot{"a": {ServerID: "a", State: metrics.StateOffline, Err: "connection\x1b[31m refused"}},
	}})
	called := false
	f.m.deps.Shell = func(store.Server) tea.ExecCommand {
		called = true
		return nil
	}
	f.send(ui.OpenShellMsg{ServerID: "a"})
	if called {
		t.Fatal("shell must not be opened for an offline server")
	}
	last := f.m.toasts.items[len(f.m.toasts.items)-1]
	if last.tone != theme.ToneWarn || !strings.Contains(last.text, "web-01 is offline (connection refused)") {
		t.Fatalf("toast = %+v", last)
	}
}

func TestShellDone(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantTone theme.Tone
		wantText string
	}{
		{"clean exit", nil, theme.ToneGood, "closed shell to web-01"},
		{"error", errors.New("exit\x1b[31m 1"), theme.ToneBad, "shell to web-01: exit 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, Options{SkipSplash: true})
			f.send(ui.FleetMsg{Fleet: ui.Fleet{Servers: []store.Server{{ID: "a", Name: "web-01"}}}})
			if cmd := f.send(ui.ShellDoneMsg{ServerID: "a", Err: tt.err}); cmd == nil {
				t.Fatal("no refresh command")
			}
			got := f.m.toasts.items[0]
			if got.tone != tt.wantTone || got.text != tt.wantText {
				t.Fatalf("toast = %+v", got)
			}
		})
	}
}

func TestToastStack(t *testing.T) {
	var s toastStack
	for i := range 6 {
		s, _ = s.push(theme.ToneInfo, string(rune('a'+i)))
	}
	if len(s.items) != maxToasts || s.items[0].text != "c" || s.items[3].text != "f" {
		t.Fatalf("items = %+v", s.items)
	}
	s = s.expire(s.items[1].id)
	if len(s.items) != 3 || s.items[1].text != "e" {
		t.Fatalf("after expire = %+v", s.items)
	}
	s, _ = s.push(theme.ToneBad, "bad\x1b[2Jtext\n")
	if got := s.items[len(s.items)-1].text; got != "badtext " {
		t.Fatalf("sanitized text = %q", got)
	}
}

func TestToastMessageExpires(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	f.send(ui.ToastMsg{Tone: theme.ToneGood, Text: "saved"})
	id := f.m.toasts.items[0].id
	f.send(toastExpireMsg{id: id})
	if len(f.m.toasts.items) != 0 {
		t.Fatal("toast did not expire")
	}
}
