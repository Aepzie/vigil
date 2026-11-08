package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/CraguS/vigil/internal/theme"
	"github.com/CraguS/vigil/internal/tui/ui"
)

const (
	helpMaxWidth = 60
	helpMinWidth = 30
)

// helpSection is a titled group of key bindings in the help overlay.
type helpSection struct {
	title string
	hints []ui.Hint
}

// helpView renders the help modal for a frame of w×h.
func helpView(th *theme.Theme, sections []helpSection, w, h int) string {
	width := min(max(helpMinWidth, w*2/3), helpMaxWidth, max(w-4, 0))
	inner := max(width-2, 0)
	keyW := 0
	for _, s := range sections {
		for _, hint := range s.hints {
			keyW = max(keyW, ansi.StringWidth(hint.Key))
		}
	}
	keyW = min(keyW, inner/2)
	bg := th.P.Surface
	body := []string{paint("", inner, bg)}
	for i, s := range sections {
		if len(s.hints) == 0 {
			continue
		}
		if i > 0 {
			body = append(body, paint("", inner, bg))
		}
		body = append(body, paint("  "+th.PanelTitle.Render(s.title), inner, bg))
		for _, hint := range s.hints {
			key := hint.Key
			if gap := keyW - ansi.StringWidth(key); gap > 0 {
				key += strings.Repeat(" ", gap)
			}
			body = append(body, paint("    "+th.HintKey.Render(key)+"   "+th.Text.Render(hint.Desc), inner, bg))
		}
	}
	body = append(body, paint("", inner, bg))
	if limit := max(h-2, 0); len(body) > limit {
		body = body[:limit]
	}
	return modal(th, "Keys", "esc close", width, body)
}
