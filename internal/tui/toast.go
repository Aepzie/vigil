package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/CraguS/vigil/internal/sanitize"
	"github.com/CraguS/vigil/internal/theme"
	"github.com/CraguS/vigil/internal/widgets"
)

const (
	// maxToasts is how many toasts are stacked at once; older ones are
	// dropped first.
	maxToasts = 4
	// toastTTL is how long a toast stays visible.
	toastTTL = 4 * time.Second
	// toastMinWidth and toastMaxWidth bound the width of one toast.
	toastMinWidth = 24
	toastMaxWidth = 56
	// toastChrome is the cells used around the text: "▌ ✓ " and a
	// trailing space.
	toastChrome = 5
)

// toast is one short notification.
type toast struct {
	id   int
	tone theme.Tone
	text string
}

// toastExpireMsg removes the toast with the given id.
type toastExpireMsg struct{ id int }

// toastStack is an immutable list of visible toasts, oldest first.
type toastStack struct {
	items  []toast
	nextID int
}

// push adds a toast (sanitizing its text) and returns the command that
// expires it after toastTTL.
func (s toastStack) push(tone theme.Tone, text string) (toastStack, tea.Cmd) {
	s.nextID++
	t := toast{id: s.nextID, tone: tone, text: sanitize.Line(text)}
	keep := s.items
	if len(keep) >= maxToasts {
		keep = keep[len(keep)-maxToasts+1:]
	}
	s.items = append(append(make([]toast, 0, len(keep)+1), keep...), t)
	id := t.id
	return s, tea.Tick(toastTTL, func(time.Time) tea.Msg { return toastExpireMsg{id: id} })
}

// expire removes the toast with id, if still present.
func (s toastStack) expire(id int) toastStack {
	out := make([]toast, 0, len(s.items))
	for _, t := range s.items {
		if t.id != id {
			out = append(out, t)
		}
	}
	s.items = out
	return s
}

// toastIcon is the font-safe glyph shown for a tone.
func toastIcon(tone theme.Tone) string {
	switch tone {
	case theme.ToneGood:
		return "✓"
	case theme.ToneWarn:
		return "!"
	case theme.ToneBad:
		return "✗"
	case theme.ToneInfo:
		return "›"
	default:
		return "·"
	}
}

// renderToast draws one toast line on the Surface background, at most
// maxWidth cells wide; long text is truncated with "…".
func renderToast(th *theme.Theme, t toast, maxWidth int) string {
	limit := min(toastMaxWidth, maxWidth)
	if limit <= 0 {
		return ""
	}
	w := min(max(ansi.StringWidth(t.text)+toastChrome, toastMinWidth), limit)
	tone := th.ToneStyle(t.tone)
	head := tone.Render("▌") + " " + tone.Bold(true).Render(toastIcon(t.tone)) + " "
	body := widgets.FitLine(t.text, max(w-toastChrome, 0))
	return paint(head+th.Text.Render(body), w, th.P.Surface)
}

// overlayToasts stacks the toasts bottom-right with the newest one on
// row bottom, one cell from the right edge of a frame width cells wide.
func overlayToasts(th *theme.Theme, frame string, s toastStack, width, bottom int) string {
	for i, t := range s.items {
		y := bottom - (len(s.items) - 1 - i)
		if y < 0 {
			continue
		}
		line := renderToast(th, t, width-2)
		x := max(width-ansi.StringWidth(line)-1, 0)
		frame = widgets.Overlay(frame, line, x, y)
	}
	return frame
}
