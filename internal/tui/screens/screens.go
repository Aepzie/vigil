// Package screens implements the dashboard tabs: Overview, Servers,
// Detail, Alerts and Settings. Each screen renders from the latest
// ui.FleetMsg and talks to the app shell only through ui messages.
package screens

import "github.com/CraguS/vigil/internal/tui/ui"

// NewOverview lists all servers as a table or tiles.
func NewOverview(d *ui.Deps) ui.Screen { return newOverview(d) }

// NewServers manages the inventory (add, edit, delete).
func NewServers(d *ui.Deps) ui.Screen { return newServers(d) }

// NewDetail shows one server in depth.
func NewDetail(d *ui.Deps) ui.Screen { return newDetail(d) }

// NewAlerts lists active and resolved alerts.
func NewAlerts(d *ui.Deps) ui.Screen { return newAlerts(d) }

// NewSettings edits thresholds and shows manager info.
func NewSettings(d *ui.Deps) ui.Screen { return newSettings(d) }
