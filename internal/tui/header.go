package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/CraguS/vigil/internal/format"
	"github.com/CraguS/vigil/internal/sanitize"
	"github.com/CraguS/vigil/internal/widgets"
)

// renderHeader draws row 1: logo, version, uptime and user on the left;
// fleet counts and the status badge on the right. Lower-priority parts
// are dropped when the width runs out.
func (m Model) renderHeader(w int) string {
	th := m.th
	dot := " " + th.HintSep.Render("·") + " "
	left := []string{th.AppName.Render("◆ vigil")}
	if v := sanitize.Line(m.deps.Version); v != "" {
		left = append(left, " "+th.Version.Render(v))
	}
	if !m.deps.StartedAt.IsZero() {
		up := m.now.Sub(m.deps.StartedAt).Truncate(time.Second)
		left = append(left, dot+th.Muted.Render("up "+format.Duration(max(up, 0))))
	}
	if u := sanitize.Line(m.deps.User); u != "" {
		left = append(left, dot+th.Muted.Render(u+"@vigil"))
	}

	tone := alertTone(m.fleet.Active)
	badge := th.BadgeStyle(tone).Render(statusLabel(tone))
	counts := m.renderCounts()

	for {
		l := " " + strings.Join(left, "")
		r := badge + " "
		if counts != "" {
			r = counts + "   " + r
		}
		if ansi.StringWidth(l)+ansi.StringWidth(r)+2 <= w {
			return widgets.SpreadLine(l, r, w)
		}
		switch {
		case len(left) > 1:
			left = left[:len(left)-1]
		case counts != "":
			counts = ""
		default:
			return widgets.SpreadLine(l, r, w)
		}
	}
}

// renderCounts draws "● 8 online · ● 1 offline" with colored dots, plus
// a pending count while servers have not reported yet.
func (m Model) renderCounts() string {
	th := m.th
	if len(m.fleet.Servers) == 0 {
		return th.Faint.Render("no servers")
	}
	c := countFleet(m.fleet)
	sep := " " + th.HintSep.Render("·") + " "
	offDot := th.Faint
	if c.offline > 0 {
		offDot = th.Bad
	}
	parts := []string{
		th.Good.Render("●") + " " + th.Muted.Render(strconv.Itoa(c.online)+" online"),
		offDot.Render("●") + " " + th.Muted.Render(strconv.Itoa(c.offline)+" offline"),
	}
	if c.pending > 0 {
		parts = append(parts, th.Warn.Render("●")+" "+th.Muted.Render(strconv.Itoa(c.pending)+" pending"))
	}
	return strings.Join(parts, sep)
}
