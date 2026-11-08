// Package tui is the dashboard shell: header, tabs, footer, toasts,
// command palette, splash screen and compatibility mode. Tab content is
// provided by package screens through the ui.Screen interface.
package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/CraguS/vigil/internal/compat"
	"github.com/CraguS/vigil/internal/tui/ui"
)

// Options tune a session.
type Options struct {
	// Compat is the initial compatibility mode.
	Compat compat.Mode
	// Env describes the client terminal for compat.Auto resolution.
	Env compat.Env
	// SkipSplash disables the splash screen.
	SkipSplash bool
}

// Model is the root Bubble Tea model of one session.
type Model struct {
	deps *ui.Deps
	opts Options
}

// New creates the root model.
func New(d *ui.Deps, opts Options) Model {
	return Model{deps: d, opts: opts}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	return m, nil
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView("vigil")
	v.AltScreen = true
	return v
}

// RenderFrame synchronously renders one complete frame of size
// width×height with the given tab active, without a running program and
// without the splash screen. It is used by `vigil render` for screenshots
// and by tests.
func RenderFrame(d *ui.Deps, opts Options, width, height int, tab ui.TabID) string {
	return ""
}
