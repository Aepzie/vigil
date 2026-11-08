package widgets

import (
	"strings"

	"github.com/CraguS/vigil/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// PanelOpts configures Panel.
type PanelOpts struct {
	Width   int
	Height  int
	Title   string
	Info    string
	Focused bool
	// Body is fitted into the inner area (Width-4)×(Height-2): one column
	// of padding inside each vertical border.
	Body string
}

// Panel draws a rounded box (╭╮╰╯) with the title embedded in the top
// border on the left and the info text on the right. The border is
// violet when focused. Output is exactly Width×Height. Panels narrower
// than two cells render blank; the title is truncated with "…" and the
// info text dropped when the top border is too short for both.
func Panel(t *theme.Theme, o PanelOpts) string {
	w, h := max0(o.Width), max0(o.Height)
	if w < 2 || h == 0 {
		return Blank(w, h)
	}
	border, title := t.PanelBorder, t.PanelTitle
	if o.Focused {
		border, title = t.PanelBorderFocus, t.PanelTitleFocus
	}
	lines := make([]string, 0, h)
	lines = append(lines, panelTop(o, w, border.Render, title.Render, t.PanelInfo.Render))
	if h == 1 {
		return lines[0]
	}
	bar := border.Render("│")
	innerW, innerH := PanelInner(w, h)
	bodyLines := strings.Split(o.Body, "\n")
	for i := range innerH {
		line := ""
		if i < len(bodyLines) {
			line = bodyLines[i]
		}
		lines = append(lines, bar+panelBodyLine(line, w-2, innerW)+bar)
	}
	lines = append(lines, border.Render("╰"+strings.Repeat("─", w-2)+"╯"))
	return strings.Join(lines, "\n")
}

// panelBodyLine fits line into the space between the borders, keeping one
// column of padding on each side when there is room for it.
func panelBodyLine(line string, between, innerW int) string {
	if innerW <= 0 {
		return strings.Repeat(" ", between)
	}
	body := ClipLine(line, innerW)
	if strings.Contains(body, "\x1b") {
		body += theme.Reset
	}
	return " " + body + " "
}

// panelTop renders "╭─ Title ───── info ─╮" into exactly w cells.
func panelTop(o PanelOpts, w int, border, title, info func(...string) string) string {
	inner := w - 2
	if inner < 2 {
		return border("╭" + strings.Repeat("─", inner) + "╮")
	}
	avail := inner - 2
	titleText := singleLine(ansi.Strip(o.Title))
	infoText := singleLine(ansi.Strip(o.Info))
	var titlePart, infoPart string
	titleW, infoW := 0, 0
	if titleText != "" && avail >= 3 {
		titlePart = ansi.Truncate(titleText, avail-2, "…")
		titleW = ansi.StringWidth(titlePart) + 2
	}
	if infoText != "" {
		gap := 1
		if titleW == 0 {
			gap = 0
		}
		if need := ansi.StringWidth(infoText) + 2; titleW+gap+need <= avail {
			infoPart = infoText
			infoW = need
		}
	}
	fill := inner - 2 - titleW - infoW
	var sb strings.Builder
	sb.WriteString(border("╭─"))
	if titlePart != "" {
		sb.WriteString(" " + title(titlePart) + " ")
	}
	sb.WriteString(border(strings.Repeat("─", max0(fill))))
	if infoPart != "" {
		sb.WriteString(" " + info(infoPart) + " ")
	}
	sb.WriteString(border("─╮"))
	return sb.String()
}

// PanelInner returns the body size available inside a panel of w×h.
func PanelInner(w, h int) (int, int) {
	return max0(w - 4), max0(h - 2)
}

// Badge renders text as a bold black-on-color pill for the tone, with one
// cell of padding on each side.
func Badge(t *theme.Theme, text string, tone theme.Tone) string {
	return t.BadgeStyle(tone).Render(singleLine(ansi.Strip(text)))
}
