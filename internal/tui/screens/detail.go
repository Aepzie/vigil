package screens

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/CraguS/vigil/internal/format"
	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/theme"
	"github.com/CraguS/vigil/internal/tui/ui"
	"github.com/CraguS/vigil/internal/widgets"
)

// Detail layout constants.
const (
	detailChartMinW  = 44
	detailKPIMinW    = 30
	detailKPIMaxW    = 44
	detailVitalsMinH = 7
	detailVitalsMaxH = 16
	detailMinBand2H  = 6
	detailCoresMinH  = 12
	detailSideMinW   = 76
	detailChartMinH  = 8
	detailChartMaxH  = 12

	// detailFullHeaderMinH is the content height from which the header
	// is a bordered panel; below it a compact two-line header is used,
	// and below detailTwoLineHeaderMinH a single line.
	detailFullHeaderMinH    = 24
	detailTwoLineHeaderMinH = 12
)

// detailAction is a palette-triggered action of the Detail screen.
type detailAction int

const (
	detailPrev detailAction = iota
	detailNext
	detailShell
	detailPoll
	detailSetWindow
)

// detailActionMsg carries a palette command back into Update. window
// indexes historyWindows for detailSetWindow.
type detailActionMsg struct {
	action detailAction
	window int
}

// detail shows one server in depth.
type detail struct {
	d        *ui.Deps
	t        *theme.Theme
	fleet    ui.Fleet
	selected string
	// lastGood keeps the most recent online snapshot per server so an
	// offline server can still show its last known values (dimmed).
	lastGood map[string]metrics.Snapshot
	// window indexes historyWindows: the time span of the charts.
	window int
}

func newDetail(d *ui.Deps) *detail {
	return &detail{
		d: d, t: themeOf(d), fleet: initialFleet(d),
		lastGood: map[string]metrics.Snapshot{},
		window:   defaultHistoryWindow,
	}
}

func (s *detail) Tab() ui.TabID   { return ui.TabDetail }
func (s *detail) Title() string   { return "Detail" }
func (s *detail) Icon() string    { return "◈" }
func (s *detail) Init() tea.Cmd   { return nil }
func (s *detail) Capturing() bool { return false }

func (s *detail) rows() []serverRow {
	rows := buildRows(s.fleet)
	sortRows(rows, sortName)
	return rows
}

// current returns the selected row, defaulting to the first server.
func (s *detail) current() (serverRow, bool) {
	rows := s.rows()
	if len(rows) == 0 {
		return serverRow{}, false
	}
	if i := rowIndex(rows, s.selected); i >= 0 {
		return rows[i], true
	}
	return rows[0], true
}

func (s *detail) step(delta int) {
	rows := s.rows()
	if len(rows) == 0 {
		return
	}
	i := rowIndex(rows, s.selected)
	if i < 0 {
		i = 0
	}
	s.selected = rows[(i+delta+len(rows))%len(rows)].Srv.ID
}

// Update implements ui.Screen.
func (s *detail) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case ui.FleetMsg:
		s.absorb(msg.Fleet)
	case ui.ShowDetailMsg:
		s.selected = msg.ServerID
	case detailActionMsg:
		return s, s.apply(msg)
	case tea.KeyPressMsg:
		return s, s.key(msg)
	}
	return s, nil
}

// absorb stores a new fleet and refreshes the last-known-good cache.
func (s *detail) absorb(f ui.Fleet) {
	s.fleet = f
	known := make(map[string]bool, len(f.Servers))
	for _, srv := range f.Servers {
		known[srv.ID] = true
		if snap, ok := f.Snaps[srv.ID]; ok && snap.State == metrics.StateOnline {
			s.lastGood[srv.ID] = snap
		}
	}
	for id := range s.lastGood {
		if !known[id] {
			delete(s.lastGood, id)
		}
	}
}

func (s *detail) apply(msg detailActionMsg) tea.Cmd {
	switch msg.action {
	case detailPrev:
		s.step(-1)
	case detailNext:
		s.step(1)
	case detailShell:
		return s.shellCmd()
	case detailPoll:
		return pollServer(s.d, s.fleet, s.selectedID())
	case detailSetWindow:
		if msg.window >= 0 && msg.window < len(historyWindows) {
			s.window = msg.window
		}
	}
	return nil
}

func (s *detail) key(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "[", "p":
		s.step(-1)
	case "]", "n":
		s.step(1)
	case "enter":
		return s.shellCmd()
	case "r":
		return pollServer(s.d, s.fleet, s.selectedID())
	case "w":
		s.window = nextWindow(s.window)
	}
	return nil
}

func (s *detail) selectedID() string {
	row, ok := s.current()
	if !ok {
		return ""
	}
	return row.Srv.ID
}

func (s *detail) shellCmd() tea.Cmd {
	id := s.selectedID()
	if id == "" {
		return nil
	}
	return emit(ui.OpenShellMsg{ServerID: id})
}

// Hints implements ui.Screen.
func (s *detail) Hints() []ui.Hint {
	if len(s.fleet.Servers) == 0 {
		return []ui.Hint{hint("2", "add server")}
	}
	return []ui.Hint{hint("[ ]", "prev/next"), hint("w", "window"), hint("enter", "shell"), hint("r", "poll")}
}

// Commands implements ui.Screen.
func (s *detail) Commands() []ui.Command {
	act := func(a detailAction) func() tea.Cmd {
		return func() tea.Cmd { return emit(detailActionMsg{action: a}) }
	}
	cmds := []ui.Command{
		{Title: "Previous server", Hint: "[", Run: act(detailPrev)},
		{Title: "Next server", Hint: "]", Run: act(detailNext)},
		{Title: "Open shell on this server", Hint: "enter", Run: act(detailShell)},
		{Title: "Poll this server now", Hint: "r", Run: act(detailPoll)},
	}
	for i, d := range historyWindows {
		cmds = append(cmds, ui.Command{
			Title: "Chart window " + windowLabel(d),
			Hint:  "w",
			Run: func() tea.Cmd {
				return emit(detailActionMsg{action: detailSetWindow, window: i})
			},
		})
	}
	return cmds
}

// view is everything needed to render one frame of the Detail screen.
type detailView struct {
	row  serverRow
	snap metrics.Snapshot
	// metrics is true when snap holds usable values (live or stale).
	metrics bool
	// dim is true when the values are stale (server not online).
	dim  bool
	hist metrics.Series
}

// resolve picks the snapshot to display for row.
func (s *detail) resolve(row serverRow) detailView {
	v := detailView{row: row, snap: row.Snap, hist: row.Hist}
	if row.Online() {
		v.metrics = true
		return v
	}
	if good, ok := s.lastGood[row.Srv.ID]; ok {
		v.snap = good
		v.metrics = true
		v.dim = true
	}
	return v
}

// View implements ui.Screen.
func (s *detail) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	row, ok := s.current()
	if !ok {
		t := s.t
		return messagePanel(t, width, height, "Detail", "", []string{
			fg(t.P.Accent, "◈"),
			"",
			fg(t.P.Text, bold("No server to show")),
			fg(t.P.Muted, "Press ") + fg(t.P.Accent, bold("2")) + fg(t.P.Muted, " to add a server."),
		})
	}
	v := s.resolve(row)
	headerH := detailHeaderHeight(height)
	blocks := []string{s.header(v, width, headerH)}
	if rest := height - headerH; rest > 0 {
		blocks = append(blocks, s.body(v, width, rest))
	}
	return widgets.Fit(widgets.JoinV(blocks...), width, height)
}

// detailHeaderHeight is the height of the header for a content area h
// rows tall: a bordered panel with two lines when there is room, a
// compact borderless header otherwise.
func detailHeaderHeight(h int) int {
	switch {
	case h >= detailFullHeaderMinH:
		return 4
	case h >= detailTwoLineHeaderMinH:
		return 2
	default:
		return min(h, 1)
	}
}

// rankedField is one part of a header line. Fields with a higher rank are
// less important and dropped first when the line is too narrow.
type rankedField struct {
	text string
	rank int
}

// fitFields joins the non-empty fields with dots into a line of at most w
// cells. While the line is too wide the least important field is
// dropped; a single remaining field is truncated with "…".
func fitFields(t *theme.Theme, w int, fields []rankedField) string {
	kept := slices.DeleteFunc(slices.Clone(fields), func(f rankedField) bool { return f.text == "" })
	join := func() string {
		parts := make([]string, len(kept))
		for i, f := range kept {
			parts[i] = f.text
		}
		return joinDots(t, parts...)
	}
	for len(kept) > 1 && ansi.StringWidth(join()) > w {
		worst := 0
		for i, f := range kept {
			if f.rank >= kept[worst].rank {
				worst = i
			}
		}
		kept = slices.Delete(kept, worst, worst+1)
	}
	line := join()
	if ansi.StringWidth(line) > w {
		return widgets.FitLine(line, w)
	}
	return line
}

// identityFields describes where the server is and what it runs.
func (s *detail) identityFields(v detailView) []rankedField {
	t := s.t
	snap := v.snap
	srv := v.row.Srv
	addr := clean(srv.Host)
	if srv.Port != 0 && srv.Port != defaultSSHPort {
		addr = hostPort(addr, srv.Port)
	}
	return []rankedField{
		{fg(t.P.Text, clean(srv.User)+"@"+addr), 0},
		{fg(t.P.Muted, clean(snap.Hostname)), 3},
		{fg(t.P.Muted, clean(snap.OS)), 1},
		{fg(t.P.Faint, clean(snap.Kernel)), 4},
		{coresText(t, snap.CPUCount), 2},
	}
}

// healthFields describes how the server is doing right now.
func (s *detail) healthFields(v detailView) []rankedField {
	t := s.t
	snap := v.snap
	switch {
	case v.row.Online():
		load := loadText(snap.Load.One) + " " + loadText(snap.Load.Five) + " " + loadText(snap.Load.Fifteen)
		return []rankedField{
			{labelValue(t, "up", fg(t.P.Text, format.Duration(snap.Uptime))), 0},
			{labelValue(t, "latency", fg(t.P.Text, format.Latency(snap.Latency))), 2},
			{labelValue(t, "load", fg(t.P.Text, load)), 1},
			{fg(t.P.Accent2, glyphDown+" "+format.Bits(snap.Net.RxBps)) + " " + fg(t.P.Accent, glyphUp+" "+format.Bits(snap.Net.TxBps)), 4},
			{fg(t.P.Faint, "polled "+format.Ago(v.row.Snap.At, s.fleet.Now)), 3},
		}
	case v.row.Has && v.row.Snap.State == metrics.StateOffline:
		reason := clean(v.row.Snap.Err)
		if reason == "" {
			reason = "unreachable"
		}
		stale := ""
		if v.dim {
			stale = fg(t.P.Faint, "showing last known values")
		}
		return []rankedField{
			{fg(t.P.Bad, bold(glyphCross+" "+reason)), 0},
			{fg(t.P.Muted, "last seen "+format.Ago(v.row.Snap.LastSeen, s.fleet.Now)), 1},
			{stale, 2},
		}
	default:
		return []rankedField{{fg(t.P.Muted, "waiting for the first poll…"), 0}}
	}
}

// compactHeader renders the name, status badge and identity on the first
// line and the health on the second; with a single line the health
// follows the badge.
func (s *detail) compactHeader(v detailView, w, h int) string {
	t := s.t
	lead := " " + fg(t.P.Text, bold(v.row.Name())) + " " + widgets.Badge(t, v.row.Status.Label, v.row.Status.Tone) + " "
	room := max(w-ansi.StringWidth(lead)-1, 0)
	if h < 2 {
		return widgets.FitLine(lead+fitFields(t, room, s.healthFields(v)), w)
	}
	first := widgets.FitLine(lead+fitFields(t, room, s.identityFields(v)), w)
	second := widgets.FitLine(" "+fitFields(t, max(w-2, 0), s.healthFields(v)), w)
	return widgets.Fit(first+"\n"+second, w, h)
}

// header renders name, status and the identity/health lines: a bordered
// panel when h ≥ 4, otherwise one or two borderless lines.
func (s *detail) header(v detailView, w, h int) string {
	t := s.t
	if h < 4 {
		return s.compactHeader(v, w, h)
	}
	iw, _ := widgets.PanelInner(w, h)
	lines := []string{
		fitFields(t, iw, s.identityFields(v)),
		fitFields(t, iw, s.healthFields(v)),
	}
	return widgets.Panel(t, widgets.PanelOpts{
		Width:   w,
		Height:  h,
		Title:   v.row.Name(),
		Info:    widgets.Badge(t, v.row.Status.Label, v.row.Status.Tone),
		Focused: true,
		Body:    strings.Join(lines, "\n"),
	})
}

func coresText(t *theme.Theme, n int) string {
	if n <= 0 {
		return ""
	}
	label := " cores"
	if n == 1 {
		label = " core"
	}
	return fg(t.P.Muted, strconv.Itoa(n)+label)
}

// chartKind identifies one history chart.
type chartKind int

const (
	chartCPU chartKind = iota
	chartMem
	chartNet
)

// body lays out the vitals band (KPIs and charts) and the resources band
// (cores, disks, processes) for the area below the header.
func (s *detail) body(v detailView, w, h int) string {
	if !v.metrics {
		return s.waiting(v, w, h)
	}
	b1 := clamp(h*45/100, detailVitalsMinH, detailVitalsMaxH)
	if h-b1 < detailMinBand2H {
		b1 = h
	}
	kpiW := clamp(s.kpiWidth(v), detailKPIMinW, detailKPIMaxW)
	charts := []chartKind{chartCPU, chartMem, chartNet}
	inBand := 0
	if w >= kpiW+1+detailChartMinW {
		inBand = min(len(charts), (w-kpiW)/(detailChartMinW+1))
	}
	if inBand == 0 {
		kpiW = w
	}
	band1 := []string{s.kpis(v, kpiW, b1)}
	if inBand > 0 {
		widths := widgets.SplitWidths(w-kpiW-1, inBand, 1)
		for i, kind := range charts[:inBand] {
			band1 = append(band1, widgets.Blank(1, b1), s.chart(v, kind, widths[i], b1, chartTitle(kind)))
		}
	}
	blocks := []string{widgets.JoinH(band1...)}
	if b2 := h - b1; b2 > 0 {
		blocks = append(blocks, s.resources(v, charts[inBand:], w, b2))
	}
	return widgets.Fit(widgets.JoinV(blocks...), w, h)
}

// waiting is shown when there are no values at all for the server.
func (s *detail) waiting(v detailView, w, h int) string {
	t := s.t
	lines := []string{fg(t.P.Muted, "No metrics yet — waiting for the first successful poll.")}
	if v.row.Has && v.row.Snap.State == metrics.StateOffline {
		reason := clean(v.row.Snap.Err)
		if reason == "" {
			reason = "unreachable"
		}
		lines = []string{
			fg(t.P.Bad, bold(glyphCross+" "+reason)),
			"",
			fg(t.P.Muted, "Check that the host is reachable and that the manager key"),
			fg(t.P.Muted, "is in ~/.ssh/authorized_keys of "+clean(v.row.Srv.User)+"."),
		}
	}
	return messagePanel(t, w, h, "Status", "", lines)
}

// resources renders the lower band: cores and disks on the left, overflow
// charts and processes on the right (stacked when narrow).
func (s *detail) resources(v detailView, overflow []chartKind, w, h int) string {
	if w < detailSideMinW {
		disksH := min(len(v.snap.Disks)+2, h/2)
		if h < 2*detailMinBand2H || disksH < 3 {
			return s.processes(v, w, h)
		}
		return widgets.JoinV(s.disks(v, w, disksH), s.processes(v, w, h-disksH))
	}
	lw := clamp(w*42/100, 34, 76)
	rw := w - lw - 1
	return widgets.JoinH(s.leftColumn(v, lw, h), widgets.Blank(1, h), s.rightColumn(v, overflow, rw, h))
}

func (s *detail) leftColumn(v detailView, w, h int) string {
	if h < detailCoresMinH || len(v.snap.CPU.Cores) == 0 {
		return s.disks(v, w, h)
	}
	iw, _ := widgets.PanelInner(w, h)
	coresH := min(coreRows(len(v.snap.CPU.Cores), iw, h/2-2)+2, h/2)
	return widgets.JoinV(s.cores(v, w, coresH), s.disks(v, w, h-coresH))
}

func (s *detail) rightColumn(v detailView, overflow []chartKind, w, h int) string {
	var blocks []string
	left := h
	chartH := clamp(h/3, detailChartMinH, detailChartMaxH)
	for _, kind := range overflow {
		if left-chartH < detailChartMinH {
			break
		}
		blocks = append(blocks, s.chart(v, kind, w, chartH, chartTitle(kind)))
		left -= chartH
	}
	blocks = append(blocks, s.processes(v, w, left))
	return widgets.JoinV(blocks...)
}

// kpiWidth is the panel width that fits CPU and RAM big numbers side by
// side.
func (s *detail) kpiWidth(v detailView) int {
	cpu := widgets.BigNumberWidth(format.Percent(v.snap.CPU.Total))
	mem := widgets.BigNumberWidth(format.Percent(v.snap.Mem.UsedPct()))
	return max(cpu, mem, widgets.BigNumberWidth("100%"))*2 + 4 + 4
}

// kpis renders the CPU and RAM big numbers with labels.
func (s *detail) kpis(v detailView, w, h int) string {
	t := s.t
	set := s.fleet.Settings
	snap := v.snap
	iw, ih := widgets.PanelInner(w, h)
	cpuPct, memPct := snap.CPU.Total, snap.Mem.UsedPct()
	cpuText, memText := format.Percent(cpuPct), format.Percent(memPct)
	cpuColor := pctColor(t, set, metricCPU, cpuPct, v.dim)
	memColor := pctColor(t, set, metricMem, memPct, v.dim)
	cpuLabel := fg(t.P.Muted, bold("CPU"))
	memLabel := fg(t.P.Muted, bold("RAM"))
	value := func(v string) string { return fg(t.P.Text, v) }
	extra := []string{
		joinDots(t,
			value(format.Bytes(float64(snap.Mem.Used))+" / "+format.Bytes(float64(snap.Mem.Total))),
			labelValue(t, "swap", value(format.Percent(snap.Mem.SwapPct()))),
		),
		joinDots(t,
			labelValue(t, "usr", value(format.Percent(snap.CPU.User))),
			labelValue(t, "sys", value(format.Percent(snap.CPU.System))),
			labelValue(t, "iow", value(format.Percent(snap.CPU.IOWait))),
		),
		joinDots(t,
			value(strconv.Itoa(max(snap.CPUCount, len(snap.CPU.Cores)))+" cores"),
			labelValue(t, "load", value(loadText(snap.Load.One))),
		),
		joinDots(t,
			labelValue(t, "cache", value(format.Bytes(float64(snap.Mem.Cached)))),
			labelValue(t, "buffers", value(format.Bytes(float64(snap.Mem.Buffers)))),
		),
	}
	cpuW, memW := widgets.BigNumberWidth(cpuText), widgets.BigNumberWidth(memText)
	var lines []string
	switch {
	case ih >= 4 && cpuW+memW+3 <= iw:
		halves := widgets.SplitWidths(iw, 2, 1)
		cpuBig := strings.Split(widgets.BigNumber(cpuText, cpuColor), "\n")
		memBig := strings.Split(widgets.BigNumber(memText, memColor), "\n")
		for i := range min(len(cpuBig), len(memBig)) {
			lines = append(lines, widgets.Center(cpuBig[i], halves[0])+" "+widgets.Center(memBig[i], halves[1]))
		}
		lines = append(lines, widgets.Center(cpuLabel, halves[0])+" "+widgets.Center(memLabel, halves[1]))
		room := ih - len(lines)
		if room > len(extra) {
			lines = append(lines, "")
			room--
		}
		for _, e := range extra[:min(room, len(extra))] {
			lines = append(lines, widgets.Center(e, iw))
		}
	case ih >= 8 && max(cpuW, memW) <= iw:
		lines = append(lines, centerLines(widgets.BigNumber(cpuText, cpuColor), iw)...)
		lines = append(lines, widgets.Center(cpuLabel, iw))
		lines = append(lines, centerLines(widgets.BigNumber(memText, memColor), iw)...)
		lines = append(lines, widgets.Center(memLabel, iw))
	default:
		lines = []string{
			fg(t.P.Muted, "CPU ") + fg(cpuColor, bold(cpuText)) + "   " + fg(t.P.Muted, "RAM ") + fg(memColor, bold(memText)),
		}
		lines = append(lines, extra...)
	}
	if pad := (ih - len(lines)) / 2; pad > 0 {
		lines = append(make([]string, pad), lines...)
	}
	info := ""
	if v.dim {
		info = "stale"
	}
	return widgets.Panel(t, widgets.PanelOpts{
		Width: w, Height: h, Title: "Now", Info: info, Body: strings.Join(lines, "\n"),
	})
}

// centerLines centers every line of block in w cells.
func centerLines(block string, w int) []string {
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		lines[i] = widgets.Center(l, w)
	}
	return lines
}

// seriesColor dims chart colors for stale data.
func (s *detail) seriesColor(c theme.RGB, dim bool) theme.RGB {
	if dim {
		return s.t.P.Faint
	}
	return c
}

// historyStep is the sampling interval of the history.
func (s *detail) historyStep(h metrics.Series) time.Duration {
	if h.Step > 0 {
		return h.Step
	}
	return time.Second
}

// chartTitle is the panel title of a history chart.
func chartTitle(kind chartKind) string {
	switch kind {
	case chartCPU:
		return "CPU"
	case chartMem:
		return "Memory"
	default:
		return "Network"
	}
}

// fitInfo returns the first candidate that fits next to title in the top
// border of a panel w cells wide, or "" when none does.
func fitInfo(title string, w int, candidates ...string) string {
	avail := w - 4
	titleW := ansi.StringWidth(title) + 3
	for _, c := range candidates {
		if c != "" && titleW+ansi.StringWidth(c)+2 <= avail {
			return c
		}
	}
	return ""
}

// chart renders one history chart panel over the selected time window.
// CPU and memory are area charts on a fixed 0–100% scale; network is a
// braille line chart of receive and transmit rates. Swap is reported in
// the memory panel info rather than drawn, since a stacked swap band
// would read as extra RAM.
func (s *detail) chart(v detailView, kind chartKind, w, h int, title string) string {
	t := s.t
	snap := v.snap
	iw, ih := widgets.PanelInner(w, h)
	opts := widgets.ChartOpts{Width: iw, Height: ih}
	step, window := s.historyStep(v.hist), historyWindows[s.window]
	var value string
	var res windowResult
	render := widgets.AreaChart
	switch kind {
	case chartCPU:
		value = format.Percent(snap.CPU.Total) + glyphSep + "load " + loadText(snap.Load.One)
		opts.Max, opts.YLabel = 100, format.Percent
		res = applyWindow(opts, step, window, 1, []windowSeries{
			{name: "cpu", values: v.hist.CPU, color: s.seriesColor(t.P.Accent, v.dim), reduce: bucketMax},
		})
	case chartMem:
		value = format.Percent(snap.Mem.UsedPct())
		if snap.Mem.SwapTotal > 0 {
			value += glyphSep + "swap " + format.Percent(snap.Mem.SwapPct())
		}
		opts.Max, opts.YLabel = 100, format.Percent
		res = applyWindow(opts, step, window, 1, []windowSeries{
			{name: "ram", values: v.hist.Mem, color: s.seriesColor(t.P.Teal, v.dim), reduce: bucketMean},
		})
	default:
		value = glyphDown + " " + format.Bits(snap.Net.RxBps) + "  " + glyphUp + " " + format.Bits(snap.Net.TxBps)
		opts.YLabel = format.Bits
		res = applyWindow(opts, step, window, 2, []windowSeries{
			{name: "rx", values: v.hist.RxBps, color: s.seriesColor(t.P.Accent2, v.dim), reduce: bucketMean},
			{name: "tx", values: v.hist.TxBps, color: s.seriesColor(t.P.Accent, v.dim), reduce: bucketMean},
		})
		render = widgets.LineChart
	}
	opts.Series, opts.Step = res.series, res.step
	span := windowLabel(window)
	if res.span > 0 {
		span = windowLabel(res.span)
	}
	short, _, _ := strings.Cut(value, glyphSep)
	info := fitInfo(title, w, value+glyphSep+span, short+glyphSep+span, span)
	return widgets.Panel(t, widgets.PanelOpts{Width: w, Height: h, Title: title, Info: info, Body: render(t, opts)})
}

// Core grid sizing.
const (
	coreCellMinW = 15
	coreCellGap  = 2
)

// coreColumns is how many core cells fit side by side in iw cells.
func coreColumns(iw int) int {
	return max((iw+coreCellGap)/(coreCellMinW+coreCellGap), 1)
}

// coreRows is the number of grid rows needed for n cores in iw cells,
// capped at maxRows (≥1).
func coreRows(n, iw, maxRows int) int {
	cols := coreColumns(iw)
	return clamp((n+cols-1)/cols, 1, max(maxRows, 1))
}

// cores renders a compact grid of per-core utilization bars.
func (s *detail) cores(v detailView, w, h int) string {
	t := s.t
	cores := v.snap.CPU.Cores
	iw, ih := widgets.PanelInner(w, h)
	cols := coreColumns(iw)
	if rows := (len(cores) + cols - 1) / cols; rows > ih && ih > 0 {
		cols = min((len(cores)+ih-1)/ih, max(iw/(coreCellMinW-4+coreCellGap), 1))
	}
	widths := widgets.SplitWidths(iw, cols, coreCellGap)
	labelW := len("c"+strconv.Itoa(len(cores)-1)) + 1
	var lines []string
	for start := 0; start < len(cores); start += cols {
		var sb strings.Builder
		for c := range cols {
			i := start + c
			if c > 0 {
				sb.WriteString(strings.Repeat(" ", coreCellGap))
			}
			if i >= len(cores) {
				sb.WriteString(strings.Repeat(" ", widths[c]))
				continue
			}
			pct := cores[i]
			color := pctColor(t, s.fleet.Settings, metricCPU, pct, v.dim)
			barW := max(widths[c]-labelW-5, 1)
			cell := widgets.FitLine(fg(t.P.Faint, "c"+strconv.Itoa(i)), labelW) +
				widgets.Progress(t, barW, pct/100, color) +
				widgets.AlignRight(fg(color, format.Percent(pct)), 5)
			sb.WriteString(widgets.FitLine(cell, widths[c]))
		}
		lines = append(lines, sb.String())
	}
	shown := min(len(cores), cols*ih)
	info := strconv.Itoa(len(cores)) + " cores"
	if shown < len(cores) {
		info = strconv.Itoa(shown) + " of " + info
	}
	return widgets.Panel(t, widgets.PanelOpts{Width: w, Height: h, Title: "Cores", Info: info, Body: strings.Join(lines, "\n")})
}

// disks renders one usage bar per mount.
func (s *detail) disks(v detailView, w, h int) string {
	t := s.t
	iw, _ := widgets.PanelInner(w, h)
	disks := v.snap.Disks
	mountW := clamp(iw/4, 6, 18)
	sizeW := 0
	if iw >= 36 {
		sizeW = 12
	}
	var lines []string
	for _, d := range disks {
		pct := d.UsedPct()
		color := pctColor(t, s.fleet.Settings, metricDisk, pct, v.dim)
		barW := max(iw-mountW-1-5-sizeW, 1)
		line := widgets.FitLine(fg(t.P.Text, clean(d.Mount)), mountW) + " " +
			widgets.Progress(t, barW, pct/100, color) +
			widgets.AlignRight(fg(color, format.Percent(pct)), 5)
		if sizeW > 0 {
			line += widgets.AlignRight(fg(t.P.Muted, format.Bytes(float64(d.Used))+"/"+format.Bytes(float64(d.Total))), sizeW)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = []string{fg(t.P.Faint, "no filesystems reported")}
	}
	return widgets.Panel(t, widgets.PanelOpts{
		Width: w, Height: h, Title: "Disks", Info: strconv.Itoa(len(disks)) + " mounts",
		Body: strings.Join(lines, "\n"),
	})
}

// processes renders the top processes by CPU.
func (s *detail) processes(v detailView, w, h int) string {
	t := s.t
	iw, ih := widgets.PanelInner(w, h)
	procs := slices.Clone(v.snap.Procs)
	slices.SortStableFunc(procs, func(a, b metrics.Process) int { return cmp.Compare(b.CPU, a.CPU) })
	text := t.P.Text
	muted := t.P.Muted
	if v.dim {
		text, muted = t.P.Faint, t.P.Faint
	}
	set := s.fleet.Settings
	cols := fitColumns([]prioColumn[metrics.Process]{
		{widgets.Column{Title: "PID", Width: 7, Align: widgets.AlignRightCol}, 90, func(p metrics.Process) string {
			return fg(muted, strconv.Itoa(p.PID))
		}},
		{widgets.Column{Title: "USER", Width: 9}, 40, func(p metrics.Process) string {
			return fg(muted, clean(p.User))
		}},
		{widgets.Column{Title: "COMMAND", Min: 10, Flex: 1}, 100, func(p metrics.Process) string {
			return fg(text, clean(p.Command))
		}},
		{widgets.Column{Title: "CPU%", Width: 6, Align: widgets.AlignRightCol}, 95, func(p metrics.Process) string {
			return fg(pctColor(t, set, metricCPU, p.CPU, v.dim), strconv.FormatFloat(p.CPU, 'f', 1, 64))
		}},
		{widgets.Column{Title: "MEM%", Width: 6, Align: widgets.AlignRightCol}, 80, func(p metrics.Process) string {
			return fg(pctColor(t, set, metricMem, p.MemPct, v.dim), strconv.FormatFloat(p.MemPct, 'f', 1, 64))
		}},
		{widgets.Column{Title: "RSS", Width: 7, Align: widgets.AlignRightCol}, 60, func(p metrics.Process) string {
			return fg(muted, format.Bytes(float64(p.RSS)))
		}},
	}, iw)
	columns, cells := tableData(cols, procs)
	return widgets.Panel(t, widgets.PanelOpts{
		Width: w, Height: h, Title: "Processes", Info: "top " + strconv.Itoa(len(procs)) + " by cpu",
		Body: widgets.Table(t, widgets.TableOpts{
			Width: iw, Height: ih, Columns: columns, Rows: cells, Selected: -1,
			Empty: "no process data",
		}),
	})
}
