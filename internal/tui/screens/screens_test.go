package screens

import (
	"fmt"
	"testing"

	"github.com/CraguS/vigil/internal/tui/ui"
)

var testSizes = [][2]int{{80, 21}, {150, 41}, {220, 57}, {40, 10}, {1, 1}, {12, 3}, {0, 0}}

// screenStates returns named screen configurations for size checks: each
// constructor in its default state plus interesting modal/mode states.
func screenStates(t *testing.T, fleet ui.Fleet) map[string]ui.Screen {
	t.Helper()
	d := testDeps(t)
	withFleet := func(s ui.Screen, keys ...string) ui.Screen {
		s, _ = s.Update(ui.FleetMsg{Fleet: fleet})
		for _, k := range keys {
			s, _ = send(s, k)
		}
		return s
	}
	states := map[string]ui.Screen{
		"overview":         withFleet(NewOverview(d)),
		"overview/tiles":   withFleet(NewOverview(d), "v", "j", "l"),
		"overview/filter":  withFleet(NewOverview(d), "/", "w", "e"),
		"servers":          withFleet(NewServers(d)),
		"servers/add":      withFleet(NewServers(d), "a"),
		"servers/edit":     withFleet(NewServers(d), "e"),
		"servers/delete":   withFleet(NewServers(d), "x"),
		"detail":           withFleet(NewDetail(d)),
		"detail/next":      withFleet(NewDetail(d), "]"),
		"alerts":           withFleet(NewAlerts(d)),
		"alerts/resolved":  withFleet(NewAlerts(d), "G"),
		"settings":         withFleet(NewSettings(d)),
		"settings/editing": withFleet(NewSettings(d), "enter", "9"),
	}
	for _, id := range []string{"s1", "s2", "s3", "s4"} {
		s, _ := withFleet(NewDetail(d)).Update(ui.ShowDetailMsg{ServerID: id})
		states["detail/"+id] = s
	}
	return states
}

func TestViewSizes(t *testing.T) {
	fleets := map[string]ui.Fleet{"empty": emptyFleet(), "populated": fakeFleet()}
	for fleetName, fleet := range fleets {
		for stateName, scr := range screenStates(t, fleet) {
			for _, sz := range testSizes {
				name := fmt.Sprintf("%s/%s/%dx%d", fleetName, stateName, sz[0], sz[1])
				t.Run(name, func(t *testing.T) {
					checkSize(t, scr.View(sz[0], sz[1]), sz[0], sz[1])
				})
			}
		}
	}
}

func TestViewBeforeFirstFleet(t *testing.T) {
	d := testDeps(t)
	for _, scr := range []ui.Screen{NewOverview(d), NewServers(d), NewDetail(d), NewAlerts(d), NewSettings(d)} {
		for _, sz := range testSizes {
			checkSize(t, scr.View(sz[0], sz[1]), sz[0], sz[1])
		}
	}
}

func TestViewWithNilDeps(t *testing.T) {
	for _, scr := range []ui.Screen{NewOverview(nil), NewServers(nil), NewDetail(nil), NewAlerts(nil), NewSettings(nil)} {
		scr, _ = scr.Update(ui.FleetMsg{Fleet: fakeFleet()})
		checkSize(t, scr.View(80, 21), 80, 21)
	}
}

func TestScreenIdentity(t *testing.T) {
	d := testDeps(t)
	tests := []struct {
		scr   ui.Screen
		tab   ui.TabID
		title string
		icon  string
	}{
		{NewOverview(d), ui.TabOverview, "Overview", "◉"},
		{NewServers(d), ui.TabServers, "Servers", "≡"},
		{NewDetail(d), ui.TabDetail, "Detail", "◈"},
		{NewAlerts(d), ui.TabAlerts, "Alerts", "⚡"},
		{NewSettings(d), ui.TabSettings, "Settings", "✱"},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			if tt.scr.Tab() != tt.tab || tt.scr.Title() != tt.title || tt.scr.Icon() != tt.icon {
				t.Fatalf("got %v %q %q", tt.scr.Tab(), tt.scr.Title(), tt.scr.Icon())
			}
			if tt.scr.Capturing() {
				t.Fatal("new screen must not capture input")
			}
			if len(tt.scr.Commands()) == 0 {
				t.Fatal("screen contributes no palette commands")
			}
			for _, c := range tt.scr.Commands() {
				if c.Title == "" || c.Run == nil {
					t.Fatalf("incomplete command %+v", c)
				}
			}
		})
	}
}
