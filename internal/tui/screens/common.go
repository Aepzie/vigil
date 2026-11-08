package screens

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/format"
	"github.com/Aepzie/vigil/internal/metrics"
	"github.com/Aepzie/vigil/internal/sanitize"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
	"github.com/Aepzie/vigil/internal/widgets"
)

// SGR fragments that only undo what fg and bold set. Using them instead of
// a full reset keeps an enclosing background (e.g. the selected table row)
// intact.
const (
	fgReset = "\x1b[39m"
	boldOn  = "\x1b[1m"
	boldOff = "\x1b[22m"
)

// Glyphs shared by several screens; all are present in common terminal
// fonts.
const (
	glyphDot   = "●"
	glyphUp    = "↑"
	glyphDown  = "↓"
	glyphCheck = "✓"
	glyphCross = "✗"
	glyphWarn  = "▲"
	glyphSep   = " · "
	glyphNone  = "-"
)

// fg colors s with c, restoring only the default foreground afterwards.
func fg(c theme.RGB, s string) string {
	if s == "" {
		return ""
	}
	return c.FG() + s + fgReset
}

// bold renders s bold, restoring only the intensity afterwards.
func bold(s string) string {
	if s == "" {
		return ""
	}
	return boldOn + s + boldOff
}

// clean sanitizes untrusted text for single-line display.
func clean(s string) string { return sanitize.Line(s) }

// themeOf returns the session theme, falling back to the default theme so
// screens stay usable with partially filled dependencies (tests, tools).
func themeOf(d *ui.Deps) *theme.Theme {
	if d != nil && d.Theme != nil {
		return d.Theme
	}
	return theme.Default()
}

// initialFleet seeds a screen before the first FleetMsg arrives.
func initialFleet(d *ui.Deps) ui.Fleet {
	f := ui.Fleet{Now: time.Now(), Settings: store.DefaultSettings()}
	if d != nil && d.Store != nil {
		f.Settings = d.Store.Settings()
	}
	return f
}

// effectiveSettings fills unset thresholds with defaults so a zero
// Settings value never paints every metric as critical.
func effectiveSettings(s store.Settings) store.Settings {
	def := store.DefaultSettings()
	fill := func(v *float64, d float64) {
		if *v <= 0 {
			*v = d
		}
	}
	fill(&s.CPUWarn, def.CPUWarn)
	fill(&s.CPUCrit, def.CPUCrit)
	fill(&s.MemWarn, def.MemWarn)
	fill(&s.MemCrit, def.MemCrit)
	fill(&s.DiskWarn, def.DiskWarn)
	fill(&s.DiskCrit, def.DiskCrit)
	return s
}

// status is the summarized health of one server.
type status struct {
	Label string
	Tone  theme.Tone
	// Rank orders statuses by urgency: lower is more urgent.
	Rank int
}

// serverStatus derives the health of a server from its latest snapshot
// and the utilization thresholds. ok reports whether a snapshot exists.
func serverStatus(snap metrics.Snapshot, ok bool, set store.Settings) status {
	if !ok {
		return status{Label: "unknown", Tone: theme.ToneNeutral, Rank: 5}
	}
	switch snap.State {
	case metrics.StateOffline:
		return status{Label: "offline", Tone: theme.ToneBad, Rank: 0}
	case metrics.StateConnecting:
		return status{Label: "connecting", Tone: theme.ToneInfo, Rank: 3}
	case metrics.StateOnline:
	default:
		return status{Label: "unknown", Tone: theme.ToneNeutral, Rank: 4}
	}
	set = effectiveSettings(set)
	disk, _ := snap.MaxDiskPct()
	worst := max(
		theme.PercentTone(snap.CPU.Total, set.CPUWarn, set.CPUCrit),
		theme.PercentTone(snap.Mem.UsedPct(), set.MemWarn, set.MemCrit),
		theme.PercentTone(disk, set.DiskWarn, set.DiskCrit),
	)
	switch worst {
	case theme.ToneBad:
		return status{Label: "crit", Tone: theme.ToneBad, Rank: 1}
	case theme.ToneWarn:
		return status{Label: "warn", Tone: theme.ToneWarn, Rank: 2}
	default:
		return status{Label: "online", Tone: theme.ToneGood, Rank: 6}
	}
}

// metricKind selects which thresholds apply to a utilization value.
type metricKind int

const (
	metricCPU metricKind = iota
	metricMem
	metricDisk
)

// thresholds returns the warn and crit percentages for a metric.
func thresholds(set store.Settings, kind metricKind) (warn, crit float64) {
	set = effectiveSettings(set)
	switch kind {
	case metricMem:
		return set.MemWarn, set.MemCrit
	case metricDisk:
		return set.DiskWarn, set.DiskCrit
	default:
		return set.CPUWarn, set.CPUCrit
	}
}

// pctColor is the threshold color for a utilization value, or Faint when
// dim is set (stale values of an offline server).
func pctColor(t *theme.Theme, set store.Settings, kind metricKind, pct float64, dim bool) theme.RGB {
	if dim {
		return t.P.Faint
	}
	warn, crit := thresholds(set, kind)
	return t.ToneColor(theme.PercentTone(pct, warn, crit))
}

// pctText renders a percentage colored by its thresholds.
func pctText(t *theme.Theme, set store.Settings, kind metricKind, pct float64, dim bool) string {
	return fg(pctColor(t, set, kind, pct, dim), format.Percent(pct))
}

// serverRow joins a server with its latest data for rendering.
type serverRow struct {
	Srv    store.Server
	Snap   metrics.Snapshot
	Has    bool
	Hist   metrics.Series
	Status status
}

// Online reports whether the row has live metrics.
func (r serverRow) Online() bool { return r.Has && r.Snap.State == metrics.StateOnline }

// Name is the sanitized display name.
func (r serverRow) Name() string { return clean(r.Srv.Name) }

// buildRows joins every server of the fleet with its snapshot and history,
// in store order.
func buildRows(f ui.Fleet) []serverRow {
	rows := make([]serverRow, 0, len(f.Servers))
	for _, srv := range f.Servers {
		snap, ok := f.Snaps[srv.ID]
		rows = append(rows, serverRow{
			Srv:    srv,
			Snap:   snap,
			Has:    ok,
			Hist:   f.History[srv.ID],
			Status: serverStatus(snap, ok, f.Settings),
		})
	}
	return rows
}

// rowIndex returns the index of the row with id, or -1.
func rowIndex(rows []serverRow, id string) int {
	return slices.IndexFunc(rows, func(r serverRow) bool { return r.Srv.ID == id })
}

// stepIndex moves cur by delta within [0, n) without wrapping. A cur of
// -1 (nothing selected) starts from the first element.
func stepIndex(cur, delta, n int) int {
	if n <= 0 {
		return -1
	}
	if cur < 0 {
		return 0
	}
	return clamp(cur+delta, 0, n-1)
}

// clamp limits v to [lo, hi].
func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}

// tail returns at most the last n values.
func tail(values []float64, n int) []float64 {
	if n <= 0 {
		return nil
	}
	if len(values) > n {
		return values[len(values)-n:]
	}
	return values
}

// sumSeries adds two series element-wise, aligned at their newest value.
func sumSeries(a, b []float64) []float64 {
	n := max(len(a), len(b))
	out := make([]float64, n)
	for i := range n {
		if j := len(a) - n + i; j >= 0 {
			out[i] += a[j]
		}
		if j := len(b) - n + i; j >= 0 {
			out[i] += b[j]
		}
	}
	return out
}

// meanSeries averages series element-wise, aligned at their newest value.
// Each point is the mean of the series that have a non-NaN value there,
// so a series with a shorter history does not drag older points down.
// Points without any value are NaN.
func meanSeries(all ...[]float64) []float64 {
	n := 0
	for _, s := range all {
		n = max(n, len(s))
	}
	out := make([]float64, n)
	for i := range n {
		sum, count := 0.0, 0
		for _, s := range all {
			j := len(s) - n + i
			if j < 0 || math.IsNaN(s[j]) {
				continue
			}
			sum += s[j]
			count++
		}
		out[i] = math.NaN()
		if count > 0 {
			out[i] = sum / float64(count)
		}
	}
	return out
}

// resample shrinks values to at most n points by averaging consecutive
// buckets of nearly equal size, ignoring NaN; a bucket of only NaN stays
// NaN. Values that already fit are returned unchanged.
func resample(values []float64, n int) []float64 {
	if n <= 0 {
		return nil
	}
	if len(values) <= n {
		return values
	}
	out := make([]float64, n)
	for c := range out {
		lo, hi := c*len(values)/n, (c+1)*len(values)/n
		sum, count := 0.0, 0
		for _, v := range values[lo:hi] {
			if !math.IsNaN(v) {
				sum += v
				count++
			}
		}
		out[c] = math.NaN()
		if count > 0 {
			out[c] = sum / float64(count)
		}
	}
	return out
}

// loadText formats a load average with two decimals.
func loadText(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// emit wraps a message in a command.
func emit(msg tea.Msg) tea.Cmd {
	return func() tea.Msg { return msg }
}

// hint builds a footer hint.
func hint(key, desc string) ui.Hint { return ui.Hint{Key: key, Desc: desc} }

// centerBlock places a block of size bw×bh at the center of a blank w×h
// area.
func centerBlock(block string, bw, bh, w, h int) string {
	return overlayCentered(widgets.Blank(w, h), block, bw, bh, w, h)
}

// overlayCentered draws a block of size bw×bh centered on bg (w×h).
func overlayCentered(bg, block string, bw, bh, w, h int) string {
	x := max((w-bw)/2, 0)
	y := max((h-bh)/2, 0)
	return widgets.Fit(widgets.Overlay(bg, block, x, y), w, h)
}

// messagePanel renders a centered, friendly panel (empty states, all
// clear) inside a w×h area. Lines are centered inside the panel.
func messagePanel(t *theme.Theme, w, h int, title, info string, lines []string) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	pw := min(w, max(48, maxWidth(lines)+8))
	ph := min(h, len(lines)+4)
	iw, _ := widgets.PanelInner(pw, ph)
	body := make([]string, 0, len(lines)+1)
	body = append(body, "")
	for _, l := range lines {
		body = append(body, widgets.Center(l, iw))
	}
	panel := widgets.Panel(t, widgets.PanelOpts{
		Width: pw, Height: ph, Title: title, Info: info,
		Body: strings.Join(body, "\n"),
	})
	return centerBlock(panel, pw, ph, w, h)
}

// maxWidth is the widest line in cells.
func maxWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		w = max(w, ansi.StringWidth(l))
	}
	return w
}

// prioColumn is a table column that can be dropped when space is short.
// Columns with a lower prio are dropped first.
type prioColumn[R any] struct {
	col  widgets.Column
	prio int
	cell func(R) string
}

// fitColumns drops the lowest-priority columns until the minimum widths
// (plus two-cell separators) fit into width. Order is preserved.
func fitColumns[R any](cols []prioColumn[R], width int) []prioColumn[R] {
	kept := slices.Clone(cols)
	need := func() int {
		total := 0
		for _, c := range kept {
			total += max(c.col.Width, c.col.Min, 1)
		}
		return total + 2*max(len(kept)-1, 0)
	}
	for len(kept) > 1 && need() > width {
		lowest := 0
		for i, c := range kept {
			if c.prio < kept[lowest].prio {
				lowest = i
			}
		}
		kept = slices.Delete(kept, lowest, lowest+1)
	}
	return kept
}

// tableData converts prioritized columns and rows into widgets.Table input.
func tableData[R any](cols []prioColumn[R], rows []R) ([]widgets.Column, [][]string) {
	out := make([]widgets.Column, len(cols))
	for i, c := range cols {
		out[i] = c.col
	}
	cells := make([][]string, len(rows))
	for r, row := range rows {
		line := make([]string, len(cols))
		for i, c := range cols {
			line[i] = c.cell(row)
		}
		cells[r] = line
	}
	return out, cells
}

// sortKey is an ordering of server rows.
type sortKey int

const (
	sortName sortKey = iota
	sortCPU
	sortMem
	sortStatus
	sortKeyCount
)

// String returns the label shown in panel infos and hints.
func (k sortKey) String() string {
	switch k {
	case sortCPU:
		return "cpu"
	case sortMem:
		return "mem"
	case sortStatus:
		return "status"
	default:
		return "name"
	}
}

// sortRows orders rows in place by key; ties fall back to the name.
func sortRows(rows []serverRow, key sortKey) {
	byName := func(a, b serverRow) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Srv.Name), strings.ToLower(b.Srv.Name)),
			cmp.Compare(a.Srv.ID, b.Srv.ID),
		)
	}
	slices.SortStableFunc(rows, func(a, b serverRow) int {
		switch key {
		case sortCPU:
			return cmp.Or(cmp.Compare(metricOrNeg(b, b.Snap.CPU.Total), metricOrNeg(a, a.Snap.CPU.Total)), byName(a, b))
		case sortMem:
			return cmp.Or(cmp.Compare(metricOrNeg(b, b.Snap.Mem.UsedPct()), metricOrNeg(a, a.Snap.Mem.UsedPct())), byName(a, b))
		case sortStatus:
			return cmp.Or(cmp.Compare(a.Status.Rank, b.Status.Rank), byName(a, b))
		default:
			return byName(a, b)
		}
	})
}

// metricOrNeg returns v for online rows and -1 otherwise, so servers
// without live data sort after all live ones in descending sorts.
func metricOrNeg(r serverRow, v float64) float64 {
	if !r.Online() {
		return -1
	}
	return v
}

// matchesFilter reports whether a server matches a case-insensitive
// substring query on name, host or any tag.
func matchesFilter(srv store.Server, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	fields := append([]string{srv.Name, srv.Host}, srv.Tags...)
	return slices.ContainsFunc(fields, func(f string) bool {
		return strings.Contains(strings.ToLower(f), q)
	})
}

// lastSeenText describes when an offline server last answered.
func lastSeenText(snap metrics.Snapshot, now time.Time) string {
	return "seen " + format.Ago(snap.LastSeen, now)
}

// labelValue renders "label value" with a muted label.
func labelValue(t *theme.Theme, label, value string) string {
	return fg(t.P.Muted, label) + " " + value
}

// joinDots joins non-empty parts with a faint " · " separator.
func joinDots(t *theme.Theme, parts ...string) string {
	kept := slices.DeleteFunc(slices.Clone(parts), func(s string) bool { return s == "" })
	return strings.Join(kept, fg(t.P.Faint, glyphSep))
}

// splitUnit splits a formatted value such as "12.4Mb/s" into the part that
// BigNumber can draw ("12.4M") and the remaining unit text ("b/s").
func splitUnit(s string) (number, unit string) {
	const drawable = "0123456789.,-%kKMGTB/s "
	for i, r := range s {
		if !strings.ContainsRune(drawable, r) {
			return s[:i], s[i:]
		}
	}
	return s, ""
}

// wrapWords word-wraps plain text into lines of at most w cells; words
// longer than w are hard-wrapped.
func wrapWords(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	var lines []string
	cur, curW := "", 0
	for word := range strings.FieldsSeq(s) {
		ww := ansi.StringWidth(word)
		if curW > 0 && curW+1+ww <= w {
			cur += " " + word
			curW += 1 + ww
			continue
		}
		if curW > 0 {
			lines = append(lines, cur)
		}
		parts := wrapCells(word, w)
		lines = append(lines, parts[:len(parts)-1]...)
		cur = parts[len(parts)-1]
		curW = ansi.StringWidth(cur)
	}
	if cur != "" || len(lines) == 0 {
		lines = append(lines, cur)
	}
	return lines
}

// paint colors every line with c.
func paint(c theme.RGB, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = fg(c, l)
	}
	return out
}

// hostPort renders host and port, bracketing IPv6 literals.
func hostPort(host string, port int) string {
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return host + ":" + strconv.Itoa(port)
}

// wrapCells hard-wraps plain text into lines of at most w cells.
func wrapCells(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	var lines []string
	var cur strings.Builder
	curW := 0
	for _, r := range s {
		rw := ansi.StringWidth(string(r))
		if curW+rw > w && curW > 0 {
			lines = append(lines, cur.String())
			cur.Reset()
			curW = 0
		}
		cur.WriteRune(r)
		curW += rw
	}
	if cur.Len() > 0 || len(lines) == 0 {
		lines = append(lines, cur.String())
	}
	return lines
}
