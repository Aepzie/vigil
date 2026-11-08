// Package tui is the dashboard shell: header, tabs, footer, toasts,
// command palette, splash screen and compatibility mode. Tab content is
// provided by package screens through the ui.Screen interface.
package tui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Aepzie/vigil/internal/compat"
	"github.com/Aepzie/vigil/internal/metrics"
	"github.com/Aepzie/vigil/internal/sanitize"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/screens"
	"github.com/Aepzie/vigil/internal/tui/ui"
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

// Model is the root Bubble Tea model of one session. It is a value;
// every Update returns a new copy and never mutates shared slices.
type Model struct {
	deps    *ui.Deps
	th      *theme.Theme
	opts    Options
	screens []ui.Screen
	active  int

	width  int
	height int
	now    time.Time

	compat  compat.Mode
	fleet   ui.Fleet
	lastSeq uint64

	toasts  toastStack
	palette palette
	help    bool

	splash      bool
	splashFrame int
}

// cycleCompatMsg asks the app to advance the compatibility mode.
type cycleCompatMsg struct{}

// New creates the root model with the standard tabs.
func New(d *ui.Deps, opts Options) Model {
	d = withDefaults(d)
	return newModel(d, opts, []ui.Screen{
		screens.NewOverview(d),
		screens.NewServers(d),
		screens.NewDetail(d),
		screens.NewAlerts(d),
		screens.NewSettings(d),
	})
}

// withDefaults returns d, or a copy of it with a theme, when d lacks
// one; nil yields empty dependencies.
func withDefaults(d *ui.Deps) *ui.Deps {
	if d == nil {
		d = &ui.Deps{}
	}
	if d.Theme != nil {
		return d
	}
	cp := *d
	cp.Theme = theme.Default()
	return &cp
}

// newModel creates the root model for an explicit list of screens.
func newModel(d *ui.Deps, opts Options, scrs []ui.Screen) Model {
	d = withDefaults(d)
	m := Model{
		deps:    d,
		th:      d.Theme,
		opts:    opts,
		screens: scrs,
		now:     time.Now(),
		compat:  opts.Compat,
		splash:  !opts.SkipSplash,
	}
	m.fleet = ui.Fleet{Now: m.now, Settings: store.DefaultSettings()}
	if d.Alerts != nil {
		m.lastSeq = d.Alerts.LatestSeq()
	}
	return m
}

// Init implements tea.Model: it starts the clock, builds the first
// fleet, runs the splash animation and initializes every screen.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tickCmd(), fleetCmd(m.deps, m.lastSeq)}
	if m.splash {
		cmds = append(cmds, splashCmd(1))
	}
	for _, s := range m.screens {
		cmds = append(cmds, s.Init())
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m.update(msg)
}

// update is Update with the concrete model type.
func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		return m.handlePaste(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		m.now = time.Time(msg)
		return m, tea.Batch(tickCmd(), fleetCmd(m.deps, m.lastSeq))
	case fleetBuiltMsg:
		return m.applyFleet(msg)
	case ui.FleetMsg:
		m = m.setFleet(msg.Fleet)
		return m.broadcast(msg)
	case splashMsg:
		return m.advanceSplash(msg.frame)
	case toastExpireMsg:
		m.toasts = m.toasts.expire(msg.id)
		return m, nil
	case ui.ToastMsg:
		return m.toast(msg.Tone, msg.Text)
	case cycleCompatMsg:
		return m.cycleCompat()
	case ui.SwitchTabMsg:
		return m.activateTab(msg.Tab), nil
	case ui.ShowDetailMsg:
		m, cmd := m.broadcast(msg)
		return m.activateTab(ui.TabDetail), cmd
	case ui.RefreshMsg:
		return m, fleetCmd(m.deps, m.lastSeq)
	case ui.OpenShellMsg:
		return m.openShell(msg.ServerID)
	case ui.ShellDoneMsg:
		return m.shellDone(msg)
	default:
		return m.broadcast(msg)
	}
}

// handleKey routes a key press: ctrl+c always quits; the splash, the
// palette and the help overlay take keys first; a capturing screen gets
// everything else; otherwise global bindings win over the screen.
func (m Model) handleKey(k tea.KeyPressMsg) (Model, tea.Cmd) {
	key := k.String()
	if key == keyForceQuit {
		return m, tea.Quit
	}
	if m.splash {
		m.splash = false
		return m, nil
	}
	if m.palette.open {
		return m.paletteKey(k)
	}
	if m.help {
		m.help = false
		if key == keyHelp || key == keyEsc || key == keyQuit {
			return m, nil
		}
	}
	if m.activeScreen() == nil {
		return m.globalKey(key)
	}
	if m.activeScreen().Capturing() {
		return m.forwardActive(k)
	}
	if next, cmd, ok := m.globalKeyHandled(key); ok {
		return next, cmd
	}
	return m.forwardActive(k)
}

// globalKey handles key as a global binding, ignoring unbound keys.
func (m Model) globalKey(key string) (Model, tea.Cmd) {
	next, cmd, _ := m.globalKeyHandled(key)
	return next, cmd
}

// globalKeyHandled applies a global binding and reports whether key was
// one.
func (m Model) globalKeyHandled(key string) (Model, tea.Cmd, bool) {
	switch key {
	case keyQuit:
		return m, tea.Quit, true
	case keyNextTab:
		return m.cycleTab(1), nil, true
	case keyPrevTab:
		return m.cycleTab(-1), nil, true
	case keyPalette, keyPaletteAlt:
		m.palette = openPalette(m.paletteEntries())
		return m, nil, true
	case keyCompat:
		next, cmd := m.cycleCompat()
		return next, cmd, true
	case keyHelp:
		m.help = true
		return m, nil, true
	}
	if idx, ok := tabIndexForKey(key, len(m.screens)); ok {
		m.active = idx
		return m, nil, true
	}
	return m, nil, false
}

// handlePaste sends pasted text to the palette query or the active
// screen.
func (m Model) handlePaste(msg tea.PasteMsg) (Model, tea.Cmd) {
	if m.splash {
		return m, nil
	}
	if m.palette.open {
		m.palette = m.palette.typed(sanitize.Line(msg.Content))
		return m, nil
	}
	return m.forwardActive(msg)
}

// activeScreen returns the active screen or nil without screens.
func (m Model) activeScreen() ui.Screen {
	if m.active < 0 || m.active >= len(m.screens) {
		return nil
	}
	return m.screens[m.active]
}

// forwardActive delivers msg to the active screen only.
func (m Model) forwardActive(msg tea.Msg) (Model, tea.Cmd) {
	s := m.activeScreen()
	if s == nil {
		return m, nil
	}
	next, cmd := s.Update(msg)
	m.screens = slices.Clone(m.screens)
	m.screens[m.active] = next
	return m, cmd
}

// broadcast delivers msg to every screen.
func (m Model) broadcast(msg tea.Msg) (Model, tea.Cmd) {
	if len(m.screens) == 0 {
		return m, nil
	}
	scrs := make([]ui.Screen, len(m.screens))
	cmds := make([]tea.Cmd, 0, len(m.screens))
	for i, s := range m.screens {
		next, cmd := s.Update(msg)
		scrs[i] = next
		cmds = append(cmds, cmd)
	}
	m.screens = scrs
	return m, tea.Batch(cmds...)
}

// cycleTab moves the active tab by delta, wrapping around.
func (m Model) cycleTab(delta int) Model {
	n := len(m.screens)
	if n == 0 {
		return m
	}
	m.active = ((m.active+delta)%n + n) % n
	return m
}

// activateTab switches to the screen showing tab, if present.
func (m Model) activateTab(tab ui.TabID) Model {
	for i, s := range m.screens {
		if s.Tab() == tab {
			m.active = i
			return m
		}
	}
	return m
}

// setFleet stores f and advances the clock to its timestamp.
func (m Model) setFleet(f ui.Fleet) Model {
	m.fleet = f
	if f.Now.After(m.now) {
		m.now = f.Now
	}
	return m
}

// applyFleet handles a fleet built by fleetCmd: alert events become
// toasts (each sequence number once) and the fleet is broadcast unless a
// newer one already arrived.
func (m Model) applyFleet(msg fleetBuiltMsg) (Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, ev := range msg.events {
		if ev.Seq <= m.lastSeq {
			continue
		}
		var cmd tea.Cmd
		m, cmd = m.toast(eventToast(ev))
		cmds = append(cmds, cmd)
	}
	m.lastSeq = max(m.lastSeq, msg.seq)
	if msg.fleet.Now.Before(m.fleet.Now) {
		return m, tea.Batch(cmds...)
	}
	m = m.setFleet(msg.fleet)
	m, cmd := m.broadcast(ui.FleetMsg{Fleet: msg.fleet})
	return m, tea.Batch(append(cmds, cmd)...)
}

// advanceSplash moves the splash animation forward and closes it after
// splashFrames.
func (m Model) advanceSplash(frame int) (Model, tea.Cmd) {
	if !m.splash {
		return m, nil
	}
	m.splashFrame = frame
	if frame >= splashFrames {
		m.splash = false
		return m, nil
	}
	return m, splashCmd(frame + 1)
}

// toast shows a notification.
func (m Model) toast(tone theme.Tone, text string) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.toasts, cmd = m.toasts.push(tone, text)
	return m, cmd
}

// cycleCompat advances the compatibility mode and announces it.
func (m Model) cycleCompat() (Model, tea.Cmd) {
	m.compat = m.compat.Next()
	return m.toast(theme.ToneInfo, "compat: "+m.compat.String())
}

// lookupServer finds a server in the store, falling back to the fleet.
func (m Model) lookupServer(id string) (store.Server, bool) {
	if m.deps.Store != nil {
		if srv, ok := m.deps.Store.Server(id); ok {
			return srv, true
		}
	}
	for _, srv := range m.fleet.Servers {
		if srv.ID == id {
			return srv, true
		}
	}
	return store.Server{}, false
}

// serverName is the sanitized display name of a server ID.
func (m Model) serverName(id string) string {
	if srv, ok := m.lookupServer(id); ok && srv.Name != "" {
		return sanitize.Line(srv.Name)
	}
	return sanitize.Line(id)
}

// openShell suspends the dashboard and runs an interactive shell.
func (m Model) openShell(id string) (Model, tea.Cmd) {
	srv, ok := m.lookupServer(id)
	if !ok {
		return m.toast(theme.ToneBad, "unknown server "+id)
	}
	if m.deps.Shell == nil {
		return m.toast(theme.ToneWarn, "shell not available in this mode")
	}
	if snap, ok := m.fleet.Snaps[id]; ok && snap.State == metrics.StateOffline {
		reason := sanitize.Line(snap.Err)
		if reason == "" {
			reason = "unreachable"
		}
		return m.toast(theme.ToneWarn, srv.Name+" is offline ("+reason+") · check host/port with e in Servers")
	}
	exec := m.deps.Shell(srv)
	if exec == nil {
		return m.toast(theme.ToneBad, "cannot open shell to "+srv.Name)
	}
	return m, tea.Exec(exec, func(err error) tea.Msg {
		return ui.ShellDoneMsg{ServerID: id, Err: err}
	})
}

// shellDone reports the end of a shell session and refreshes the fleet.
func (m Model) shellDone(msg ui.ShellDoneMsg) (Model, tea.Cmd) {
	name := m.serverName(msg.ServerID)
	var toastCmd tea.Cmd
	if msg.Err != nil {
		m, toastCmd = m.toast(theme.ToneBad, "shell to "+name+": "+msg.Err.Error())
	} else {
		m, toastCmd = m.toast(theme.ToneGood, "closed shell to "+name)
	}
	return m, tea.Batch(toastCmd, fleetCmd(m.deps, m.lastSeq))
}
