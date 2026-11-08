package screens

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/CraguS/vigil/internal/alerts"
	"github.com/CraguS/vigil/internal/format"
	"github.com/CraguS/vigil/internal/theme"
	"github.com/CraguS/vigil/internal/tui/ui"
	"github.com/CraguS/vigil/internal/widgets"
)

// Alerts layout constants.
const (
	allClearHeight = 9
	recentMinH     = 5
)

// alertAckedMsg is the result of acknowledging alerts.
type alertAckedMsg struct {
	count int
	label string
}

// alertsAction is a palette-triggered action of the Alerts screen.
type alertsAction int

const (
	alertsAck alertsAction = iota
	alertsAckAll
	alertsShowServer
)

// alertsActionMsg carries a palette command back into Update.
type alertsActionMsg struct{ action alertsAction }

// alertEntry is one navigable row: an active or a resolved alert.
type alertEntry struct {
	alert    alerts.Alert
	resolved bool
}

// key identifies an entry across fleet updates. Resolved and active
// alerts may share an ID, so the list is part of the key.
func (e alertEntry) key() string {
	if e.resolved {
		return "r:" + e.alert.ID
	}
	return "a:" + e.alert.ID
}

// alertsScreen lists active alerts and recently resolved ones.
type alertsScreen struct {
	d        *ui.Deps
	t        *theme.Theme
	fleet    ui.Fleet
	selected string
}

func newAlerts(d *ui.Deps) *alertsScreen {
	return &alertsScreen{d: d, t: themeOf(d), fleet: initialFleet(d)}
}

func (s *alertsScreen) Tab() ui.TabID   { return ui.TabAlerts }
func (s *alertsScreen) Title() string   { return "Alerts" }
func (s *alertsScreen) Icon() string    { return "⚡" }
func (s *alertsScreen) Init() tea.Cmd   { return nil }
func (s *alertsScreen) Capturing() bool { return false }

// lists returns the active and resolved alerts of the fleet.
func (s *alertsScreen) lists() (active, resolved []alertEntry) {
	for _, a := range s.fleet.Active {
		active = append(active, alertEntry{alert: a})
	}
	for _, a := range s.fleet.Recent {
		if a.Resolved {
			resolved = append(resolved, alertEntry{alert: a, resolved: true})
		}
	}
	return active, resolved
}

// entries is the navigation order: active first, then resolved.
func (s *alertsScreen) entries() []alertEntry {
	active, resolved := s.lists()
	return append(active, resolved...)
}

func entryIndex(entries []alertEntry, key string) int {
	for i, e := range entries {
		if e.key() == key {
			return i
		}
	}
	return -1
}

func (s *alertsScreen) current() (alertEntry, bool) {
	entries := s.entries()
	i := entryIndex(entries, s.selected)
	if i < 0 {
		return alertEntry{}, false
	}
	return entries[i], true
}

func (s *alertsScreen) syncSelection() {
	entries := s.entries()
	if entryIndex(entries, s.selected) >= 0 {
		return
	}
	s.selected = ""
	if len(entries) > 0 {
		s.selected = entries[0].key()
	}
}

func (s *alertsScreen) move(delta int) {
	entries := s.entries()
	if i := stepIndex(entryIndex(entries, s.selected), delta, len(entries)); i >= 0 {
		s.selected = entries[i].key()
	}
}

// Update implements ui.Screen.
func (s *alertsScreen) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case ui.FleetMsg:
		s.fleet = msg.Fleet
		s.syncSelection()
	case alertAckedMsg:
		text := "Acknowledged " + msg.label
		if msg.count != 1 {
			text = "Acknowledged " + strconv.Itoa(msg.count) + " alerts"
		}
		return s, tea.Batch(ui.Toast(theme.ToneGood, text), ui.Refresh)
	case alertsActionMsg:
		return s, s.apply(msg.action)
	case tea.KeyPressMsg:
		return s, s.key(msg)
	}
	return s, nil
}

func (s *alertsScreen) apply(a alertsAction) tea.Cmd {
	switch a {
	case alertsAck:
		return s.ackSelected()
	case alertsAckAll:
		return s.ackAll()
	case alertsShowServer:
		return s.showServer()
	}
	return nil
}

func (s *alertsScreen) key(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		s.move(1)
	case "k", "up":
		s.move(-1)
	case "g", "home":
		s.move(-len(s.entries()))
	case "G", "end":
		s.move(len(s.entries()))
	case "a":
		return s.ackSelected()
	case "A":
		return s.ackAll()
	case "enter", "d":
		return s.showServer()
	}
	return nil
}

func (s *alertsScreen) showServer() tea.Cmd {
	e, ok := s.current()
	if !ok || e.alert.ServerID == "" {
		return nil
	}
	return emit(ui.ShowDetailMsg{ServerID: e.alert.ServerID})
}

func (s *alertsScreen) ackSelected() tea.Cmd {
	e, ok := s.current()
	if !ok || e.resolved || e.alert.Acked {
		return nil
	}
	label := e.alert.Kind.String() + " on " + clean(e.alert.ServerName)
	return s.ackCmd([]string{e.alert.ID}, label)
}

func (s *alertsScreen) ackAll() tea.Cmd {
	var ids []string
	for _, a := range s.fleet.Active {
		if !a.Acked {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		return ui.Toast(theme.ToneInfo, "Nothing to acknowledge")
	}
	return s.ackCmd(ids, "")
}

// ackCmd acknowledges alerts off the UI goroutine.
func (s *alertsScreen) ackCmd(ids []string, label string) tea.Cmd {
	if s.d == nil || s.d.Alerts == nil {
		return ui.Toast(theme.ToneWarn, "Alerts are not available in this session")
	}
	eng := s.d.Alerts
	return func() tea.Msg {
		for _, id := range ids {
			eng.Ack(id)
		}
		return alertAckedMsg{count: len(ids), label: label}
	}
}

// Hints implements ui.Screen.
func (s *alertsScreen) Hints() []ui.Hint {
	e, ok := s.current()
	if !ok {
		return nil
	}
	hints := []ui.Hint{hint("j/k", "move"), hint("enter", "server detail")}
	if !e.resolved && !e.alert.Acked {
		hints = append(hints, hint("a", "acknowledge"))
	}
	if len(s.fleet.Active) > 0 {
		hints = append(hints, hint("A", "ack all"))
	}
	return hints
}

// Commands implements ui.Screen.
func (s *alertsScreen) Commands() []ui.Command {
	act := func(a alertsAction) func() tea.Cmd {
		return func() tea.Cmd { return emit(alertsActionMsg{action: a}) }
	}
	return []ui.Command{
		{Title: "Acknowledge selected alert", Hint: "a", Run: act(alertsAck)},
		{Title: "Acknowledge all alerts", Hint: "A", Run: act(alertsAckAll)},
		{Title: "Show server of selected alert", Hint: "enter", Run: act(alertsShowServer)},
	}
}

// View implements ui.Screen.
func (s *alertsScreen) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	active, resolved := s.lists()
	cur, _ := s.current()
	var top string
	topH := height
	if len(active) == 0 {
		if len(resolved) > 0 && height >= allClearHeight+recentMinH {
			topH = allClearHeight
		}
		top = s.allClear(width, topH)
	} else {
		if len(resolved) > 0 && height >= 2*recentMinH {
			topH = clamp(len(active)+4, recentMinH, height-recentMinH)
		}
		top = s.activePanel(active, cur, width, topH)
	}
	blocks := []string{top}
	if rest := height - topH; rest > 0 {
		blocks = append(blocks, s.recentPanel(resolved, cur, width, rest))
	}
	return widgets.Fit(widgets.JoinV(blocks...), width, height)
}

// allClear is the calm empty state for active alerts.
func (s *alertsScreen) allClear(w, h int) string {
	t := s.t
	iw, ih := widgets.PanelInner(w, h)
	n := len(s.fleet.Servers)
	scope := "No active alerts."
	if n > 0 {
		scope = "No active alerts across " + strconv.Itoa(n) + " servers."
	}
	lines := []string{
		fg(t.P.Good, bold(glyphCheck)),
		fg(t.P.Good, bold("All clear")),
		fg(t.P.Muted, scope),
	}
	pad := max((ih-len(lines))/2, 0)
	body := make([]string, 0, ih)
	for range pad {
		body = append(body, "")
	}
	for _, l := range lines {
		body = append(body, widgets.Center(l, iw))
	}
	return widgets.Panel(t, widgets.PanelOpts{
		Width: w, Height: h, Title: "Active alerts", Info: "0", Body: strings.Join(body, "\n"),
	})
}

// activePanel renders the table of active alerts.
func (s *alertsScreen) activePanel(active []alertEntry, cur alertEntry, w, h int) string {
	t := s.t
	iw, ih := widgets.PanelInner(w, h)
	cols := fitColumns(s.activeColumns(), iw)
	columns, cells := tableData(cols, active)
	selected := -1
	if !cur.resolved {
		selected = entryIndex(active, cur.key())
	}
	crit := 0
	for _, e := range active {
		if e.alert.Severity == alerts.SeverityCrit {
			crit++
		}
	}
	info := strconv.Itoa(len(active)) + " active"
	if crit > 0 {
		info += glyphSep + strconv.Itoa(crit) + " critical"
	}
	return widgets.Panel(t, widgets.PanelOpts{
		Width: w, Height: h, Title: "Active alerts", Info: info, Focused: selected >= 0,
		Body: widgets.Table(t, widgets.TableOpts{
			Width: iw, Height: ih, Columns: columns, Rows: cells, Selected: selected,
		}),
	})
}

// severityBadge renders CRIT/WARN as a badge.
func severityBadge(t *theme.Theme, sev alerts.Severity) string {
	if sev == alerts.SeverityCrit {
		return widgets.Badge(t, "CRIT", theme.ToneBad)
	}
	return widgets.Badge(t, "WARN", theme.ToneWarn)
}

// alertValue renders "value ≥ threshold" for utilization alerts.
func alertValue(a alerts.Alert) string {
	if a.Kind == alerts.KindOffline {
		return ""
	}
	return format.Percent(a.Value) + " ≥ " + format.Percent(a.Threshold)
}

// alertKind renders the kind with its subject, e.g. "disk /var".
func alertKind(a alerts.Alert) string {
	if a.Subject == "" {
		return a.Kind.String()
	}
	return a.Kind.String() + " " + clean(a.Subject)
}

func (s *alertsScreen) activeColumns() []prioColumn[alertEntry] {
	t := s.t
	now := s.fleet.Now
	text := func(e alertEntry) theme.RGB {
		if e.alert.Acked {
			return t.P.Muted
		}
		return t.P.Text
	}
	return []prioColumn[alertEntry]{
		{widgets.Column{Title: "SEV", Width: 6}, 100, func(e alertEntry) string {
			return severityBadge(t, e.alert.Severity)
		}},
		{widgets.Column{Title: "SERVER", Min: 10, Flex: 1}, 99, func(e alertEntry) string {
			return fg(text(e), bold(clean(e.alert.ServerName)))
		}},
		{widgets.Column{Title: "KIND", Min: 7, Flex: 1}, 80, func(e alertEntry) string {
			return fg(t.P.Muted, alertKind(e.alert))
		}},
		{widgets.Column{Title: "MESSAGE", Min: 14, Flex: 3}, 90, func(e alertEntry) string {
			return fg(text(e), clean(e.alert.Message))
		}},
		{widgets.Column{Title: "VALUE", Width: 11, Align: widgets.AlignRightCol}, 50, func(e alertEntry) string {
			return fg(t.ToneColor(severityTone(e.alert.Severity)), alertValue(e.alert))
		}},
		{widgets.Column{Title: "SINCE", Width: 8, Align: widgets.AlignRightCol}, 85, func(e alertEntry) string {
			return fg(t.P.Muted, format.Duration(now.Sub(e.alert.Since)))
		}},
		{widgets.Column{Title: "ACK", Width: 3, Align: widgets.AlignCenterCol}, 70, func(e alertEntry) string {
			if e.alert.Acked {
				return fg(t.P.Good, glyphCheck)
			}
			return ""
		}},
	}
}

func severityTone(sev alerts.Severity) theme.Tone {
	if sev == alerts.SeverityCrit {
		return theme.ToneBad
	}
	return theme.ToneWarn
}

// recentPanel renders the dimmed table of resolved alerts.
func (s *alertsScreen) recentPanel(resolved []alertEntry, cur alertEntry, w, h int) string {
	t := s.t
	iw, ih := widgets.PanelInner(w, h)
	now := s.fleet.Now
	faint := func(v string) string { return fg(t.P.Faint, v) }
	cols := fitColumns([]prioColumn[alertEntry]{
		{widgets.Column{Title: "RESOLVED", Width: 9}, 95, func(e alertEntry) string {
			return faint(format.Ago(e.alert.ResolvedAt, now))
		}},
		{widgets.Column{Title: "SEV", Width: 4}, 60, func(e alertEntry) string {
			return faint(e.alert.Severity.String())
		}},
		{widgets.Column{Title: "SERVER", Min: 10, Flex: 1}, 99, func(e alertEntry) string {
			return faint(clean(e.alert.ServerName))
		}},
		{widgets.Column{Title: "KIND", Min: 7, Flex: 1}, 80, func(e alertEntry) string {
			return faint(alertKind(e.alert))
		}},
		{widgets.Column{Title: "MESSAGE", Min: 14, Flex: 3}, 90, func(e alertEntry) string {
			return faint(clean(e.alert.Message))
		}},
		{widgets.Column{Title: "LASTED", Width: 8, Align: widgets.AlignRightCol}, 70, func(e alertEntry) string {
			if e.alert.ResolvedAt.IsZero() || e.alert.Since.IsZero() {
				return faint(glyphNone)
			}
			return faint(format.Duration(e.alert.ResolvedAt.Sub(e.alert.Since)))
		}},
	}, iw)
	columns, cells := tableData(cols, resolved)
	selected := -1
	if cur.resolved {
		selected = entryIndex(resolved, cur.key())
	}
	return widgets.Panel(t, widgets.PanelOpts{
		Width: w, Height: h, Title: "Recent / resolved", Info: strconv.Itoa(len(resolved)),
		Focused: selected >= 0,
		Body: widgets.Table(t, widgets.TableOpts{
			Width: iw, Height: ih, Columns: columns, Rows: cells, Selected: selected,
			Empty: "Nothing resolved recently",
		}),
	})
}
