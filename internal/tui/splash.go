package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/widgets"
)

const (
	// splashStep is the animation frame interval of the splash screen.
	splashStep = 70 * time.Millisecond
	// splashDuration is how long the splash stays up without a key.
	splashDuration = 1400 * time.Millisecond
	// splashFrames is the frame at which the splash closes itself.
	splashFrames = int(splashDuration / splashStep)
	// splashBarWidth is the maximum width of the progress bar.
	splashBarWidth = 32
	// splashTagline is shown below the logo.
	splashTagline = "fleet monitoring over ssh"
)

// splashMsg advances the splash animation to frame.
type splashMsg struct{ frame int }

// splashCmd schedules the splash frame after the current one.
func splashCmd(frame int) tea.Cmd {
	return tea.Tick(splashStep, func(time.Time) tea.Msg { return splashMsg{frame: frame} })
}

// renderSplash draws the centered logo, tagline, version and progress
// bar for animation frame. It falls back to a compact text logo when
// the block logo does not fit.
func renderSplash(th *theme.Theme, version string, frame, w, h int) string {
	logoW, logoH := widgets.LogoSize()
	var logo []string
	if logoW+4 <= w && logoH+6 <= h {
		logo = strings.Split(widgets.Logo(th), "\n")
	} else {
		const text = "V I G I L"
		compact := th.AppName.Render("◆ ") + theme.Bold + widgets.Gradient(text, th.P.Accent, th.P.Accent2) + theme.Reset
		logo, logoW = []string{compact}, ansi.StringWidth(compact)
	}
	lines := make([]string, 0, len(logo)+5)
	pad := strings.Repeat(" ", max((w-logoW)/2, 0))
	for _, l := range logo {
		lines = append(lines, pad+l)
	}
	lines = append(lines, "", widgets.Center(th.Muted.Render(splashTagline), w))
	if version != "" {
		lines = append(lines, widgets.Center(th.Version.Render(version), w))
	}
	lines = append(lines, "", widgets.Center(splashBar(th, frame, min(splashBarWidth, max(w-4, 0))), w))
	top := max((h-len(lines))/2, 0)
	return widgets.Fit(strings.Repeat("\n", top)+strings.Join(lines, "\n"), w, h)
}

// splashBar renders a progress bar width cells wide, filled in an
// Accent to Accent2 gradient according to frame/splashFrames.
func splashBar(th *theme.Theme, frame, width int) string {
	if width <= 0 {
		return ""
	}
	filled := min(max(frame*width/splashFrames, 0), width)
	var b strings.Builder
	for i := range filled {
		c := th.P.Accent.Lerp(th.P.Accent2, float64(i)/float64(max(width-1, 1)))
		b.WriteString(c.FG() + "━")
	}
	if filled > 0 {
		b.WriteString(theme.Reset)
	}
	if rest := width - filled; rest > 0 {
		b.WriteString(th.PanelBorder.Render(strings.Repeat("─", rest)))
	}
	return b.String()
}
