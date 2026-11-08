package screens

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/CraguS/vigil/internal/format"
	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/theme"
	"github.com/CraguS/vigil/internal/tui/ui"
	"github.com/CraguS/vigil/internal/widgets"
)

// overviewMode is the presentation of the server list.
type overviewMode int

const (
	overviewList overviewMode = iota
	overviewTiles
)

// Overview layout constants.
const (
	tileMinWidth   = 32
	tileMaxWidth   = 56
	tileHeight     = 8
	bigSummaryH    = 5
	bigSummaryMinH = 28
	stripGapMinH   = 16
)

// overviewAction is a palette-triggered action of the Overview screen.
type overviewAction int

const (
	overviewToggleMode overviewAction = iota
	overviewSetSort
	overviewStartFilter
	overviewPoll
)

// overviewActionMsg carries a palette command back into Update.
type overviewActionMsg struct {
	action overviewAction
	sort   sortKey
}

// overview lists every server as a table or as a grid of tiles.
type overview struct {
	d         *ui.Deps
	t         *theme.Theme
	fleet     ui.Fleet
	mode      overviewMode
	sort      sortKey
	selected  string
	filter    textInput
	filtering bool
	// tileCols is the column count of the last rendered tile grid; it lets
	// up/down move by one grid row.
	tileCols int
}

func newOverview(d *ui.Deps) *overview {
	return &overview{
		d:        d,
		t:        themeOf(d),
		fleet:    initialFleet(d),
		filter:   newInput("name, host or tag"),
		tileCols: 1,
	}
}

func (s *overview) Tab() ui.TabID   { return ui.TabOverview }
func (s *overview) Title() string   { return "Overview" }
func (s *overview) Icon() string    { return "◉" }
func (s *overview) Init() tea.Cmd   { return nil }
func (s *overview) Capturing() bool { return s.filtering }

// rows returns the filtered and sorted rows.
func (s *overview) rows() []serverRow {
	all := buildRows(s.fleet)
	q := s.filter.Value()
	out := all[:0]
	for _, r := range all {
		if matchesFilter(r.Srv, q) {
			out = append(out, r)
		}
	}
	sortRows(out, s.sort)
	return out
}

// syncSelection keeps the selection on a visible server.
func (s *overview) syncSelection() {
	rows := s.rows()
	if rowIndex(rows, s.selected) >= 0 {
		return
	}
	s.selected = ""
	if len(rows) > 0 {
		s.selected = rows[0].Srv.ID
	}
}

// move shifts the selection by delta rows of the current ordering.
func (s *overview) move(delta int) {
	rows := s.rows()
	i := stepIndex(rowIndex(rows, s.selected), delta, len(rows))
	if i >= 0 {
		s.selected = rows[i].Srv.ID
	}
}

// Update implements ui.Screen.
func (s *overview) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case ui.FleetMsg:
		s.fleet = msg.Fleet
		s.syncSelection()
	case overviewActionMsg:
		return s, s.apply(msg)
	case tea.PasteMsg:
		if s.filtering {
			s.filter.Insert(msg.Content)
			s.syncSelection()
		}
	case tea.KeyPressMsg:
		if s.filtering {
			s.filterKey(msg)
			return s, nil
		}
		return s, s.key(msg)
	}
	return s, nil
}

// apply runs a palette action.
func (s *overview) apply(msg overviewActionMsg) tea.Cmd {
	switch msg.action {
	case overviewToggleMode:
		s.toggleMode()
	case overviewSetSort:
		s.sort = msg.sort
	case overviewStartFilter:
		s.filtering = true
	case overviewPoll:
		return s.pollCmd()
	}
	return nil
}

func (s *overview) toggleMode() {
	if s.mode == overviewList {
		s.mode = overviewTiles
		return
	}
	s.mode = overviewList
}

// filterKey edits the filter while it captures input.
func (s *overview) filterKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc":
		s.filter.SetValue("")
		s.filtering = false
	case "enter":
		s.filtering = false
	case "up":
		s.move(-1)
	case "down":
		s.move(1)
	default:
		s.filter.Update(msg)
	}
	s.syncSelection()
}

// key handles navigation and actions.
func (s *overview) key(msg tea.KeyPressMsg) tea.Cmd {
	vertical := 1
	if s.mode == overviewTiles {
		vertical = max(s.tileCols, 1)
	}
	switch msg.String() {
	case "j", "down":
		s.move(vertical)
	case "k", "up":
		s.move(-vertical)
	case "h", "left":
		if s.mode == overviewTiles {
			s.move(-1)
		}
	case "l", "right":
		if s.mode == overviewTiles {
			s.move(1)
			return nil
		}
		return s.detailCmd()
	case "g", "home":
		s.move(-len(s.fleet.Servers))
	case "G", "end":
		s.move(len(s.fleet.Servers))
	case "pgdown":
		s.move(10)
	case "pgup":
		s.move(-10)
	case "enter":
		return s.shellCmd()
	case "d", "space":
		return s.detailCmd()
	case "v":
		s.toggleMode()
	case "s":
		s.sort = (s.sort + 1) % sortKeyCount
	case "/":
		s.filtering = true
	case "esc":
		s.filter.SetValue("")
		s.syncSelection()
	case "r":
		return s.pollCmd()
	}
	return nil
}

func (s *overview) shellCmd() tea.Cmd {
	if s.selected == "" {
		return nil
	}
	return emit(ui.OpenShellMsg{ServerID: s.selected})
}

func (s *overview) detailCmd() tea.Cmd {
	if s.selected == "" {
		return nil
	}
	return emit(ui.ShowDetailMsg{ServerID: s.selected})
}

// pollCmd triggers an immediate poll of the selected server.
func (s *overview) pollCmd() tea.Cmd {
	return pollServer(s.d, s.fleet, s.selected)
}

// pollServer asks the collector to poll id now and confirms with a toast.
func pollServer(d *ui.Deps, f ui.Fleet, id string) tea.Cmd {
	if id == "" {
		return nil
	}
	if d == nil || d.PollNow == nil {
		return ui.Toast(theme.ToneWarn, "Polling is not available in this session")
	}
	name := id
	for _, srv := range f.Servers {
		if srv.ID == id {
			name = clean(srv.Name)
		}
	}
	poll := d.PollNow
	return func() tea.Msg {
		poll(id)
		return ui.ToastMsg{Tone: theme.ToneInfo, Text: "Polling " + name + "…"}
	}
}

// Hints implements ui.Screen.
func (s *overview) Hints() []ui.Hint {
	if s.filtering {
		return []ui.Hint{hint("enter", "apply"), hint("esc", "clear"), hint("↑↓", "move")}
	}
	if len(s.fleet.Servers) == 0 {
		return []ui.Hint{hint("2", "add server")}
	}
	nav := hint("j/k", "move")
	if s.mode == overviewTiles {
		nav = hint("hjkl", "move")
	}
	return []ui.Hint{
		nav,
		hint("enter", "shell"),
		hint("d", "detail"),
		hint("v", "tiles/list"),
		hint("s", "sort "+((s.sort+1)%sortKeyCount).String()),
		hint("/", "filter"),
		hint("r", "poll"),
	}
}

// Commands implements ui.Screen.
func (s *overview) Commands() []ui.Command {
	act := func(a overviewAction, k sortKey) func() tea.Cmd {
		return func() tea.Cmd { return emit(overviewActionMsg{action: a, sort: k}) }
	}
	return []ui.Command{
		{Title: "Toggle tiles/list", Hint: "v", Run: act(overviewToggleMode, 0)},
		{Title: "Sort by name", Hint: "s", Run: act(overviewSetSort, sortName)},
		{Title: "Sort by CPU", Hint: "s", Run: act(overviewSetSort, sortCPU)},
		{Title: "Sort by memory", Hint: "s", Run: act(overviewSetSort, sortMem)},
		{Title: "Sort by status", Hint: "s", Run: act(overviewSetSort, sortStatus)},
		{Title: "Filter servers", Hint: "/", Run: act(overviewStartFilter, 0)},
		{Title: "Poll selected server now", Hint: "r", Run: act(overviewPoll, 0)},
	}
}

// View implements ui.Screen.
func (s *overview) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if len(s.fleet.Servers) == 0 {
		return s.emptyView(width, height)
	}
	all := buildRows(s.fleet)
	var blocks []string
	used := 0
	if big, ok := s.bigSummary(all, width); ok && height >= bigSummaryMinH {
		blocks = append(blocks, big)
		used += bigSummaryH
	} else {
		blocks = append(blocks, s.summaryStrip(all, width))
		used++
		if height >= stripGapMinH {
			blocks = append(blocks, "")
			used++
		}
	}
	if s.filtering && height-used > 3 {
		blocks = append(blocks, s.filterBar(width))
		used++
	}
	if mainH := height - used; mainH > 0 {
		rows := s.rows()
		if s.mode == overviewTiles {
			blocks = append(blocks, s.tiles(rows, width, mainH))
		} else {
			blocks = append(blocks, s.list(rows, width, mainH))
		}
	}
	return widgets.Fit(widgets.JoinV(blocks...), width, height)
}

func (s *overview) emptyView(w, h int) string {
	t := s.t
	return messagePanel(t, w, h, "Overview", "", []string{
		fg(t.P.Accent, "◉"),
		"",
		fg(t.P.Text, bold("No servers yet")),
		fg(t.P.Muted, "Press ") + fg(t.P.Accent, bold("2")) + fg(t.P.Muted, " to add one, or open the palette"),
		fg(t.P.Muted, "and choose “Add server”."),
	})
}

// fleetTotals aggregates the summary numbers.
type fleetTotals struct {
	online, offline, warn int
	cpuAvg                float64
	rx, tx                float64
	cpuHist               []float64
}

func totals(rows []serverRow) fleetTotals {
	var ft fleetTotals
	var hist []float64
	for _, r := range rows {
		if r.Has && r.Snap.State == metrics.StateOffline {
			ft.offline++
		}
		if !r.Online() {
			continue
		}
		ft.online++
		if r.Status.Rank == 1 || r.Status.Rank == 2 {
			ft.warn++
		}
		ft.cpuAvg += r.Snap.CPU.Total
		ft.rx += r.Snap.Net.RxBps
		ft.tx += r.Snap.Net.TxBps
		hist = sumSeries(hist, r.Hist.CPU)
	}
	if ft.online > 0 {
		ft.cpuAvg /= float64(ft.online)
		for i := range hist {
			hist[i] /= float64(ft.online)
		}
	}
	ft.cpuHist = hist
	return ft
}

// summaryStrip is the one-line fleet summary.
func (s *overview) summaryStrip(rows []serverRow, w int) string {
	t := s.t
	ft := totals(rows)
	count := func(c theme.RGB, glyph string, n int, label string) string {
		if n == 0 {
			c = t.P.Faint
		}
		return fg(c, glyph) + " " + fg(t.P.Text, bold(strconv.Itoa(n))) + " " + fg(t.P.Muted, label)
	}
	left := " " + strings.Join([]string{
		count(t.P.Good, glyphDot, ft.online, "online"),
		count(t.P.Bad, glyphDot, ft.offline, "offline"),
		count(t.P.Warn, glyphWarn, ft.warn, "warn"),
	}, "   ")
	cpu := labelValue(t, "cpu avg", pctText(t, s.fleet.Settings, metricCPU, ft.cpuAvg, ft.online == 0))
	spark := widgets.Sparkline(tail(ft.cpuHist, 12), 12, 100, pctColor(t, s.fleet.Settings, metricCPU, ft.cpuAvg, false))
	net := fg(t.P.Accent2, glyphDown) + " " + fg(t.P.Text, format.Bits(ft.rx)) + "  " +
		fg(t.P.Accent, glyphUp) + " " + fg(t.P.Text, format.Bits(ft.tx))
	variants := []string{
		cpu + " " + spark + "    " + net + " ",
		cpu + "    " + net + " ",
		net + " ",
		"",
	}
	lw := ansi.StringWidth(left)
	for _, right := range variants {
		if lw+2+ansi.StringWidth(right) <= w {
			return widgets.SpreadLine(left, right, w)
		}
	}
	return widgets.FitLine(left, w)
}

// summaryCard is one KPI of the big summary.
type summaryCard struct {
	title string
	value string
	unit  string
	color theme.RGB
}

// bigSummary renders the KPI cards with 7-segment numbers. ok is false
// when the cards do not fit into w.
func (s *overview) bigSummary(rows []serverRow, w int) (string, bool) {
	t := s.t
	ft := totals(rows)
	dimIfZero := func(c theme.RGB, n int) theme.RGB {
		if n == 0 {
			return t.P.Faint
		}
		return c
	}
	rxNum, rxUnit := splitUnit(format.Bits(ft.rx))
	txNum, txUnit := splitUnit(format.Bits(ft.tx))
	cards := []summaryCard{
		{"Online", strconv.Itoa(ft.online), "", dimIfZero(t.P.Good, ft.online)},
		{"Offline", strconv.Itoa(ft.offline), "", dimIfZero(t.P.Bad, ft.offline)},
		{"Warnings", strconv.Itoa(ft.warn), "", dimIfZero(t.P.Warn, ft.warn)},
		{"CPU avg", format.Percent(ft.cpuAvg), "", pctColor(t, s.fleet.Settings, metricCPU, ft.cpuAvg, ft.online == 0)},
		{"Net in", rxNum, rxUnit, t.P.Accent2},
		{"Net out", txNum, txUnit, t.P.Accent},
	}
	widths := widgets.SplitWidths(w, len(cards), 1)
	blocks := make([]string, 0, 2*len(cards))
	for i, c := range cards {
		iw, _ := widgets.PanelInner(widths[i], bigSummaryH)
		unitW := 0
		if c.unit != "" {
			unitW = ansi.StringWidth(c.unit) + 1
		}
		if widgets.BigNumberWidth(c.value)+unitW > iw {
			return "", false
		}
		if i > 0 {
			blocks = append(blocks, widgets.Blank(1, bigSummaryH))
		}
		blocks = append(blocks, s.card(c, widths[i]))
	}
	return widgets.JoinH(blocks...), true
}

func (s *overview) card(c summaryCard, w int) string {
	t := s.t
	iw, _ := widgets.PanelInner(w, bigSummaryH)
	digits := strings.Split(widgets.BigNumber(c.value, c.color), "\n")
	unitW := ansi.StringWidth(c.unit)
	lines := make([]string, len(digits))
	for i, d := range digits {
		suffix := ""
		if c.unit != "" {
			suffix = " " + strings.Repeat(" ", unitW)
			if i == len(digits)-1 {
				suffix = " " + fg(t.P.Muted, c.unit)
			}
		}
		lines[i] = widgets.Center(d+suffix, iw)
	}
	return widgets.Panel(t, widgets.PanelOpts{
		Width: w, Height: bigSummaryH, Title: c.title, Body: strings.Join(lines, "\n"),
	})
}

// filterBar shows the filter input while typing.
func (s *overview) filterBar(w int) string {
	t := s.t
	prefix := " " + fg(t.P.Accent, bold("/")) + " "
	pw := ansi.StringWidth(prefix)
	inputW := clamp(w-pw-1, 1, 48)
	return widgets.FitLine(prefix+s.filter.View(t, inputW, true), w)
}

// listInfo is the info text of the list/tile panel.
func (s *overview) listInfo(shown int) string {
	parts := []string{"sort " + s.sort.String()}
	if q := s.filter.Value(); q != "" {
		parts = append(parts, "filter “"+q+"”")
	}
	parts = append(parts, strconv.Itoa(shown)+"/"+strconv.Itoa(len(s.fleet.Servers)))
	return strings.Join(parts, glyphSep)
}

// list renders the table panel.
func (s *overview) list(rows []serverRow, w, h int) string {
	iw, ih := widgets.PanelInner(w, h)
	cols := fitColumns(s.listColumns(), iw)
	columns, cells := tableData(cols, rows)
	empty := "No servers match “" + s.filter.Value() + "”"
	table := widgets.Table(s.t, widgets.TableOpts{
		Width: iw, Height: ih, Columns: columns, Rows: cells,
		Selected: rowIndex(rows, s.selected), Empty: empty,
	})
	return widgets.Panel(s.t, widgets.PanelOpts{
		Width: w, Height: h, Title: "Servers", Info: s.listInfo(len(rows)),
		Focused: !s.filtering, Body: table,
	})
}

// listColumns defines every possible list column with its priority.
func (s *overview) listColumns() []prioColumn[serverRow] {
	t := s.t
	set := s.fleet.Settings
	now := s.fleet.Now
	none := fg(t.P.Faint, glyphNone)
	live := func(r serverRow, render func() string) string {
		if !r.Online() {
			return none
		}
		return render()
	}
	const sparkW = 10
	return []prioColumn[serverRow]{
		{widgets.Column{Width: 1}, 100, func(r serverRow) string {
			return fg(t.ToneColor(r.Status.Tone), glyphDot)
		}},
		{widgets.Column{Title: "NAME", Min: 10, Flex: 2}, 99, func(r serverRow) string {
			return fg(t.P.Text, r.Name())
		}},
		{widgets.Column{Title: "HOST", Min: 12, Flex: 2}, 52, func(r serverRow) string {
			return fg(t.P.Muted, clean(r.Srv.Host))
		}},
		{widgets.Column{Title: "STATUS", Width: 8}, 60, func(r serverRow) string {
			return fg(t.ToneColor(r.Status.Tone), r.Status.Label)
		}},
		{widgets.Column{Title: "CPU", Width: 5, Align: widgets.AlignRightCol}, 90, func(r serverRow) string {
			return live(r, func() string { return pctText(t, set, metricCPU, r.Snap.CPU.Total, false) })
		}},
		{widgets.Column{Title: "CPU 60s", Width: sparkW}, 40, func(r serverRow) string {
			return live(r, func() string {
				return widgets.Sparkline(tail(r.Hist.CPU, sparkW), sparkW, 100, pctColor(t, set, metricCPU, r.Snap.CPU.Total, false))
			})
		}},
		{widgets.Column{Title: "RAM", Width: 5, Align: widgets.AlignRightCol}, 85, func(r serverRow) string {
			return live(r, func() string { return pctText(t, set, metricMem, r.Snap.Mem.UsedPct(), false) })
		}},
		{widgets.Column{Title: "DISK", Width: 5, Align: widgets.AlignRightCol}, 70, func(r serverRow) string {
			return live(r, func() string {
				p, _ := r.Snap.MaxDiskPct()
				return pctText(t, set, metricDisk, p, false)
			})
		}},
		{widgets.Column{Title: "RX", Width: 9, Align: widgets.AlignRightCol}, 65, func(r serverRow) string {
			return live(r, func() string { return fg(t.P.Accent2, format.Bits(r.Snap.Net.RxBps)) })
		}},
		{widgets.Column{Title: "TX", Width: 9, Align: widgets.AlignRightCol}, 64, func(r serverRow) string {
			return live(r, func() string { return fg(t.P.Accent, format.Bits(r.Snap.Net.TxBps)) })
		}},
		{widgets.Column{Title: "NET 60s", Width: sparkW}, 35, func(r serverRow) string {
			return live(r, func() string {
				return widgets.Sparkline(tail(sumSeries(r.Hist.RxBps, r.Hist.TxBps), sparkW), sparkW, 0, t.P.Accent2)
			})
		}},
		{widgets.Column{Title: "LOAD", Width: 5, Align: widgets.AlignRightCol}, 45, func(r serverRow) string {
			return live(r, func() string { return fg(t.P.Text, loadText(r.Snap.Load.One)) })
		}},
		{widgets.Column{Title: "LAT", Width: 6, Align: widgets.AlignRightCol}, 55, func(r serverRow) string {
			return live(r, func() string { return fg(t.P.Muted, format.Latency(r.Snap.Latency)) })
		}},
		{widgets.Column{Title: "UPTIME", Width: 12}, 50, func(r serverRow) string {
			switch {
			case r.Online():
				return fg(t.P.Muted, format.Duration(r.Snap.Uptime))
			case r.Has && r.Snap.State == metrics.StateOffline:
				return fg(t.P.Bad, lastSeenText(r.Snap, now))
			default:
				return none
			}
		}},
	}
}

// tiles renders the grid of server tiles that fits into w×h, scrolled so
// the selected tile is visible.
func (s *overview) tiles(rows []serverRow, w, h int) string {
	if len(rows) == 0 {
		return messagePanel(s.t, w, h, "Servers", s.listInfo(0), []string{
			fg(s.t.P.Muted, "No servers match “"+s.filter.Value()+"”"),
		})
	}
	cols := max((w+1)/(tileMinWidth+1), 1)
	if len(rows) < cols {
		cols = max(len(rows), (w+1)/(tileMaxWidth+1), 1)
	}
	s.tileCols = cols
	widths := widgets.SplitWidths(w, cols, 1)
	tileH := min(tileHeight, h)
	visible := max(h/tileH, 1)
	sel := max(rowIndex(rows, s.selected), 0)
	gridRows := (len(rows) + cols - 1) / cols
	first := clamp(sel/cols-visible+1, 0, max(gridRows-visible, 0))

	var lines []string
	for gr := first; gr < min(first+visible, gridRows); gr++ {
		blocks := make([]string, 0, 2*cols)
		for c := range cols {
			if c > 0 {
				blocks = append(blocks, widgets.Blank(1, tileH))
			}
			i := gr*cols + c
			if i >= len(rows) {
				blocks = append(blocks, widgets.Blank(widths[c], tileH))
				continue
			}
			blocks = append(blocks, s.tile(rows[i], widths[c], tileH, i == sel))
		}
		lines = append(lines, widgets.JoinH(blocks...))
	}
	return widgets.Fit(widgets.JoinV(lines...), w, h)
}

// tile renders one server as a small panel.
func (s *overview) tile(r serverRow, w, h int, selected bool) string {
	iw, _ := widgets.PanelInner(w, h)
	return widgets.Panel(s.t, widgets.PanelOpts{
		Width:   w,
		Height:  h,
		Title:   r.Name(),
		Info:    widgets.Badge(s.t, r.Status.Label, r.Status.Tone),
		Focused: selected,
		Body:    strings.Join(s.tileBody(r, iw), "\n"),
	})
}

// tileBody is the content of one tile, iw cells wide.
func (s *overview) tileBody(r serverRow, iw int) []string {
	t := s.t
	set := s.fleet.Settings
	if !r.Online() {
		return s.tileOffline(r)
	}
	const labelW, valueW = 5, 8
	label := func(text string) string { return widgets.FitLine(fg(t.P.Muted, text), labelW) }
	sparkW := max(iw-labelW-valueW-1, 0)
	cpu := r.Snap.CPU.Total
	mem := r.Snap.Mem.UsedPct()
	disk, _ := r.Snap.MaxDiskPct()
	cpuColor := pctColor(t, set, metricCPU, cpu, false)
	net := r.Snap.Net.RxBps + r.Snap.Net.TxBps

	barW := max(iw-labelW-5, 0)
	bar := func(name string, kind metricKind, pct float64) string {
		c := pctColor(t, set, kind, pct, false)
		return label(name) + widgets.Progress(t, barW, pct/100, c) + widgets.AlignRight(fg(c, format.Percent(pct)), 5)
	}
	return []string{
		label("CPU") + widgets.Sparkline(tail(r.Hist.CPU, sparkW), sparkW, 100, cpuColor) + " " +
			widgets.AlignRight(fg(cpuColor, format.Percent(cpu)), valueW),
		label("NET") + widgets.Sparkline(tail(sumSeries(r.Hist.RxBps, r.Hist.TxBps), sparkW), sparkW, 0, t.P.Accent2) + " " +
			widgets.AlignRight(fg(t.P.Accent2, format.Bits(net)), valueW),
		bar("cpu", metricCPU, cpu),
		bar("mem", metricMem, mem),
		bar("disk", metricDisk, disk),
		joinDots(t,
			labelValue(t, "lat", fg(t.P.Text, format.Latency(r.Snap.Latency))),
			labelValue(t, "load", fg(t.P.Text, loadText(r.Snap.Load.One))),
			labelValue(t, "up", fg(t.P.Text, format.Duration(r.Snap.Uptime))),
		),
	}
}

// tileOffline is the tile content for servers without live data.
func (s *overview) tileOffline(r serverRow) []string {
	t := s.t
	host := fg(t.P.Faint, clean(r.Srv.Host))
	if !r.Has || r.Snap.State != metrics.StateOffline {
		return []string{"", fg(t.P.Muted, "waiting for first poll…"), "", host}
	}
	reason := clean(r.Snap.Err)
	if reason == "" {
		reason = "unreachable"
	}
	return []string{
		"",
		fg(t.P.Bad, bold(glyphCross+" "+reason)),
		fg(t.P.Faint, lastSeenText(r.Snap, s.fleet.Now)),
		"",
		host,
	}
}
