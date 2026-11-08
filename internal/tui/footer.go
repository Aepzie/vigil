package tui

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/compat"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
	"github.com/Aepzie/vigil/internal/widgets"
)

// renderFooter draws the last row: the active screen's hints followed by
// the global ones (stopping at the first that does not fit), and on the
// right the compatibility mode when not auto plus the clock.
func (m Model) renderFooter(w int) string {
	th := m.th
	right := th.Muted.Render(m.now.Format("15:04:05")) + " "
	if m.compat != compat.Auto {
		right = th.Faint.Render("compat ") + th.Warn.Render(m.compat.String()) + "  " + right
	}
	var hints []ui.Hint
	if s := m.activeScreen(); s != nil {
		hints = append(hints, s.Hints()...)
	}
	hints = append(hints, globalHints...)
	left := " " + joinHints(th, hints, w-ansi.StringWidth(right)-2)
	return widgets.SpreadLine(left, right, w)
}

// joinHints renders "key desc · key desc" within room cells.
func joinHints(th *theme.Theme, hints []ui.Hint, room int) string {
	sep := " " + th.HintSep.Render("·") + " "
	sepW := ansi.StringWidth(sep)
	out, used := "", 0
	for _, h := range hints {
		if h.Key == "" {
			continue
		}
		hint := th.HintKey.Render(h.Key)
		if h.Desc != "" {
			hint += " " + th.HintDesc.Render(h.Desc)
		}
		need := ansi.StringWidth(hint)
		if out != "" {
			need += sepW
		}
		if used+need > room {
			break
		}
		if out != "" {
			out += sep
		}
		out += hint
		used += need
	}
	return out
}
