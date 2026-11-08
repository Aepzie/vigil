package screens

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/sanitize"
	"github.com/Aepzie/vigil/internal/theme"
)

// maxInputRunes bounds the length of any single-line input.
const maxInputRunes = 512

// textInput is a minimal single-line editor with exact-width rendering.
// Everything typed or pasted is sanitized before it is stored, so the
// value is always safe to display.
type textInput struct {
	value       []rune
	cursor      int
	placeholder string
}

// newInput creates an input with a placeholder shown while empty.
func newInput(placeholder string) textInput {
	return textInput{placeholder: placeholder}
}

// Value returns the current text.
func (in textInput) Value() string { return string(in.value) }

// SetValue replaces the text and moves the cursor to the end.
func (in *textInput) SetValue(s string) {
	in.value = []rune(sanitize.Line(s))
	if len(in.value) > maxInputRunes {
		in.value = in.value[:maxInputRunes]
	}
	in.cursor = len(in.value)
}

// Insert adds sanitized text at the cursor.
func (in *textInput) Insert(s string) {
	add := []rune(sanitize.Line(s))
	room := maxInputRunes - len(in.value)
	if room <= 0 || len(add) == 0 {
		return
	}
	if len(add) > room {
		add = add[:room]
	}
	in.value = slices.Insert(in.value, in.cursor, add...)
	in.cursor += len(add)
}

// Update applies an editing key. It reports whether the key was consumed.
func (in *textInput) Update(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "left", "ctrl+b":
		in.cursor = max(in.cursor-1, 0)
	case "right", "ctrl+f":
		in.cursor = min(in.cursor+1, len(in.value))
	case "home", "ctrl+a":
		in.cursor = 0
	case "end", "ctrl+e":
		in.cursor = len(in.value)
	case "backspace", "ctrl+h":
		if in.cursor > 0 {
			in.value = slices.Delete(in.value, in.cursor-1, in.cursor)
			in.cursor--
		}
	case "delete", "ctrl+d":
		if in.cursor < len(in.value) {
			in.value = slices.Delete(in.value, in.cursor, in.cursor+1)
		}
	case "ctrl+u":
		in.value = slices.Delete(in.value, 0, in.cursor)
		in.cursor = 0
	case "ctrl+k":
		in.value = in.value[:in.cursor]
	case "ctrl+w", "alt+backspace":
		in.deleteWord()
	default:
		if msg.Text == "" || msg.Mod.Contains(tea.ModCtrl) || msg.Mod.Contains(tea.ModAlt) {
			return false
		}
		in.Insert(msg.Text)
	}
	return true
}

// deleteWord removes the word before the cursor.
func (in *textInput) deleteWord() {
	start := in.cursor
	for start > 0 && in.value[start-1] == ' ' {
		start--
	}
	for start > 0 && in.value[start-1] != ' ' {
		start--
	}
	in.value = slices.Delete(in.value, start, in.cursor)
	in.cursor = start
}

// View renders the input into exactly width cells. When focused, the
// cursor cell is highlighted and the text scrolls to keep it visible.
func (in textInput) View(t *theme.Theme, width int, focused bool) string {
	if width <= 0 {
		return ""
	}
	bg := t.P.Surface
	if focused {
		bg = t.P.Surface2
	}
	var sb strings.Builder
	sb.WriteString(bg.BG())
	used := 0
	if len(in.value) == 0 {
		if focused {
			sb.WriteString(t.P.Black.FG() + t.P.Accent.BG() + " " + bg.BG())
			used++
		}
		ph := ansi.Truncate(in.placeholder, width-used, "…")
		sb.WriteString(t.P.Faint.FG() + ph)
		used += ansi.StringWidth(ph)
	} else {
		start := 0
		if focused {
			start = in.scrollStart(width)
		}
		sb.WriteString(t.P.Text.FG())
		for i := start; i <= len(in.value) && used < width; i++ {
			cell := " "
			if i < len(in.value) {
				cell = string(in.value[i])
			}
			cw := max(ansi.StringWidth(cell), 1)
			if used+cw > width {
				break
			}
			if i == len(in.value) && !focused {
				break
			}
			if focused && i == in.cursor {
				sb.WriteString(t.P.Black.FG() + t.P.Accent.BG() + cell + t.P.Text.FG() + bg.BG())
			} else {
				sb.WriteString(cell)
			}
			used += cw
		}
	}
	if used < width {
		sb.WriteString(strings.Repeat(" ", width-used))
	}
	sb.WriteString(theme.Reset)
	return sb.String()
}

// scrollStart is the first rune shown so that the cursor (plus one cell
// for it at the end of the text) fits into width cells.
func (in textInput) scrollStart(width int) int {
	need := 1
	start := in.cursor
	for start > 0 {
		cw := max(ansi.StringWidth(string(in.value[start-1])), 1)
		if need+cw > width {
			break
		}
		need += cw
		start--
	}
	return start
}
