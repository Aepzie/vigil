package tui

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/CraguS/vigil/internal/compat"
	"github.com/CraguS/vigil/internal/theme"
	"github.com/CraguS/vigil/internal/tui/ui"
	"github.com/CraguS/vigil/internal/widgets"
)

const (
	// minWidth and minHeight are the smallest terminal the dashboard
	// supports; below it a notice is shown instead.
	minWidth  = 80
	minHeight = 24
	// chromeRows are the rows used by header, tabs, rule and footer.
	chromeRows = 4
	// windowTitle is the terminal title of every session.
	windowTitle = "vigil"
)

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.BackgroundColor = m.th.P.Bg.Color()
	v.WindowTitle = windowTitle
	return v
}

// render composes the complete frame: exactly width×height cells after
// widgets.Fit, then rewritten for the compatibility mode.
func (m Model) render() string {
	w, h := m.width, m.height
	if w <= 0 || h <= 0 {
		return ""
	}
	var frame string
	switch {
	case m.splash:
		frame = renderSplash(m.th, m.deps.Version, m.splashFrame, w, h)
	case w < minWidth || h < minHeight:
		frame = renderTooSmall(m.th, w, h)
	default:
		frame = m.renderDashboard(w, h)
	}
	return compat.Apply(widgets.Fit(frame, w, h), compat.Resolve(m.compat, m.opts.Env))
}

// renderDashboard draws header, tabs, the active screen, footer and the
// toast, palette and help overlays.
func (m Model) renderDashboard(w, h int) string {
	contentH := h - chromeRows
	content := widgets.Blank(w, contentH)
	if s := m.activeScreen(); s != nil {
		content = widgets.Fit(s.View(w, contentH), w, contentH)
	}
	frame := widgets.JoinV(
		widgets.ClipLine(m.renderHeader(w), w),
		m.renderTabs(w),
		m.renderRule(w),
		content,
		widgets.ClipLine(m.renderFooter(w), w),
	)
	frame = overlayToasts(m.th, frame, m.toasts, w, h-2)
	switch {
	case m.palette.open:
		box := m.palette.view(m.th, w, h)
		pw, rows := m.palette.size(w, h)
		x, y := center(w, h, pw, rows+paletteChrome)
		frame = widgets.Overlay(frame, box, x, y)
	case m.help:
		box := helpView(m.th, m.helpSections(), w, h)
		bw, bh := blockSize(box)
		x, y := center(w, h, bw, bh)
		frame = widgets.Overlay(frame, box, x, y)
	}
	return frame
}

// helpSections are the global keys and the active screen's hints.
func (m Model) helpSections() []helpSection {
	sections := []helpSection{{title: "Global", hints: globalHelp}}
	if s := m.activeScreen(); s != nil {
		sections = append(sections, helpSection{title: s.Title(), hints: s.Hints()})
	}
	return sections
}

// blockSize returns the width of the first line and the line count.
func blockSize(block string) (int, int) {
	lines := strings.Split(block, "\n")
	return ansi.StringWidth(lines[0]), len(lines)
}

// renderTooSmall draws a centered notice for terminals below the
// minimum size.
func renderTooSmall(th *theme.Theme, w, h int) string {
	size := func(cw, ch int) string { return strconv.Itoa(cw) + "×" + strconv.Itoa(ch) }
	dot := " " + th.HintSep.Render("·") + " "
	lines := []string{
		widgets.Center(th.AppName.Render("◆ vigil"), w),
		"",
		widgets.Center(th.Warn.Bold(true).Render("Terminal too small")+dot+
			th.Bad.Render(size(w, h))+dot+th.Muted.Render("need ")+th.Good.Render(size(minWidth, minHeight)), w),
		widgets.Center(th.Faint.Render("enlarge the window to continue · q quits"), w),
	}
	top := max((h-len(lines))/2, 0)
	return widgets.Fit(strings.Repeat("\n", top)+strings.Join(lines, "\n"), w, h)
}

// RenderFrame synchronously renders one complete frame of size
// width×height with the given tab active, without a running program and
// without the splash screen. It is used by `vigil render` for screenshots
// and by tests.
func RenderFrame(d *ui.Deps, opts Options, width, height int, tab ui.TabID) string {
	opts.SkipSplash = true
	return renderModelFrame(New(d, opts), width, height, tab)
}

// renderModelFrame sizes m, delivers a synchronously built fleet to all
// screens, activates tab and renders the frame.
func renderModelFrame(m Model, width, height int, tab ui.TabID) string {
	m.splash = false
	m, _ = m.update(tea.WindowSizeMsg{Width: width, Height: height})
	m, _ = m.update(ui.FleetMsg{Fleet: buildFleet(m.deps, time.Now())})
	return m.activateTab(tab).render()
}
