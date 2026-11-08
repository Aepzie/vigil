package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/widgets"
)

// sgrReset is the reset sequence lipgloss emits after styled runs.
const sgrReset = "\x1b[m"

// paint fits s to exactly w cells and gives every cell the background
// bg, re-applying it after each reset emitted by nested styles.
func paint(s string, w int, bg theme.RGB) string {
	if w <= 0 {
		return ""
	}
	set := bg.BG()
	line := widgets.FitLine(s, w)
	line = strings.ReplaceAll(line, theme.Reset, sgrReset)
	line = strings.ReplaceAll(line, sgrReset, sgrReset+set)
	return set + line + sgrReset
}

// modal draws a rounded box in the focus color around body lines that
// are already exactly width-2 cells wide. title sits in the top border
// on the left and info on the right.
func modal(th *theme.Theme, title, info string, width int, body []string) string {
	border := th.PanelBorderFocus
	inner := max(width-2, 0)
	lines := make([]string, 0, len(body)+2)
	lines = append(lines, modalTop(th, title, info, width))
	edge := border.Render("│")
	for _, l := range body {
		lines = append(lines, edge+l+edge)
	}
	lines = append(lines, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(lines, "\n")
}

// modalTop renders "╭─ Title ───── info ─╮", dropping info and then
// truncating the title when the width is too small.
func modalTop(th *theme.Theme, title, info string, width int) string {
	border := th.PanelBorderFocus
	const corners = 4
	room := width - corners
	padded := func(s string) int {
		if s == "" {
			return 0
		}
		return ansi.StringWidth(s) + 2
	}
	titleW, infoW := padded(title), padded(info)
	if titleW+infoW+1 > room {
		info, infoW = "", 0
	}
	if titleW > room {
		title = ansi.Truncate(title, max(room-2, 0), "…")
		titleW = padded(title)
	}
	fill := max(room-titleW-infoW, 0)
	var b strings.Builder
	b.WriteString(border.Render("╭─"))
	if title != "" {
		b.WriteString(" " + th.PanelTitleFocus.Render(title) + " ")
	}
	b.WriteString(border.Render(strings.Repeat("─", fill)))
	if info != "" {
		b.WriteString(" " + th.PanelInfo.Render(info) + " ")
	}
	b.WriteString(border.Render("─╮"))
	return widgets.ClipLine(b.String(), width)
}

// center returns the top-left position that centers a block of w×h in
// a frame of fw×fh.
func center(fw, fh, w, h int) (int, int) {
	return max((fw-w)/2, 0), max((fh-h)/2, 0)
}
