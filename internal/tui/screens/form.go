package screens

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
	"github.com/Aepzie/vigil/internal/widgets"
)

// formField is one labeled input of a form.
type formField struct {
	label string
	// help is a dim explanation shown under the field when there is no
	// error and enough room.
	help  string
	input textInput
	err   string
}

// formAction is what a key press asks the form owner to do.
type formAction int

const (
	formNone formAction = iota
	formSubmit
	formCancel
)

// form is a modal list of inputs: tab/shift+tab/up/down move between
// fields, enter advances (and submits on the last field), ctrl+s submits
// and esc cancels.
type form struct {
	title  string
	fields []formField
	focus  int
	// err is a form-wide error, e.g. from the store.
	err  string
	busy bool
}

// maxFormWidth caps the modal width so labels and inputs stay close.
const maxFormWidth = 66

// Update applies a key press and reports what the owner should do.
func (f *form) Update(msg tea.KeyPressMsg) formAction {
	n := len(f.fields)
	if n == 0 {
		return formCancel
	}
	switch msg.String() {
	case "esc":
		return formCancel
	case "ctrl+s":
		return formSubmit
	case "tab", "down", "ctrl+n":
		f.focus = (f.focus + 1) % n
	case "shift+tab", "up", "ctrl+p":
		f.focus = (f.focus - 1 + n) % n
	case "enter":
		if f.focus == n-1 {
			return formSubmit
		}
		f.focus++
	default:
		if f.fields[f.focus].input.Update(msg) {
			f.fields[f.focus].err = ""
			f.err = ""
		}
	}
	return formNone
}

// Paste inserts pasted text into the focused field.
func (f *form) Paste(s string) {
	if len(f.fields) == 0 {
		return
	}
	f.fields[f.focus].input.Insert(s)
}

// Value returns the trimmed text of field i.
func (f *form) Value(i int) string {
	return strings.TrimSpace(f.fields[i].input.Value())
}

// clearErrors removes all field and form errors.
func (f *form) clearErrors() {
	f.err = ""
	for i := range f.fields {
		f.fields[i].err = ""
	}
}

// fail records a field error and focuses that field.
func (f *form) fail(field int, msg string) {
	f.fields[field].err = msg
	f.focus = field
}

// errorCount is the number of fields with an error.
func (f *form) errorCount() int {
	n := 0
	for _, fl := range f.fields {
		if fl.err != "" {
			n++
		}
	}
	return n
}

// Size returns the modal panel size for a w×h area.
func (f *form) Size(w, h int) (int, int) {
	n := len(f.fields)
	pw := min(w, maxFormWidth)
	switch {
	case h >= 2*n+4:
		return pw, 2*n + 4
	case h >= n+4:
		return pw, n + 4
	default:
		return pw, h
	}
}

// labelWidth is the width of the label column.
func (f *form) labelWidth() int {
	w := 0
	for _, fl := range f.fields {
		w = max(w, ansi.StringWidth(fl.label))
	}
	return w + 2
}

// View renders the form as a focused panel of exactly pw×ph cells.
func (f *form) View(t *theme.Theme, pw, ph int) string {
	iw, ih := widgets.PanelInner(pw, ph)
	n := len(f.fields)
	spacious := ih >= 2*n+2
	lw := min(f.labelWidth(), max(iw/3, 1))
	inputW := max(iw-lw, 1)

	var lines []string
	for i, fl := range f.fields {
		focused := i == f.focus
		label := fl.label
		labelColor := t.P.Muted
		if focused {
			labelColor = t.P.Accent
		}
		if fl.err != "" {
			labelColor = t.P.Bad
		}
		row := widgets.FitLine(fg(labelColor, label), lw-1) + " " + fl.input.View(t, inputW, focused)
		lines = append(lines, row)
		if !spacious {
			continue
		}
		note := fg(t.P.Faint, fl.help)
		if fl.err != "" {
			note = fg(t.P.Bad, glyphCross+" "+fl.err)
		}
		lines = append(lines, strings.Repeat(" ", lw)+note)
	}

	footer := f.footer(t)
	fieldRows := len(lines)
	visible := max(ih-2, 1)
	if !spacious && fieldRows > visible {
		start := clamp(f.focus-visible/2, 0, fieldRows-visible)
		lines = lines[start : start+visible]
	}
	lines = append(lines, "", footer)

	return widgets.Panel(t, widgets.PanelOpts{
		Width:   pw,
		Height:  ph,
		Title:   f.title,
		Info:    "esc cancel",
		Focused: true,
		Body:    strings.Join(lines, "\n"),
	})
}

// footer shows the form-wide error, the focused field's error in compact
// layouts, a progress note or the key help.
func (f *form) footer(t *theme.Theme) string {
	switch {
	case f.busy:
		return fg(t.P.Muted, "saving…")
	case f.err != "":
		return fg(t.P.Bad, glyphCross+" "+f.err)
	case len(f.fields) > 0 && f.fields[f.focus].err != "":
		return fg(t.P.Bad, glyphCross+" "+f.fields[f.focus].label+": "+f.fields[f.focus].err)
	default:
		return fg(t.P.Faint, "tab next · enter on last field or ctrl+s save · esc cancel")
	}
}

// formHints are the footer hints while a form is open.
func formHints() []ui.Hint {
	return []ui.Hint{hint("tab", "next"), hint("shift+tab", "prev"), hint("ctrl+s", "save"), hint("esc", "cancel")}
}

// confirmDialog asks a yes/no question about one target.
type confirmDialog struct {
	title  string
	prompt string
	target string
}

// confirmAction is the outcome of a key press in a confirm dialog.
type confirmAction int

const (
	confirmNone confirmAction = iota
	confirmYes
	confirmNo
)

// Update maps y/n/esc to an outcome; other keys are swallowed.
func (c *confirmDialog) Update(msg tea.KeyPressMsg) confirmAction {
	switch msg.String() {
	case "y", "Y":
		return confirmYes
	case "n", "N", "esc", "q":
		return confirmNo
	default:
		return confirmNone
	}
}

// Size returns the dialog size for a w×h area.
func (c *confirmDialog) Size(w, h int) (int, int) {
	pw := min(w, max(40, ansi.StringWidth(c.prompt)+8))
	return pw, min(h, 6)
}

// View renders the dialog as a focused panel of exactly pw×ph cells.
func (c *confirmDialog) View(t *theme.Theme, pw, ph int) string {
	iw, _ := widgets.PanelInner(pw, ph)
	keys := fg(t.P.Bad, bold("y")) + fg(t.P.Faint, " delete") + fg(t.P.Faint, glyphSep) +
		fg(t.P.Accent, bold("n")) + fg(t.P.Faint, " keep")
	body := []string{
		"",
		widgets.Center(fg(t.P.Text, bold(c.prompt)), iw),
		"",
		widgets.Center(keys, iw),
	}
	return widgets.Panel(t, widgets.PanelOpts{
		Width: pw, Height: ph, Title: c.title, Focused: true,
		Body: strings.Join(body, "\n"),
	})
}
