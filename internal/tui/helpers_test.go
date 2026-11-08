package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/alerts"
	"github.com/Aepzie/vigil/internal/fuzzy"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
	"github.com/Aepzie/vigil/internal/widgets"
)

// recorder collects the messages delivered to fake screens.
type recorder struct{ msgs []tea.Msg }

// fakeScreen is a configurable ui.Screen that records its messages.
type fakeScreen struct {
	tab       ui.TabID
	title     string
	icon      string
	capturing bool
	hints     []ui.Hint
	commands  []ui.Command
	rec       *recorder
}

func (f fakeScreen) Tab() ui.TabID   { return f.tab }
func (f fakeScreen) Title() string   { return f.title }
func (f fakeScreen) Icon() string    { return f.icon }
func (f fakeScreen) Init() tea.Cmd   { return nil }
func (f fakeScreen) Capturing() bool { return f.capturing }
func (f fakeScreen) Hints() []ui.Hint {
	return f.hints
}
func (f fakeScreen) Commands() []ui.Command { return f.commands }

func (f fakeScreen) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	f.rec.msgs = append(f.rec.msgs, msg)
	return f, nil
}

func (f fakeScreen) View(width, height int) string {
	return widgets.Fit(f.title+" content", width, height)
}

// received returns the messages of type T delivered to the screen.
func received[T any](rec *recorder) []T {
	var out []T
	for _, m := range rec.msgs {
		if v, ok := m.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

// fixture is a model with five fake screens and real dependencies.
type fixture struct {
	m    Model
	recs []*recorder
	deps *ui.Deps
}

// newFixture builds a model over fake screens; capture marks the first
// screen as capturing input.
func newFixture(t *testing.T, opts Options) fixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	deps := &ui.Deps{
		Theme:   theme.Default(),
		Store:   st,
		Alerts:  alerts.NewEngine(alerts.Thresholds{CPUWarn: 80, CPUCrit: 95}),
		User:    "admin",
		Version: "v0.1.0",
	}
	specs := []struct {
		tab   ui.TabID
		title string
		icon  string
	}{
		{ui.TabOverview, "Overview", "◉"},
		{ui.TabServers, "Servers", "≡"},
		{ui.TabDetail, "Detail", "◈"},
		{ui.TabAlerts, "Alerts", "!"},
		{ui.TabSettings, "Settings", "✱"},
	}
	recs := make([]*recorder, len(specs))
	scrs := make([]ui.Screen, len(specs))
	for i, s := range specs {
		recs[i] = &recorder{}
		scrs[i] = fakeScreen{
			tab: s.tab, title: s.title, icon: s.icon, rec: recs[i],
			hints: []ui.Hint{{Key: "enter", Desc: "open"}},
		}
	}
	return fixture{m: newModel(deps, opts, scrs), recs: recs, deps: deps}
}

// send delivers msgs to the model in order.
func (f *fixture) send(msgs ...tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	for _, msg := range msgs {
		f.m, cmd = f.m.update(msg)
	}
	return cmd
}

// key builds a key press for a binding string used by the app.
func key(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "f2":
		return tea.KeyPressMsg{Code: tea.KeyF2}
	}
	if mod, rest, ok := strings.Cut(s, "+"); ok && mod == "ctrl" {
		return tea.KeyPressMsg{Code: []rune(rest)[0], Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// typeText sends one key press per rune of s.
func typeText(s string) []tea.Msg {
	out := make([]tea.Msg, 0, len(s))
	for _, r := range s {
		out = append(out, key(string(r)))
	}
	return out
}

// collect runs cmd and every nested batch and returns the messages. It
// blocks on timers, so only use it for commands without ticks.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collect(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// isQuit reports whether cmd is tea.Quit.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// assertFrame checks that frame is exactly w×h cells.
func assertFrame(t *testing.T, frame string, w, h int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) != h {
		t.Fatalf("frame has %d lines, want %d", len(lines), h)
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != w {
			t.Fatalf("line %d is %d cells, want %d: %q", i, got, w, ansi.Strip(l))
		}
	}
}

// substringRank is a deterministic stand-in for fuzzy.Rank: titles
// containing the query (case-insensitively) in their original order.
func substringRank(query string, targets []string) []fuzzy.Ranked {
	q := strings.ToLower(query)
	var out []fuzzy.Ranked
	for i, t := range targets {
		pos := strings.Index(strings.ToLower(t), q)
		if pos < 0 {
			continue
		}
		var positions []int
		for p := range len([]rune(query)) {
			positions = append(positions, len([]rune(t[:pos]))+p)
		}
		out = append(out, fuzzy.Ranked{Index: i, Match: fuzzy.Match{Positions: positions}})
	}
	return out
}

// withRanker swaps the palette ranking for the duration of a test.
func withRanker(t *testing.T) {
	t.Helper()
	prev := rankCommands
	rankCommands = substringRank
	t.Cleanup(func() { rankCommands = prev })
}
