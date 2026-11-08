package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/alerts"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
)

var frameSizes = []struct{ w, h int }{
	{80, 24},
	{150, 44},
	{220, 60},
	{60, 20},
	{30, 8},
}

func TestFrameDimensions(t *testing.T) {
	setups := []struct {
		name  string
		opts  Options
		apply func(f *fixture)
	}{
		{name: "dashboard", opts: Options{SkipSplash: true}},
		{name: "splash", opts: Options{}},
		{name: "splash late frame", opts: Options{}, apply: func(f *fixture) { f.send(splashMsg{frame: splashFrames - 1}) }},
		{name: "palette", opts: Options{SkipSplash: true}, apply: func(f *fixture) { f.send(key(":")) }},
		{name: "help", opts: Options{SkipSplash: true}, apply: func(f *fixture) { f.send(key("?")) }},
		{name: "toasts", opts: Options{SkipSplash: true}, apply: func(f *fixture) {
			for _, tone := range []theme.Tone{theme.ToneInfo, theme.ToneGood, theme.ToneWarn, theme.ToneBad, theme.ToneBad} {
				f.send(ui.ToastMsg{Tone: tone, Text: strings.Repeat("a very long toast message ", 6)})
			}
		}},
		{name: "fleet with alerts", opts: Options{SkipSplash: true}, apply: func(f *fixture) {
			f.send(ui.FleetMsg{Fleet: ui.Fleet{
				Servers: []store.Server{{ID: "a", Name: "web-01"}, {ID: "b", Name: "db-01"}},
				Active:  []alerts.Alert{{Severity: alerts.SeverityCrit}},
			}})
		}},
	}
	for _, s := range setups {
		for _, size := range frameSizes {
			f := newFixture(t, s.opts)
			f.send(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			if s.apply != nil {
				s.apply(&f)
			}
			assertFrame(t, f.m.View().Content, size.w, size.h)
		}
	}
}

func TestViewProperties(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	v := f.m.View()
	if !v.AltScreen || v.WindowTitle != "vigil" || v.BackgroundColor == nil {
		t.Fatalf("view = alt %v title %q bg %v", v.AltScreen, v.WindowTitle, v.BackgroundColor)
	}
	if v.Content != "" {
		t.Fatalf("content before the first WindowSizeMsg = %q, want empty", v.Content)
	}
}

func TestRenderFrame(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := &ui.Deps{Store: st, Alerts: alerts.NewEngine(alerts.Thresholds{}), Version: "v0.1.0"}
	for _, size := range frameSizes {
		for tab := ui.TabOverview; tab <= ui.TabSettings; tab++ {
			assertFrame(t, RenderFrame(d, Options{}, size.w, size.h, tab), size.w, size.h)
		}
	}
	if RenderFrame(nil, Options{}, 0, 0, ui.TabOverview) != "" {
		t.Fatal("zero size frame should be empty")
	}
}

func TestRenderFrameActivatesTab(t *testing.T) {
	f := newFixture(t, Options{})
	frame := ansi.Strip(renderModelFrame(f.m, 100, 30, ui.TabAlerts))
	if !strings.Contains(frame, "Alerts content") {
		t.Fatalf("alerts screen not rendered:\n%s", frame)
	}
	if strings.Contains(frame, "fleet monitoring over ssh") {
		t.Fatal("splash rendered by RenderFrame")
	}
	if got := received[ui.FleetMsg](f.recs[0]); len(got) != 1 {
		t.Fatalf("overview got %d FleetMsg, want 1", len(got))
	}
}

func TestTooSmallNotice(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	f.send(tea.WindowSizeMsg{Width: 72, Height: 20})
	frame := ansi.Strip(f.m.View().Content)
	for _, want := range []string{"Terminal too small", "72×20", "need 80×24"} {
		if !strings.Contains(frame, want) {
			t.Errorf("notice lacks %q:\n%s", want, frame)
		}
	}
	f.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	if strings.Contains(ansi.Strip(f.m.View().Content), "too small") {
		t.Error("notice shown at the minimum size")
	}
}

func TestHeaderAndFooter(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	f.send(tea.WindowSizeMsg{Width: 150, Height: 40})
	f.send(ui.FleetMsg{Fleet: ui.Fleet{Active: []alerts.Alert{{Severity: alerts.SeverityWarn}, {Severity: alerts.SeverityWarn}}}})
	lines := strings.Split(ansi.Strip(f.m.View().Content), "\n")
	for _, want := range []string{"◆ vigil", "v0.1.0", "admin@vigil", "WARN"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("header lacks %q: %q", want, lines[0])
		}
	}
	if !strings.Contains(lines[1], "1 ◉ Overview") || !strings.Contains(lines[1], "Alerts 2") {
		t.Errorf("tabs = %q", lines[1])
	}
	footer := lines[len(lines)-1]
	for _, want := range []string{"enter open", "tab switch", "q quit"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer lacks %q: %q", want, footer)
		}
	}
	if strings.Index(footer, "enter open") > strings.Index(footer, "tab switch") {
		t.Errorf("screen hints must come before globals: %q", footer)
	}
}

func TestFooterDropsHintsThatDoNotFit(t *testing.T) {
	th := theme.Default()
	hints := []ui.Hint{{Key: "a", Desc: "one"}, {Key: "b", Desc: "two"}, {Key: "c", Desc: "three"}}
	got := ansi.Strip(joinHints(th, hints, 13))
	if got != "a one · b two" {
		t.Fatalf("joinHints = %q", got)
	}
}

func TestFooterShowsCompatMode(t *testing.T) {
	f := newFixture(t, Options{SkipSplash: true})
	f.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	f.send(key("f2"))
	f.send(key("f2"))
	lines := strings.Split(ansi.Strip(f.m.View().Content), "\n")
	if footer := lines[len(lines)-1]; !strings.Contains(footer, "compat ascii") {
		t.Fatalf("footer = %q", footer)
	}
}

func TestToastRendering(t *testing.T) {
	th := theme.Default()
	tests := []struct {
		name     string
		text     string
		maxWidth int
		wantW    int
	}{
		{"short uses min width", "ok", 100, toastMinWidth},
		{"long is capped", strings.Repeat("x", 200), 100, toastMaxWidth},
		{"narrow frame", strings.Repeat("x", 200), 30, 30},
		{"fits text", strings.Repeat("y", 30), 100, 30 + toastChrome},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := renderToast(th, toast{tone: theme.ToneWarn, text: tt.text}, tt.maxWidth)
			if got := ansi.StringWidth(line); got != tt.wantW {
				t.Fatalf("width = %d, want %d", got, tt.wantW)
			}
			if !strings.HasPrefix(ansi.Strip(line), "▌ ! ") {
				t.Fatalf("toast = %q", ansi.Strip(line))
			}
		})
	}
}

func TestSplashCompactLogo(t *testing.T) {
	th := theme.Default()
	frame := renderSplash(th, "v1", 3, 20, 10)
	assertFrame(t, frame, 20, 10)
	plain := ansi.Strip(frame)
	if !strings.Contains(plain, "fleet monitoring") && !strings.Contains(plain, "V I G I L") {
		t.Fatalf("splash = %q", plain)
	}
}

func TestPaintKeepsBackground(t *testing.T) {
	th := theme.Default()
	line := paint(th.Faint.Render("a")+"b", 6, th.P.Surface)
	if got := ansi.StringWidth(line); got != 6 {
		t.Fatalf("width = %d", got)
	}
	if strings.Count(line, th.P.Surface.BG()) < 2 {
		t.Fatalf("background not re-applied after reset: %q", line)
	}
}

func TestModalTop(t *testing.T) {
	th := theme.Default()
	for _, w := range []int{4, 10, 20, 60} {
		top := modalTop(th, "Command", "12/30", w)
		if got := ansi.StringWidth(top); got != w {
			t.Errorf("width %d: top is %d cells: %q", w, got, ansi.Strip(top))
		}
	}
}
