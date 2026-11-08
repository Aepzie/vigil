package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/CraguS/vigil/internal/fuzzy"
	"github.com/CraguS/vigil/internal/sanitize"
	"github.com/CraguS/vigil/internal/theme"
	"github.com/CraguS/vigil/internal/tui/ui"
	"github.com/CraguS/vigil/internal/widgets"
)

const (
	paletteMinWidth = 40
	paletteMaxWidth = 72
	paletteMaxRows  = 10
	paletteMinRows  = 3
	// paletteChrome is the number of modal lines besides result rows:
	// two borders, the input, the separator and the footer.
	paletteChrome = 5
)

// rankCommands ranks palette titles for a query. It is a variable so
// tests can pin the ranking independently of the fuzzy implementation.
var rankCommands = fuzzy.Rank

// paletteEntry is one command of the palette.
type paletteEntry struct {
	title string
	hint  string
	// tab is the screen index to activate before running, or -1.
	tab int
	// run returns the command to execute; nil only switches tabs.
	run func() tea.Cmd
}

// palette is the immutable state of the command palette.
type palette struct {
	open    bool
	query   string
	entries []paletteEntry
	titles  []string
	ranked  []fuzzy.Ranked
	sel     int
}

// openPalette opens the palette over entries with an empty query.
func openPalette(entries []paletteEntry) palette {
	titles := make([]string, len(entries))
	for i, e := range entries {
		titles[i] = e.title
	}
	p := palette{open: true, entries: entries, titles: titles}
	return p.withQuery("")
}

// withQuery re-ranks the entries for q and selects the best match.
func (p palette) withQuery(q string) palette {
	p.query = q
	p.ranked = rankCommands(q, p.titles)
	p.sel = 0
	return p
}

// typed appends text to the query.
func (p palette) typed(text string) palette {
	return p.withQuery(p.query + text)
}

// backspace removes the last rune of the query.
func (p palette) backspace() palette {
	if p.query == "" {
		return p
	}
	r := []rune(p.query)
	return p.withQuery(string(r[:len(r)-1]))
}

// move shifts the selection by delta, wrapping around.
func (p palette) move(delta int) palette {
	n := len(p.ranked)
	if n == 0 {
		return p
	}
	p.sel = ((p.sel+delta)%n + n) % n
	return p
}

// selected returns the highlighted entry.
func (p palette) selected() (paletteEntry, bool) {
	if p.sel < 0 || p.sel >= len(p.ranked) {
		return paletteEntry{}, false
	}
	idx := p.ranked[p.sel].Index
	if idx < 0 || idx >= len(p.entries) {
		return paletteEntry{}, false
	}
	return p.entries[idx], true
}

// size returns the modal width and the number of result rows for a
// frame of w×h.
func (p palette) size(w, h int) (int, int) {
	pw := min(max(w*3/5, paletteMinWidth), paletteMaxWidth, max(w-4, 0))
	rows := min(paletteMaxRows, max(h-paletteChrome-4, paletteMinRows))
	return pw, rows
}

// view renders the palette modal for a frame of w×h.
func (p palette) view(th *theme.Theme, w, h int) string {
	pw, rows := p.size(w, h)
	inner := max(pw-2, 0)
	bg := th.P.Surface
	body := make([]string, 0, rows+3)
	body = append(body, paint(p.inputLine(th, inner), inner, bg))
	body = append(body, paint(th.PanelBorder.Render(strings.Repeat("─", inner)), inner, bg))
	offset := max(p.sel-rows+1, 0)
	for i := range rows {
		idx := offset + i
		switch {
		case idx < len(p.ranked):
			body = append(body, p.row(th, idx, inner))
		case i == 0:
			body = append(body, paint("  "+th.Faint.Render("no matching commands"), inner, bg))
		default:
			body = append(body, paint("", inner, bg))
		}
	}
	keys := th.Faint.Render("↑↓ select · enter run · esc close") + " "
	body = append(body, paint(widgets.AlignRight(keys, inner), inner, bg))
	info := strconv.Itoa(len(p.ranked)) + "/" + strconv.Itoa(len(p.entries))
	return modal(th, "Command", info, pw, body)
}

// inputLine renders "› query█", keeping the tail of long queries.
func (p palette) inputLine(th *theme.Theme, inner int) string {
	prompt := " " + th.Accent.Bold(true).Render("›") + " "
	cursor := th.Cursor.Render(" ")
	room := max(inner-5, 1)
	q := p.query
	if over := ansi.StringWidth(q) - room; over > 0 {
		q = ansi.TruncateLeft(q, over+1, "…")
	}
	if q == "" {
		return prompt + cursor + th.Placeholder.Render(" type a command or server name")
	}
	return prompt + th.Text.Render(q) + cursor
}

// row renders one result; the selected row gets the Surface2 background
// and an accent marker.
func (p palette) row(th *theme.Theme, idx, inner int) string {
	r := p.ranked[idx]
	e := p.entries[r.Index]
	selected := idx == p.sel
	bg, base, marker := th.P.Surface, th.Text, " "
	if selected {
		bg, base, marker = th.P.Surface2, th.Strong, th.Accent.Render("▌")
	}
	hint := ""
	if e.hint != "" {
		hint = th.Faint.Render(e.hint) + " "
	}
	titleW := max(inner-2-ansi.StringWidth(hint)-1, 1)
	title := fuzzy.Highlight(e.title, r.Positions, base, th.MatchHit)
	if ansi.StringWidth(title) > titleW {
		title = ansi.Truncate(title, titleW, "…")
	}
	left := marker + " " + title
	gap := max(inner-ansi.StringWidth(left)-ansi.StringWidth(hint), 0)
	return paint(left+strings.Repeat(" ", gap)+hint, inner, bg)
}

// paletteEntries lists the commands offered by the palette: tab
// switches, global actions, per-server actions, the active screen's
// commands and then the other screens' commands prefixed by their title.
func (m Model) paletteEntries() []paletteEntry {
	entries := make([]paletteEntry, 0, len(m.screens)+2+2*len(m.fleet.Servers))
	for i, s := range m.screens {
		entries = append(entries, paletteEntry{title: "Go to " + s.Title(), hint: strconv.Itoa(i + 1), tab: i})
	}
	entries = append(entries,
		paletteEntry{title: "Toggle compatibility mode", hint: "f2", tab: -1, run: emit(cycleCompatMsg{})},
		paletteEntry{title: "Quit", hint: "q", tab: -1, run: func() tea.Cmd { return tea.Quit }},
	)
	for _, srv := range m.fleet.Servers {
		name := sanitize.Line(srv.Name)
		entries = append(entries,
			paletteEntry{title: "Open shell: " + name, tab: -1, run: emit(ui.OpenShellMsg{ServerID: srv.ID})},
			paletteEntry{title: "Show detail: " + name, tab: -1, run: emit(ui.ShowDetailMsg{ServerID: srv.ID})},
		)
	}
	if s := m.activeScreen(); s != nil {
		entries = appendScreenCommands(entries, s, "", -1)
	}
	for i, s := range m.screens {
		if i != m.active {
			entries = appendScreenCommands(entries, s, s.Title()+" › ", i)
		}
	}
	return entries
}

// appendScreenCommands adds the commands of s, titled with prefix and
// activating tab before they run.
func appendScreenCommands(entries []paletteEntry, s ui.Screen, prefix string, tab int) []paletteEntry {
	for _, c := range s.Commands() {
		if c.Run == nil {
			continue
		}
		entries = append(entries, paletteEntry{
			title: prefix + sanitize.Line(c.Title),
			hint:  sanitize.Line(c.Hint),
			tab:   tab,
			run:   c.Run,
		})
	}
	return entries
}

// emit returns a palette action that sends msg.
func emit(msg tea.Msg) func() tea.Cmd {
	return func() tea.Cmd { return func() tea.Msg { return msg } }
}

// paletteKey handles a key press while the palette is open.
func (m Model) paletteKey(k tea.KeyPressMsg) (Model, tea.Cmd) {
	switch k.String() {
	case keyEsc, keyPaletteAlt:
		m.palette = palette{}
		return m, nil
	case keyEnter:
		return m.runPalette()
	case keyUp, keyPrev, keyPrevTab:
		m.palette = m.palette.move(-1)
	case keyDown, keyNext, keyNextTab:
		m.palette = m.palette.move(1)
	case keyBackspace:
		m.palette = m.palette.backspace()
	case keyClear:
		m.palette = m.palette.withQuery("")
	default:
		if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			m.palette = m.palette.typed(sanitize.Line(k.Text))
		}
	}
	return m, nil
}

// runPalette closes the palette and runs the selected entry.
func (m Model) runPalette() (Model, tea.Cmd) {
	entry, ok := m.palette.selected()
	m.palette = palette{}
	if !ok {
		return m, nil
	}
	if entry.tab >= 0 && entry.tab < len(m.screens) {
		m.active = entry.tab
	}
	if entry.run == nil {
		return m, nil
	}
	return m, entry.run()
}
