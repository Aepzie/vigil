package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/CraguS/vigil/internal/tui/ui"
	"github.com/CraguS/vigil/internal/widgets"
)

// renderTabs draws row 2: "1 ◉ Overview" per screen with the active tab
// as a violet pill. Inactive tabs collapse to number and icon when the
// full labels do not fit.
func (m Model) renderTabs(w int) string {
	var line string
	for _, compact := range []bool{false, true} {
		labels := make([]string, len(m.screens))
		for i, s := range m.screens {
			labels[i] = m.tabLabel(i, s, compact)
		}
		line = " " + strings.Join(labels, "")
		if ansi.StringWidth(line) <= w {
			break
		}
	}
	return widgets.ClipLine(line, w)
}

// tabLabel renders one tab. The Alerts tab carries the number of active
// alerts in the fleet status color.
func (m Model) tabLabel(i int, s ui.Screen, compact bool) string {
	th := m.th
	num := strconv.Itoa(i + 1)
	count := ""
	if s.Tab() == ui.TabAlerts {
		if n := activeCount(m.fleet.Active); n > 0 {
			count = strconv.Itoa(n)
		}
	}
	if i == m.active {
		return th.TabActive.Render(joinWords(num, s.Icon(), s.Title(), count))
	}
	label := " " + th.Faint.Render(num) + " " + th.Muted.Render(s.Icon())
	if !compact {
		label += " " + th.Muted.Render(s.Title())
	}
	if count != "" {
		label += " " + th.ToneStyle(alertTone(m.fleet.Active)).Bold(true).Render(count)
	}
	return label + " "
}

// renderRule draws the thin separator under the tabs.
func (m Model) renderRule(w int) string {
	return m.th.PanelBorder.Render(strings.Repeat("─", max(w, 0)))
}
