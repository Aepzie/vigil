// Package theme holds every color and lipgloss style used by vigil.
// Views and widgets must never hard-code color values; they reference a
// *Theme (or its Palette) instead.
package theme

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
)

// RGB is a truecolor value. Widgets that write raw SGR sequences use the
// FG/BG helpers; lipgloss styles use Color(). Bubble Tea downsamples the
// emitted sequences for 256/16 color terminals automatically.
type RGB struct{ R, G, B uint8 }

// Hex parses "#rrggbb". It panics on malformed input because it is only
// used with compile-time constants.
func Hex(s string) RGB {
	var c RGB
	if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &c.R, &c.G, &c.B); err != nil {
		panic("theme: bad hex color " + s)
	}
	return c
}

// String returns the "#rrggbb" form.
func (c RGB) String() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// Color converts to an image/color value usable by lipgloss.
func (c RGB) Color() color.Color { return color.RGBA{R: c.R, G: c.G, B: c.B, A: 0xff} }

// FG returns the SGR sequence that sets this color as foreground.
func (c RGB) FG() string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.R, c.G, c.B) }

// BG returns the SGR sequence that sets this color as background.
func (c RGB) BG() string { return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.R, c.G, c.B) }

// Lerp linearly interpolates between c (t=0) and o (t=1).
func (c RGB) Lerp(o RGB, t float64) RGB {
	if t <= 0 {
		return c
	}
	if t >= 1 {
		return o
	}
	mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t + 0.5) }
	return RGB{mix(c.R, o.R), mix(c.G, o.G), mix(c.B, o.B)}
}

// Darken mixes the color towards black by t (0..1).
func (c RGB) Darken(t float64) RGB { return c.Lerp(RGB{}, t) }

// Reset is the SGR sequence that clears all attributes.
const Reset = "\x1b[0m"

// Bold is the SGR sequence that enables bold text.
const Bold = "\x1b[1m"

// Tone is a semantic status used for badges, toasts and alert coloring.
type Tone int

const (
	ToneNeutral Tone = iota
	ToneInfo
	ToneGood
	ToneWarn
	ToneBad
)

// Palette lists the raw colors of the theme.
type Palette struct {
	Bg          RGB
	Surface     RGB
	Surface2    RGB
	Border      RGB
	BorderFocus RGB
	Text        RGB
	Muted       RGB
	Faint       RGB
	Accent      RGB
	Accent2     RGB
	Good        RGB
	Warn        RGB
	Bad         RGB
	Teal        RGB
	Blue        RGB
	Black       RGB
}

// Theme bundles the palette with prebuilt lipgloss styles.
type Theme struct {
	P Palette

	Text    lipgloss.Style
	Muted   lipgloss.Style
	Faint   lipgloss.Style
	Strong  lipgloss.Style
	Accent  lipgloss.Style
	Accent2 lipgloss.Style
	Good    lipgloss.Style
	Warn    lipgloss.Style
	Bad     lipgloss.Style

	AppName lipgloss.Style
	Version lipgloss.Style

	TabActive   lipgloss.Style
	TabInactive lipgloss.Style

	HintKey  lipgloss.Style
	HintDesc lipgloss.Style
	HintSep  lipgloss.Style

	PanelBorder      lipgloss.Style
	PanelBorderFocus lipgloss.Style
	PanelTitle       lipgloss.Style
	PanelTitleFocus  lipgloss.Style
	PanelInfo        lipgloss.Style

	TableHeader   lipgloss.Style
	TableCell     lipgloss.Style
	TableSelected lipgloss.Style

	Input        lipgloss.Style
	InputFocused lipgloss.Style
	InputLabel   lipgloss.Style
	Placeholder  lipgloss.Style
	Cursor       lipgloss.Style

	MatchHit lipgloss.Style
	Selected lipgloss.Style
}

// Default returns the dark monitoring theme.
func Default() *Theme {
	p := Palette{
		Bg:          Hex("#0a0b0d"),
		Surface:     Hex("#111316"),
		Surface2:    Hex("#181b20"),
		Border:      Hex("#262a31"),
		BorderFocus: Hex("#8b5cf6"),
		Text:        Hex("#e8eaed"),
		Muted:       Hex("#9aa1ab"),
		Faint:       Hex("#5a6069"),
		Accent:      Hex("#a78bfa"),
		Accent2:     Hex("#22d3ee"),
		Good:        Hex("#34d399"),
		Warn:        Hex("#fbbf24"),
		Bad:         Hex("#f87171"),
		Teal:        Hex("#2dd4bf"),
		Blue:        Hex("#60a5fa"),
		Black:       Hex("#0a0b0d"),
	}
	fg := func(c RGB) lipgloss.Style { return lipgloss.NewStyle().Foreground(c.Color()) }
	t := &Theme{P: p}

	t.Text = fg(p.Text)
	t.Muted = fg(p.Muted)
	t.Faint = fg(p.Faint)
	t.Strong = fg(p.Text).Bold(true)
	t.Accent = fg(p.Accent)
	t.Accent2 = fg(p.Accent2)
	t.Good = fg(p.Good)
	t.Warn = fg(p.Warn)
	t.Bad = fg(p.Bad)

	t.AppName = fg(p.Accent).Bold(true)
	t.Version = fg(p.Faint)

	t.TabActive = lipgloss.NewStyle().Foreground(p.Black.Color()).Background(p.Accent.Color()).Bold(true).Padding(0, 1)
	t.TabInactive = fg(p.Muted).Padding(0, 1)

	t.HintKey = fg(p.Accent).Bold(true)
	t.HintDesc = fg(p.Faint)
	t.HintSep = fg(p.Border)

	t.PanelBorder = fg(p.Border)
	t.PanelBorderFocus = fg(p.BorderFocus)
	t.PanelTitle = fg(p.Muted).Bold(true)
	t.PanelTitleFocus = fg(p.Accent).Bold(true)
	t.PanelInfo = fg(p.Faint)

	t.TableHeader = fg(p.Faint).Bold(true)
	t.TableCell = fg(p.Text)
	t.TableSelected = lipgloss.NewStyle().Foreground(p.Text.Color()).Background(p.Surface2.Color()).Bold(true)

	t.Input = fg(p.Text).Background(p.Surface.Color())
	t.InputFocused = fg(p.Text).Background(p.Surface2.Color())
	t.InputLabel = fg(p.Muted)
	t.Placeholder = fg(p.Faint)
	t.Cursor = lipgloss.NewStyle().Foreground(p.Black.Color()).Background(p.Accent.Color())

	t.MatchHit = fg(p.Accent).Bold(true).Underline(true)
	t.Selected = lipgloss.NewStyle().Background(p.Surface2.Color())
	return t
}

// ToneColor maps a semantic tone to its palette color.
func (t *Theme) ToneColor(tone Tone) RGB {
	switch tone {
	case ToneInfo:
		return t.P.Blue
	case ToneGood:
		return t.P.Good
	case ToneWarn:
		return t.P.Warn
	case ToneBad:
		return t.P.Bad
	default:
		return t.P.Muted
	}
}

// ToneStyle is a foreground style in the tone's color.
func (t *Theme) ToneStyle(tone Tone) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.ToneColor(tone).Color())
}

// BadgeStyle is black bold text on the tone's color.
func (t *Theme) BadgeStyle(tone Tone) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(t.P.Black.Color()).
		Background(t.ToneColor(tone).Color()).
		Bold(true).
		Padding(0, 1)
}

// SeriesColors is the ordered color cycle for multi-series charts.
func (t *Theme) SeriesColors() []RGB {
	return []RGB{t.P.Accent, t.P.Accent2, t.P.Teal, t.P.Blue, t.P.Good, t.P.Warn}
}

// PercentTone returns the tone for a utilization percentage against warn
// and critical thresholds.
func PercentTone(pct, warn, crit float64) Tone {
	switch {
	case pct >= crit:
		return ToneBad
	case pct >= warn:
		return ToneWarn
	default:
		return ToneGood
	}
}
