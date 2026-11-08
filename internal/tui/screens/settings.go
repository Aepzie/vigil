package screens

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Aepzie/vigil/internal/alerts"
	"github.com/Aepzie/vigil/internal/compat"
	"github.com/Aepzie/vigil/internal/format"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
	"github.com/Aepzie/vigil/internal/widgets"
)

// Settings layout constants.
const (
	settingsSideMinW = 76
	settingsLabelW   = 16
	settingsValueW   = 9
	settingsUnitW    = 5
)

// settingField describes one editable setting.
type settingField struct {
	label string
	unit  string
	help  string
	// enum marks fields that cycle through fixed values instead of being
	// typed.
	enum bool
	get  func(store.Settings) string
	set  func(*store.Settings, string) error
}

// numberField parses a number within [lo, hi].
func parseNumber(raw string, lo, hi float64) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errors.New("not a number")
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf("must be between %s and %s", strconv.FormatFloat(lo, 'f', -1, 64), strconv.FormatFloat(hi, 'f', -1, 64))
	}
	return v, nil
}

func secondsField(label, help string, lo, hi float64, ptr func(*store.Settings) *time.Duration) settingField {
	return settingField{
		label: label, unit: "s", help: help,
		get: func(s store.Settings) string {
			return strconv.FormatFloat(ptr(&s).Seconds(), 'f', -1, 64)
		},
		set: func(s *store.Settings, raw string) error {
			v, err := parseNumber(raw, lo, hi)
			if err != nil {
				return err
			}
			*ptr(s) = time.Duration(v * float64(time.Second)).Round(time.Millisecond)
			return nil
		},
	}
}

func percentField(label, help string, ptr func(*store.Settings) *float64) settingField {
	return settingField{
		label: label, unit: "%", help: help,
		get: func(s store.Settings) string {
			return strconv.FormatFloat(*ptr(&s), 'f', -1, 64)
		},
		set: func(s *store.Settings, raw string) error {
			v, err := parseNumber(raw, 1, 100)
			if err != nil {
				return err
			}
			*ptr(s) = v
			return nil
		},
	}
}

// settingFields lists the editable settings in display order.
func settingFields() []settingField {
	return []settingField{
		secondsField("Poll interval", "time between polls of each server", 1, 3600,
			func(s *store.Settings) *time.Duration { return &s.PollInterval }),
		{
			label: "History", unit: "min", help: "how much history charts keep",
			get: func(s store.Settings) string { return strconv.Itoa(s.HistoryMinutes) },
			set: func(s *store.Settings, raw string) error {
				v, err := parseNumber(raw, 1, 1440)
				if err != nil {
					return err
				}
				if v != math.Trunc(v) {
					return errors.New("must be a whole number")
				}
				s.HistoryMinutes = int(v)
				return nil
			},
		},
		percentField("CPU warn", "yellow and warning alert above", func(s *store.Settings) *float64 { return &s.CPUWarn }),
		percentField("CPU crit", "red and critical alert above", func(s *store.Settings) *float64 { return &s.CPUCrit }),
		percentField("RAM warn", "memory warning threshold", func(s *store.Settings) *float64 { return &s.MemWarn }),
		percentField("RAM crit", "memory critical threshold", func(s *store.Settings) *float64 { return &s.MemCrit }),
		percentField("Disk warn", "fullest mount warning threshold", func(s *store.Settings) *float64 { return &s.DiskWarn }),
		percentField("Disk crit", "fullest mount critical threshold", func(s *store.Settings) *float64 { return &s.DiskCrit }),
		secondsField("Offline grace", "unreachable this long before alerting", 0, 86400,
			func(s *store.Settings) *time.Duration { return &s.OfflineGrace }),
		{
			label: "Compat mode", help: "default glyph set: auto, blocks, ascii, off", enum: true,
			get: func(s store.Settings) string { return compat.Parse(s.Compat).String() },
			set: func(s *store.Settings, raw string) error {
				s.Compat = compat.Parse(raw).String()
				return nil
			},
		},
	}
}

// validateSettings checks cross-field rules before saving.
func validateSettings(s store.Settings) error {
	pairs := []struct {
		name       string
		warn, crit float64
	}{
		{"CPU", s.CPUWarn, s.CPUCrit},
		{"RAM", s.MemWarn, s.MemCrit},
		{"Disk", s.DiskWarn, s.DiskCrit},
	}
	for _, p := range pairs {
		if p.warn >= p.crit {
			return fmt.Errorf("%s warn must be below %s crit", p.name, p.name)
		}
	}
	return nil
}

// settingsSavedMsg is the result of saving settings.
type settingsSavedMsg struct {
	set store.Settings
	err error
}

// settingsAction is a palette-triggered action of the Settings screen.
type settingsAction int

const (
	settingsSave settingsAction = iota
	settingsRevert
	settingsCycleCompat
)

// settingsActionMsg carries a palette command back into Update.
type settingsActionMsg struct{ action settingsAction }

// settingsScreen edits manager settings and shows manager information.
type settingsScreen struct {
	d      *ui.Deps
	t      *theme.Theme
	fleet  ui.Fleet
	fields []settingField
	draft  store.Settings
	cursor int
	// editing is true while the focused value is being typed.
	editing bool
	input   textInput
	// fieldErr is the parse error of the field being edited.
	fieldErr string
	// note is a status line for the panel (save errors).
	note     string
	noteTone theme.Tone
	saving   bool
}

func newSettings(d *ui.Deps) *settingsScreen {
	f := initialFleet(d)
	return &settingsScreen{d: d, t: themeOf(d), fleet: f, fields: settingFields(), draft: f.Settings}
}

func (s *settingsScreen) Tab() ui.TabID   { return ui.TabSettings }
func (s *settingsScreen) Title() string   { return "Settings" }
func (s *settingsScreen) Icon() string    { return "✱" }
func (s *settingsScreen) Init() tea.Cmd   { return nil }
func (s *settingsScreen) Capturing() bool { return s.editing }

// dirty reports unsaved changes.
func (s *settingsScreen) dirty() bool { return s.draft != s.fleet.Settings }

// Update implements ui.Screen.
func (s *settingsScreen) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case ui.FleetMsg:
		wasDirty := s.dirty()
		s.fleet = msg.Fleet
		if !wasDirty {
			s.draft = msg.Fleet.Settings
		}
	case settingsSavedMsg:
		return s, s.saved(msg)
	case settingsActionMsg:
		return s, s.apply(msg.action)
	case tea.PasteMsg:
		if s.editing {
			s.input.Insert(msg.Content)
		}
	case tea.KeyPressMsg:
		if s.editing {
			s.editKey(msg)
			return s, nil
		}
		return s, s.key(msg)
	}
	return s, nil
}

func (s *settingsScreen) apply(a settingsAction) tea.Cmd {
	if s.editing {
		return nil
	}
	if isReadOnly(s.d) && a != settingsRevert {
		return refuseReadOnly()
	}
	switch a {
	case settingsSave:
		return s.save()
	case settingsRevert:
		s.revert()
	case settingsCycleCompat:
		s.cycleCompat(1)
	}
	return nil
}

func (s *settingsScreen) key(msg tea.KeyPressMsg) tea.Cmd {
	field := s.fields[s.cursor]
	switch msg.String() {
	case "j", "down":
		s.cursor = stepIndex(s.cursor, 1, len(s.fields))
		return nil
	case "k", "up":
		s.cursor = stepIndex(s.cursor, -1, len(s.fields))
		return nil
	case "g", "home":
		s.cursor = 0
		return nil
	case "G", "end":
		s.cursor = len(s.fields) - 1
		return nil
	}
	if isReadOnly(s.d) {
		return s.refuseEditKey(msg, field)
	}
	switch msg.String() {
	case "enter", "e":
		if field.enum {
			s.cycleCompat(1)
			return nil
		}
		s.startEdit()
	case "left", "h":
		if field.enum {
			s.cycleCompat(-1)
		}
	case "right", "l", "space":
		if field.enum {
			s.cycleCompat(1)
		}
	case "ctrl+s", "S":
		return s.save()
	case "u":
		s.revert()
	}
	return nil
}

// refuseEditKey answers keys that would change settings in a read-only
// session.
func (s *settingsScreen) refuseEditKey(msg tea.KeyPressMsg, field settingField) tea.Cmd {
	switch msg.String() {
	case "enter", "e", "ctrl+s", "S":
		return refuseReadOnly()
	case "left", "h", "right", "l", "space":
		if field.enum {
			return refuseReadOnly()
		}
	}
	return nil
}

func (s *settingsScreen) startEdit() {
	s.editing = true
	s.fieldErr = ""
	s.input = newInput("")
	s.input.SetValue(s.fields[s.cursor].get(s.draft))
}

func (s *settingsScreen) editKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc":
		s.editing = false
		s.fieldErr = ""
	case "enter":
		next := s.draft
		if err := s.fields[s.cursor].set(&next, s.input.Value()); err != nil {
			s.fieldErr = err.Error()
			return
		}
		s.draft = next
		s.editing = false
		s.fieldErr = ""
		s.note = ""
	default:
		if s.input.Update(msg) {
			s.fieldErr = ""
		}
	}
}

// cycleCompat moves the compat mode forward (dir 1) or backward (dir -1).
func (s *settingsScreen) cycleCompat(dir int) {
	m := compat.Parse(s.draft.Compat)
	if dir < 0 {
		m = (m + 3) % 4
	} else {
		m = m.Next()
	}
	s.draft.Compat = m.String()
}

func (s *settingsScreen) revert() {
	s.draft = s.fleet.Settings
	s.note = ""
}

// save validates the draft and persists it off the UI goroutine.
func (s *settingsScreen) save() tea.Cmd {
	if isReadOnly(s.d) {
		return refuseReadOnly()
	}
	if s.saving {
		return nil
	}
	if err := validateSettings(s.draft); err != nil {
		s.note, s.noteTone = err.Error(), theme.ToneBad
		return nil
	}
	var st *store.Store
	var eng *alerts.Engine
	if s.d != nil {
		st, eng = s.d.Store, s.d.Alerts
	}
	set := s.draft
	s.saving = true
	s.note = ""
	return func() tea.Msg {
		if st == nil {
			return settingsSavedMsg{set: set, err: errNoStore}
		}
		if err := st.SaveSettings(set); err != nil {
			return settingsSavedMsg{set: set, err: err}
		}
		if eng != nil {
			eng.SetThresholds(alerts.Thresholds{
				CPUWarn: set.CPUWarn, CPUCrit: set.CPUCrit,
				MemWarn: set.MemWarn, MemCrit: set.MemCrit,
				DiskWarn: set.DiskWarn, DiskCrit: set.DiskCrit,
				OfflineGrace: set.OfflineGrace,
			})
		}
		return settingsSavedMsg{set: set}
	}
}

func (s *settingsScreen) saved(msg settingsSavedMsg) tea.Cmd {
	s.saving = false
	if msg.err != nil {
		text := clean(msg.err.Error())
		s.note, s.noteTone = text, theme.ToneBad
		return ui.Toast(theme.ToneBad, "Settings not saved: "+text)
	}
	s.fleet.Settings = msg.set
	s.draft = msg.set
	s.note = ""
	return tea.Batch(ui.Toast(theme.ToneGood, "Settings saved"), ui.Refresh)
}

// Hints implements ui.Screen.
func (s *settingsScreen) Hints() []ui.Hint {
	if s.editing {
		return []ui.Hint{hint("enter", "confirm"), hint("esc", "cancel")}
	}
	if isReadOnly(s.d) {
		return []ui.Hint{hint("j/k", "move")}
	}
	edit := hint("enter", "edit")
	if s.fields[s.cursor].enum {
		edit = hint("←/→", "cycle")
	}
	hints := []ui.Hint{hint("j/k", "move"), edit, hint("ctrl+s", "save")}
	if s.dirty() {
		hints = append(hints, hint("u", "revert"))
	}
	return hints
}

// Commands implements ui.Screen.
func (s *settingsScreen) Commands() []ui.Command {
	if isReadOnly(s.d) {
		return nil
	}
	act := func(a settingsAction) func() tea.Cmd {
		return func() tea.Cmd { return emit(settingsActionMsg{action: a}) }
	}
	return []ui.Command{
		{Title: "Save settings", Hint: "ctrl+s", Run: act(settingsSave)},
		{Title: "Revert unsaved settings", Hint: "u", Run: act(settingsRevert)},
		{Title: "Cycle default compat mode", Hint: "←/→", Run: act(settingsCycleCompat)},
	}
}

// View implements ui.Screen.
func (s *settingsScreen) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if width >= settingsSideMinW {
		lw := clamp(width*45/100, 38, 80)
		return widgets.JoinH(
			s.formPanel(lw, height),
			widgets.Blank(1, height),
			s.managerPanel(width-lw-1, height),
		)
	}
	formH := min(len(s.fields)+6, height)
	blocks := []string{s.formPanel(width, formH)}
	if rest := height - formH; rest > 0 {
		blocks = append(blocks, s.managerPanel(width, rest))
	}
	return widgets.Fit(widgets.JoinV(blocks...), width, height)
}

// settingGroups maps the index of the first field of a group to its
// heading.
var settingGroups = map[int]string{0: "COLLECTION", 2: "THRESHOLDS", 8: "ALERTING", 9: "DISPLAY"}

// settingsRowFixedW is the row width without the help column.
const settingsRowFixedW = 2 + settingsLabelW + 2 + settingsValueW + settingsUnitW + 1

// formPanel renders the settings rows (grouped when there is room) and a
// status line.
func (s *settingsScreen) formPanel(w, h int) string {
	t := s.t
	iw, ih := widgets.PanelInner(w, h)
	inlineHelp := iw-settingsRowFixedW >= 16
	footer := []string{"", s.statusLine()}
	if !inlineHelp {
		footer = append([]string{"", s.selectedHelp()}, footer...)
	}
	grouped := ih >= len(s.fields)+2*len(settingGroups)-1+len(footer)
	var rows []string
	selLine := 0
	for i := range s.fields {
		if title, ok := settingGroups[i]; ok && grouped {
			if i > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, fg(t.P.Faint, bold(title)))
		}
		if i == s.cursor {
			selLine = len(rows)
		}
		rows = append(rows, s.row(i, iw, inlineHelp))
	}
	visible := max(ih-len(footer), 1)
	if len(rows) > visible {
		start := clamp(selLine-visible/2, 0, len(rows)-visible)
		rows = rows[start : start+visible]
	}
	lines := append(rows, footer...)
	info := "saved"
	switch {
	case isReadOnly(s.d):
		info = "read-only"
	case s.dirty():
		info = "unsaved changes"
	}
	return widgets.Panel(t, widgets.PanelOpts{
		Width: w, Height: h, Title: "Settings", Info: info, Focused: true,
		Body: strings.Join(lines, "\n"),
	})
}

// selectedHelp is the help or error of the focused field, shown under the
// rows when there is no room for inline help.
func (s *settingsScreen) selectedHelp() string {
	if s.fieldErr != "" {
		return fg(s.t.P.Bad, glyphCross+" "+s.fieldErr)
	}
	return fg(s.t.P.Faint, s.fields[s.cursor].help)
}

// row renders one setting, iw cells wide. withHelp appends the field's
// help (or edit error) after the value.
func (s *settingsScreen) row(i, iw int, withHelp bool) string {
	t := s.t
	f := s.fields[i]
	selected := i == s.cursor
	changed := f.get(s.draft) != f.get(s.fleet.Settings)

	marker := "  "
	if selected {
		marker = fg(t.P.Accent, "› ")
	}
	labelColor := t.P.Muted
	if selected {
		labelColor = t.P.Text
	}
	label := widgets.FitLine(fg(labelColor, f.label), settingsLabelW)
	dot := " "
	if changed {
		dot = fg(t.P.Accent, glyphDot)
	}

	var value string
	switch {
	case selected && s.editing:
		value = s.input.View(t, settingsValueW, true)
	case f.enum:
		value = widgets.FitLine(fg(t.P.Accent2, "‹ "+f.get(s.draft)+" ›"), settingsValueW)
	default:
		value = widgets.AlignRight(fg(t.P.Text, bold(f.get(s.draft))), settingsValueW)
	}
	unit := widgets.FitLine(" "+fg(t.P.Faint, f.unit), settingsUnitW)

	line := marker + label + dot + " " + value + unit
	if withHelp {
		note := fg(t.P.Faint, f.help)
		if selected && s.fieldErr != "" {
			note = fg(t.P.Bad, glyphCross+" "+s.fieldErr)
		}
		line += " " + note
	}
	if !selected {
		return widgets.FitLine(line, iw)
	}
	return t.P.Surface2.BG() + widgets.FitLine(line, iw) + theme.Reset
}

// statusLine summarizes the save state.
func (s *settingsScreen) statusLine() string {
	t := s.t
	switch {
	case isReadOnly(s.d):
		return fg(t.P.Faint, readOnlyNotice+glyphSep+"settings cannot be changed")
	case s.saving:
		return fg(t.P.Muted, "saving…")
	case s.note != "":
		return fg(t.ToneColor(s.noteTone), glyphCross+" "+s.note)
	case s.dirty():
		return fg(t.P.Warn, glyphDot+" unsaved changes") + fg(t.P.Faint, glyphSep+"ctrl+s save"+glyphSep+"u revert")
	default:
		return fg(t.P.Faint, "enter edit"+glyphSep+"ctrl+s save")
	}
}

// managerPanel shows manager facts and the public key to install.
func (s *settingsScreen) managerPanel(w, h int) string {
	t := s.t
	iw, ih := widgets.PanelInner(w, h)
	d := s.d
	if d == nil {
		d = &ui.Deps{}
	}
	listen := d.Listen
	if listen == "" {
		listen = "local session"
	}
	uptime := glyphNone
	if !d.StartedAt.IsZero() {
		uptime = format.Duration(s.fleet.Now.Sub(d.StartedAt))
	}
	const keyW = 10
	kv := func(k, v string) string {
		return widgets.FitLine(fg(t.P.Muted, k), keyW) + fg(t.P.Text, clean(v))
	}
	facts := []string{
		kv("Version", orNone(d.Version)),
		kv("User", orNone(d.User)),
		kv("Listen", listen),
		kv("Data dir", orNone(d.DataDir)),
		kv("Uptime", uptime),
		kv("Terminal", orNone(d.Term)),
	}
	compact := joinDots(t,
		fg(t.P.Text, clean(orNone(d.Version))),
		fg(t.P.Text, clean(orNone(d.User))),
		fg(t.P.Muted, clean(listen)),
		fg(t.P.Muted, "up "+uptime),
	)

	key := strings.TrimSpace(clean(d.ManagerKey))
	keyBlock := []string{fg(t.P.Muted, bold("Manager public key"))}
	if key == "" {
		keyBlock = append(keyBlock, fg(t.P.Faint, "not available"))
	} else {
		for _, l := range wrapCells(key, iw) {
			keyBlock = append(keyBlock, fg(t.P.Accent2, l))
		}
	}
	keyBlock = append(keyBlock, paint(t.P.Faint, wrapWords("add this to ~/.ssh/authorized_keys on each server", iw))...)

	var lines []string
	switch {
	case ih >= len(facts)+1+len(keyBlock):
		lines = append(append(facts, ""), keyBlock...)
	case ih >= 2+len(keyBlock):
		lines = append([]string{compact, ""}, keyBlock...)
	default:
		lines = keyBlock[1:]
	}
	return widgets.Panel(t, widgets.PanelOpts{
		Width: w, Height: h, Title: "Manager", Info: clean(d.Version), Body: strings.Join(lines, "\n"),
	})
}

// orNone substitutes a dash for empty values.
func orNone(v string) string {
	if v == "" {
		return glyphNone
	}
	return v
}
