// Package screens implements the dashboard tabs.
package screens

import (
	tea "charm.land/bubbletea/v2"

	"github.com/CraguS/vigil/internal/tui/ui"
	"github.com/CraguS/vigil/internal/widgets"
)

type placeholder struct {
	tab   ui.TabID
	title string
	icon  string
}

func (p placeholder) Tab() ui.TabID                       { return p.tab }
func (p placeholder) Title() string                       { return p.title }
func (p placeholder) Icon() string                        { return p.icon }
func (p placeholder) Init() tea.Cmd                       { return nil }
func (p placeholder) Update(tea.Msg) (ui.Screen, tea.Cmd) { return p, nil }
func (p placeholder) View(width, height int) string       { return widgets.Blank(width, height) }
func (p placeholder) Hints() []ui.Hint                    { return nil }
func (p placeholder) Capturing() bool                     { return false }
func (p placeholder) Commands() []ui.Command              { return nil }

// NewOverview lists all servers as a table or tiles.
func NewOverview(d *ui.Deps) ui.Screen { return newOverview(d) }

// NewServers manages the inventory (add, edit, delete).
func NewServers(d *ui.Deps) ui.Screen { return newServers(d) }

// NewDetail shows one server in depth.
func NewDetail(d *ui.Deps) ui.Screen { return newDetail(d) }

// NewAlerts lists active and resolved alerts.
func NewAlerts(d *ui.Deps) ui.Screen { return placeholder{ui.TabAlerts, "Alerts", "⚡"} }

// NewSettings edits thresholds and shows manager info.
func NewSettings(d *ui.Deps) ui.Screen { return placeholder{ui.TabSettings, "Settings", "✱"} }
