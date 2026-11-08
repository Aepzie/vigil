package screens

import (
	"testing"
	"time"

	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/tui/ui"
)

func newTestSettings(t *testing.T) *settingsScreen {
	t.Helper()
	s := newSettings(testDeps(t))
	s.Update(ui.FleetMsg{Fleet: fakeFleet()})
	return s
}

func TestSettingsEditField(t *testing.T) {
	s := newTestSettings(t)
	send(s, "enter")
	if !s.Capturing() {
		t.Fatal("enter did not start editing")
	}
	send(s, "ctrl+u")
	typeText(s, "abc")
	send(s, "enter")
	if !s.Capturing() || s.fieldErr == "" {
		t.Fatal("invalid value must keep editing and show an error")
	}
	send(s, "ctrl+u")
	typeText(s, "5")
	send(s, "enter")
	if s.Capturing() || s.fieldErr != "" {
		t.Fatal("valid value should finish editing")
	}
	if s.draft.PollInterval != 5*time.Second {
		t.Fatalf("poll interval %v", s.draft.PollInterval)
	}
	if !s.dirty() {
		t.Fatal("draft should be dirty")
	}

	send(s, "e")
	typeText(s, "0")
	send(s, "esc")
	if s.Capturing() || s.draft.PollInterval != 5*time.Second {
		t.Fatal("esc must cancel without changing the draft")
	}
}

func TestSettingsFieldParsing(t *testing.T) {
	fields := settingFields()
	tests := []struct {
		field int
		raw   string
		ok    bool
		check func(store.Settings) bool
	}{
		{0, "2.5", true, func(s store.Settings) bool { return s.PollInterval == 2500*time.Millisecond }},
		{0, "0", false, nil},
		{1, "30", true, func(s store.Settings) bool { return s.HistoryMinutes == 30 }},
		{1, "1.5", false, nil},
		{2, "70", true, func(s store.Settings) bool { return s.CPUWarn == 70 }},
		{3, "101", false, nil},
		{7, "NaN", false, nil},
		{8, "0", true, func(s store.Settings) bool { return s.OfflineGrace == 0 }},
		{9, "ascii", true, func(s store.Settings) bool { return s.Compat == "ascii" }},
	}
	for _, tt := range tests {
		set := store.DefaultSettings()
		err := fields[tt.field].set(&set, tt.raw)
		if (err == nil) != tt.ok {
			t.Errorf("%s=%q: err=%v, want ok=%v", fields[tt.field].label, tt.raw, err, tt.ok)
			continue
		}
		if tt.ok && !tt.check(set) {
			t.Errorf("%s=%q: value not applied", fields[tt.field].label, tt.raw)
		}
	}
}

func TestSettingsCompatCycle(t *testing.T) {
	s := newTestSettings(t)
	send(s, "G")
	if !s.fields[s.cursor].enum {
		t.Fatal("last field should be the compat mode")
	}
	tests := []struct {
		key  string
		want string
	}{
		{"right", "blocks"},
		{"right", "ascii"},
		{"left", "blocks"},
		{"left", "auto"},
		{"left", "off"},
		{"enter", "auto"},
	}
	for _, tt := range tests {
		send(s, tt.key)
		if s.draft.Compat != tt.want {
			t.Fatalf("after %s: %q, want %q", tt.key, s.draft.Compat, tt.want)
		}
	}
	if s.Capturing() {
		t.Fatal("cycling the compat mode must not capture input")
	}
}

func TestSettingsSave(t *testing.T) {
	s := newTestSettings(t)
	send(s, "j")
	send(s, "enter")
	send(s, "ctrl+u")
	typeText(s, "60")
	send(s, "enter")
	_, cmd := send(s, "ctrl+s")
	if cmd == nil || !s.saving {
		t.Fatal("ctrl+s did not start saving")
	}
	msg, ok := find[settingsSavedMsg](collect(cmd))
	if !ok || msg.err != nil || msg.set.HistoryMinutes != 60 {
		t.Fatalf("saved %#v", msg)
	}
	_, cmd = s.Update(msg)
	if s.saving || s.dirty() {
		t.Fatal("saved settings should be clean")
	}
	if _, ok := find[ui.RefreshMsg](collect(cmd)); !ok {
		t.Fatal("no refresh after save")
	}
}

func TestSettingsSaveRejectsInvertedThresholds(t *testing.T) {
	s := newTestSettings(t)
	s.draft.CPUWarn = 96
	if _, cmd := send(s, "S"); cmd != nil {
		t.Fatal("inverted thresholds must not be saved")
	}
	if s.note == "" {
		t.Fatal("no error shown")
	}
	send(s, "u")
	if s.dirty() || s.note != "" {
		t.Fatal("u should revert the draft")
	}
}

func TestSettingsDraftSurvivesFleetUpdates(t *testing.T) {
	s := newTestSettings(t)
	s.draft.CPUWarn = 70
	s.Update(ui.FleetMsg{Fleet: fakeFleet()})
	if s.draft.CPUWarn != 70 {
		t.Fatal("fleet update overwrote unsaved draft")
	}
	s.revert()
	f := fakeFleet()
	f.Settings.MemWarn = 50
	s.Update(ui.FleetMsg{Fleet: f})
	if s.draft.MemWarn != 50 {
		t.Fatal("clean draft should follow fleet settings")
	}
}

func TestValidateSettings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*store.Settings)
		ok     bool
	}{
		{"defaults", func(*store.Settings) {}, true},
		{"cpu equal", func(s *store.Settings) { s.CPUWarn = s.CPUCrit }, false},
		{"mem inverted", func(s *store.Settings) { s.MemWarn = 99 }, false},
		{"disk inverted", func(s *store.Settings) { s.DiskCrit = 10 }, false},
	}
	for _, tt := range tests {
		set := store.DefaultSettings()
		tt.mutate(&set)
		if err := validateSettings(set); (err == nil) != tt.ok {
			t.Errorf("%s: err=%v", tt.name, err)
		}
	}
}

func TestSettingsReadOnly(t *testing.T) {
	d := testDeps(t)
	d.ReadOnly = true
	s := newSettings(d)
	s.Update(ui.FleetMsg{Fleet: fakeFleet()})
	before := s.draft

	for _, key := range []string{"enter", "e", "ctrl+s", "S"} {
		_, cmd := send(s, key)
		if s.Capturing() || s.saving {
			t.Fatalf("%s started an edit or save in a read-only session", key)
		}
		expectToast(t, cmd, readOnlyNotice)
	}
	send(s, "G")
	for _, key := range []string{"left", "right", "space"} {
		_, cmd := send(s, key)
		expectToast(t, cmd, readOnlyNotice)
	}
	for _, a := range []settingsAction{settingsSave, settingsCycleCompat} {
		_, cmd := s.Update(settingsActionMsg{action: a})
		expectToast(t, cmd, readOnlyNotice)
	}
	if s.draft != before || s.saving {
		t.Fatal("read-only session changed the draft")
	}
	if cmds := s.Commands(); len(cmds) != 0 {
		t.Fatalf("read-only commands: %+v", cmds)
	}
	if hints := s.Hints(); len(hints) != 1 || hints[0].Key != "j/k" {
		t.Fatalf("read-only hints: %+v", hints)
	}
	checkSize(t, s.View(100, 30), 100, 30)
}
