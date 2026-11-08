package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
)

// openFixturePalette returns a fixture with two servers, a screen
// command on the Servers tab and the palette open.
func openFixturePalette(t *testing.T) fixture {
	t.Helper()
	withRanker(t)
	f := newFixture(t, Options{SkipSplash: true})
	scr := f.m.screens[1].(fakeScreen)
	scr.commands = []ui.Command{{Title: "Add server", Hint: "a", Run: func() tea.Cmd {
		return ui.Toast(theme.ToneInfo, "add")
	}}}
	f.m.screens[1] = scr
	f.send(
		tea.WindowSizeMsg{Width: 120, Height: 40},
		ui.FleetMsg{Fleet: ui.Fleet{Servers: []store.Server{{ID: "w", Name: "web-01"}, {ID: "d", Name: "db-01"}}}},
		key("ctrl+k"),
	)
	if !f.m.palette.open {
		t.Fatal("palette not open")
	}
	return f
}

// selectedTitle is the title of the palette's highlighted entry.
func selectedTitle(f fixture) string {
	e, _ := f.m.palette.selected()
	return e.title
}

func TestPaletteEntries(t *testing.T) {
	f := openFixturePalette(t)
	titles := strings.Join(f.m.palette.titles, "\n")
	for _, want := range []string{
		"Go to Overview", "Go to Settings", "Toggle compatibility mode", "Quit",
		"Open shell: web-01", "Show detail: db-01", "Servers › Add server",
	} {
		if !strings.Contains(titles, want) {
			t.Errorf("palette lacks %q", want)
		}
	}
}

func TestPaletteFilteringAndSelection(t *testing.T) {
	tests := []struct {
		name  string
		input []tea.Msg
		want  string
		count int
	}{
		{name: "empty query", want: "Go to Overview", count: 12},
		{name: "filter", input: typeText("shell"), want: "Open shell: web-01", count: 2},
		{name: "down", input: append(typeText("shell"), key("down")), want: "Open shell: db-01", count: 2},
		{name: "ctrl+n wraps", input: append(typeText("shell"), key("ctrl+n"), key("ctrl+n")), want: "Open shell: web-01", count: 2},
		{name: "up wraps", input: append(typeText("shell"), key("up")), want: "Open shell: db-01", count: 2},
		{name: "backspace", input: append(typeText("quitx"), key("backspace")), want: "Quit", count: 1},
		{name: "ctrl+u clears", input: append(typeText("quit"), key("ctrl+u")), want: "Go to Overview", count: 12},
		{name: "no match", input: typeText("zzz"), count: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := openFixturePalette(t)
			f.send(tt.input...)
			if got := len(f.m.palette.ranked); got != tt.count {
				t.Fatalf("results = %d, want %d", got, tt.count)
			}
			if got := selectedTitle(f); got != tt.want {
				t.Fatalf("selected = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPaletteRun(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantActive int
		wantMsg    func(tea.Msg) bool
	}{
		{name: "go to", query: "go to alerts", wantActive: 3},
		{name: "quit", query: "quit", wantMsg: func(m tea.Msg) bool { _, ok := m.(tea.QuitMsg); return ok }},
		{name: "show detail", query: "detail: db", wantMsg: func(m tea.Msg) bool {
			return m == ui.ShowDetailMsg{ServerID: "d"}
		}},
		{name: "open shell", query: "shell: web", wantMsg: func(m tea.Msg) bool {
			return m == ui.OpenShellMsg{ServerID: "w"}
		}},
		{name: "compat", query: "compat", wantMsg: func(m tea.Msg) bool { return m == cycleCompatMsg{} }},
		{name: "other screen command switches tab", query: "add server", wantActive: 1, wantMsg: func(m tea.Msg) bool {
			return m == ui.ToastMsg{Tone: theme.ToneInfo, Text: "add"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := openFixturePalette(t)
			f.send(typeText(tt.query)...)
			cmd := f.send(key("enter"))
			if f.m.palette.open {
				t.Fatal("palette still open")
			}
			if f.m.active != tt.wantActive {
				t.Fatalf("active = %d, want %d", f.m.active, tt.wantActive)
			}
			if tt.wantMsg == nil {
				if cmd != nil {
					t.Fatal("unexpected command")
				}
				return
			}
			msgs := collect(cmd)
			if len(msgs) != 1 || !tt.wantMsg(msgs[0]) {
				t.Fatalf("messages = %#v", msgs)
			}
		})
	}
}

func TestPaletteEscAndKeysDoNotLeak(t *testing.T) {
	f := openFixturePalette(t)
	if cmd := f.send(key("q")); isQuit(cmd) {
		t.Fatal("q quit while the palette was open")
	}
	if f.m.palette.query != "q" {
		t.Fatalf("query = %q", f.m.palette.query)
	}
	f.send(tea.PasteMsg{Content: "uit\x1b[2J"})
	if f.m.palette.query != "quit" {
		t.Fatalf("query after paste = %q", f.m.palette.query)
	}
	f.send(key("esc"))
	if f.m.palette.open {
		t.Fatal("esc did not close")
	}
	for i, rec := range f.recs {
		if len(received[tea.KeyPressMsg](rec)) > 0 {
			t.Errorf("screen %d received palette keys", i)
		}
	}
	f.send(key("ctrl+k"))
	if f.m.palette.query != "" {
		t.Fatal("reopened palette should start empty")
	}
}

func TestPaletteView(t *testing.T) {
	th := theme.Default()
	for _, size := range []struct{ w, h int }{{80, 24}, {150, 44}, {44, 12}} {
		f := openFixturePalette(t)
		f.send(typeText(strings.Repeat("o", 80))...)
		f.send(key("backspace"))
		for _, q := range []string{"", "o"} {
			f.m.palette = f.m.palette.withQuery(q)
			box := f.m.palette.view(th, size.w, size.h)
			pw, rows := f.m.palette.size(size.w, size.h)
			assertFrame(t, box, pw, rows+paletteChrome)
		}
		f.m.palette = f.m.palette.withQuery(strings.Repeat("long query ", 20))
		box := f.m.palette.view(th, size.w, size.h)
		pw, rows := f.m.palette.size(size.w, size.h)
		assertFrame(t, box, pw, rows+paletteChrome)
		if !strings.Contains(ansi.Strip(box), "Command") {
			t.Fatalf("missing title:\n%s", ansi.Strip(box))
		}
	}
}

func TestPaletteScrollsToSelection(t *testing.T) {
	th := theme.Default()
	f := openFixturePalette(t)
	for range 10 {
		f.send(key("down"))
	}
	box := ansi.Strip(f.m.palette.view(th, 80, 20))
	if !strings.Contains(box, selectedTitle(f)) {
		t.Fatalf("selected %q not visible:\n%s", selectedTitle(f), box)
	}
}
