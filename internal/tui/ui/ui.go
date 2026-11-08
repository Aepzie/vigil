// Package ui is the contract between the TUI shell (package tui) and the
// individual screens (package screens): the Screen interface, shared
// dependencies and the messages they exchange.
package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Aepzie/vigil/internal/alerts"
	"github.com/Aepzie/vigil/internal/collector"
	"github.com/Aepzie/vigil/internal/metrics"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
)

// ShellFunc builds an interactive shell command for a server, to be run
// with tea.Exec. It is nil when shells are unavailable.
type ShellFunc func(srv store.Server) tea.ExecCommand

// Deps are shared by the app and every screen of one session.
type Deps struct {
	Theme   *theme.Theme
	Store   *store.Store
	Source  collector.Source
	Alerts  *alerts.Engine
	Shell   ShellFunc
	PollNow func(id string)
	// User is the logged-in user name (already sanitized).
	User string
	// Version is the build version, e.g. "v0.1.0".
	Version string
	// StartedAt is when the manager process started (header uptime).
	StartedAt time.Time
	// ManagerKey is the manager public key (authorized_keys line) shown in
	// Settings so it can be installed on targets.
	ManagerKey string
	// DataDir is shown in Settings.
	DataDir string
	// Listen is the SSH listen address shown in Settings ("" for local).
	Listen string
	// Term is the client TERM value.
	Term string
	// ReadOnly hides and refuses every mutation of shared state (adding,
	// editing and deleting servers, editing and saving settings). It is
	// set for public demo sessions, whose anonymous viewers share one
	// store.
	ReadOnly bool
	// MaxServers caps the inventory: adding a server is refused once this
	// many exist. 0 means no limit.
	MaxServers int
}

// TabID identifies a top-level tab.
type TabID int

const (
	TabOverview TabID = iota
	TabServers
	TabDetail
	TabAlerts
	TabSettings
)

// Hint is one footer key hint, e.g. {Key: "enter", Desc: "shell"}.
type Hint struct {
	Key  string
	Desc string
}

// Command is a command-palette entry contributed by a screen.
type Command struct {
	Title string
	// Hint is shown dim at the right, e.g. a key binding.
	Hint string
	Run  func() tea.Cmd
}

// Screen is one tab. Implementations must be cheap to render: View is
// pure and must return exactly width×height cells.
type Screen interface {
	Tab() TabID
	// Title is the tab label, e.g. "Overview".
	Title() string
	// Icon is a single glyph from common terminal fonts (no emoji),
	// e.g. "◉", "≡", "⚡".
	Icon() string
	Init() tea.Cmd
	// Update receives FleetMsg broadcasts, key presses while the screen
	// is active (after global keys were handled), and the screen's own
	// messages.
	Update(msg tea.Msg) (Screen, tea.Cmd)
	View(width, height int) string
	// Hints are the footer key hints for the current screen state.
	Hints() []Hint
	// Capturing is true while a text input or modal form has focus. The
	// app then forwards every key to the screen (including tab, 1-9, q,
	// :, esc) and only handles ctrl+c globally.
	Capturing() bool
	// Commands contributes palette entries for this screen.
	Commands() []Command
}

// Fleet is an immutable copy of all data needed to render one frame.
type Fleet struct {
	Now      time.Time
	Servers  []store.Server
	Snaps    map[string]metrics.Snapshot
	History  map[string]metrics.Series
	Active   []alerts.Alert
	Recent   []alerts.Alert
	Settings store.Settings
}

// FleetMsg is broadcast to all screens every tick (once per second) and
// after store mutations. It is built in a tea.Cmd, never in Update.
type FleetMsg struct{ Fleet Fleet }

// ToastMsg asks the app to show a toast.
type ToastMsg struct {
	Tone theme.Tone
	Text string
}

// Toast is a helper that returns a command emitting a ToastMsg.
func Toast(tone theme.Tone, text string) tea.Cmd {
	return func() tea.Msg { return ToastMsg{Tone: tone, Text: text} }
}

// OpenShellMsg asks the app to open an interactive SSH shell to a server
// and return to the dashboard when it exits.
type OpenShellMsg struct{ ServerID string }

// ShowDetailMsg asks the app to switch to the Detail tab for a server.
// The app forwards it to every screen (the Detail screen selects the
// server) and then activates TabDetail.
type ShowDetailMsg struct{ ServerID string }

// SwitchTabMsg asks the app to activate a tab.
type SwitchTabMsg struct{ Tab TabID }

// RefreshMsg asks the app to rebuild and broadcast a FleetMsg now (e.g.
// after a store mutation).
type RefreshMsg struct{}

// Refresh returns a command emitting RefreshMsg.
func Refresh() tea.Msg { return RefreshMsg{} }

// ShellDoneMsg is sent after a shell exits; Err is nil on normal exit.
type ShellDoneMsg struct {
	ServerID string
	Err      error
}
