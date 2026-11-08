package screens

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
	"github.com/Aepzie/vigil/internal/widgets"
)

// Indices of the server form fields.
const (
	fieldName = iota
	fieldHost
	fieldPort
	fieldUser
	fieldPassword
	fieldKey
	fieldTags
)

// installKeyTimeout bounds one key installation (password login, remote
// command and key login check).
const installKeyTimeout = 30 * time.Second

// Texts of the password field.
const (
	passwordHelp     = "optional · used once to add the key, never stored"
	passwordDisabled = "not used with a custom SSH key; install it manually"
	installMissing   = "key install is not available in this session"
)

// Servers layout constants.
const (
	accessPanelWidth   = 46
	accessSideMinWidth = 120
	accessBottomH      = 7
	accessMinHeight    = 16
	defaultSSHPort     = 22
)

// errNoStore is reported when the session has no inventory store.
var errNoStore = errors.New("no server store available")

// errReadOnly is reported for mutations in a read-only session.
var errReadOnly = errors.New(readOnlyNotice)

// readOnlyNotice is the toast shown when a read-only session tries to
// change shared state.
const readOnlyNotice = "read-only demo"

// isReadOnly reports whether the session must not change shared state.
func isReadOnly(d *ui.Deps) bool { return d != nil && d.ReadOnly }

// refuseReadOnly tells the user that a mutation is not allowed.
func refuseReadOnly() tea.Cmd { return ui.Toast(theme.ToneWarn, readOnlyNotice) }

// serverLimitError reports that the inventory is full.
func serverLimitError(limit int) error {
	return fmt.Errorf("server limit reached (%d)", limit)
}

// serverSavedMsg is the result of an add or update.
type serverSavedMsg struct {
	srv    store.Server
	edited bool
	err    error
	// seq identifies the form that started the save (serverForm.seq).
	seq int
	// install installs the manager key on the saved server; it is nil
	// unless a password was entered. The password lives only in its
	// closure, never in the model.
	install tea.Cmd
}

// keyInstalledMsg is the result of a key installation.
type keyInstalledMsg struct {
	id   string
	name string
	err  error
}

// serverDeletedMsg is the result of a delete.
type serverDeletedMsg struct {
	id   string
	name string
	err  error
}

// serversAction is a palette-triggered action of the Servers screen.
type serversAction int

const (
	serversAdd serversAction = iota
	serversEdit
	serversDelete
	serversTest
	serversInstallKey
)

// serversActionMsg carries a palette command back into Update.
type serversActionMsg struct{ action serversAction }

// serverForm is the add/edit modal.
type serverForm struct {
	form
	// orig is the edited server; its ID is empty when adding.
	orig store.Server
	// seq tells save results of this form apart from those of forms
	// closed while their save was in flight.
	seq int
}

// keyPrompt asks for the password that installs the manager key on srv.
type keyPrompt struct {
	form
	srv store.Server
}

// servers manages the inventory.
type servers struct {
	d        *ui.Deps
	t        *theme.Theme
	fleet    ui.Fleet
	selected string
	form     *serverForm
	confirm  *confirmDialog
	prompt   *keyPrompt
	// formSeq numbers the forms opened so far.
	formSeq int
}

func newServers(d *ui.Deps) *servers {
	return &servers{d: d, t: themeOf(d), fleet: initialFleet(d)}
}

func (s *servers) Tab() ui.TabID   { return ui.TabServers }
func (s *servers) Title() string   { return "Servers" }
func (s *servers) Icon() string    { return "≡" }
func (s *servers) Init() tea.Cmd   { return nil }
func (s *servers) Capturing() bool { return s.form != nil || s.confirm != nil || s.prompt != nil }

// installKey returns the key installer of the session, or nil when keys
// cannot be installed (no dialer, read-only session).
func (s *servers) installKey() ui.InstallKeyFunc {
	if s.d == nil || s.d.ReadOnly {
		return nil
	}
	return s.d.InstallKey
}

// rows are the servers sorted by name.
func (s *servers) rows() []serverRow {
	rows := buildRows(s.fleet)
	sortRows(rows, sortName)
	return rows
}

func (s *servers) syncSelection() {
	rows := s.rows()
	if rowIndex(rows, s.selected) >= 0 {
		return
	}
	s.selected = ""
	if len(rows) > 0 {
		s.selected = rows[0].Srv.ID
	}
}

func (s *servers) move(delta int) {
	rows := s.rows()
	if i := stepIndex(rowIndex(rows, s.selected), delta, len(rows)); i >= 0 {
		s.selected = rows[i].Srv.ID
	}
}

// selectedServer returns the selected server, if any.
func (s *servers) selectedServer() (store.Server, bool) {
	for _, srv := range s.fleet.Servers {
		if srv.ID == s.selected {
			return srv, true
		}
	}
	return store.Server{}, false
}

// Update implements ui.Screen.
func (s *servers) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case ui.FleetMsg:
		s.fleet = msg.Fleet
		s.syncSelection()
	case serversActionMsg:
		return s, s.apply(msg.action)
	case serverSavedMsg:
		return s, s.saved(msg)
	case serverDeletedMsg:
		return s, s.deleted(msg)
	case keyInstalledMsg:
		return s, s.keyInstalled(msg)
	case tea.PasteMsg:
		s.paste(msg.Content)
	case tea.KeyPressMsg:
		return s, s.key(msg)
	}
	return s, nil
}

func (s *servers) apply(a serversAction) tea.Cmd {
	if s.Capturing() {
		return nil
	}
	switch a {
	case serversAdd:
		return s.openAdd()
	case serversEdit:
		return s.openEdit()
	case serversDelete:
		return s.openDelete()
	case serversTest:
		return s.testCmd()
	case serversInstallKey:
		return s.openKeyPrompt()
	}
	return nil
}

// paste inserts pasted text into the open prompt or form.
func (s *servers) paste(text string) {
	switch {
	case s.prompt != nil:
		s.prompt.Paste(text)
	case s.form != nil:
		s.form.Paste(text)
		s.form.syncPassword()
	}
}

func (s *servers) key(msg tea.KeyPressMsg) tea.Cmd {
	if s.confirm != nil {
		return s.confirmKey(msg)
	}
	if s.prompt != nil {
		return s.promptKey(msg)
	}
	if s.form != nil {
		return s.formKey(msg)
	}
	switch msg.String() {
	case "j", "down":
		s.move(1)
	case "k", "up":
		s.move(-1)
	case "g", "home":
		s.move(-len(s.fleet.Servers))
	case "G", "end":
		s.move(len(s.fleet.Servers))
	case "a":
		return s.openAdd()
	case "e":
		return s.openEdit()
	case "x", "delete":
		return s.openDelete()
	case "t":
		return s.testCmd()
	case "p":
		return s.openKeyPrompt()
	case "enter":
		if s.selected != "" {
			return emit(ui.OpenShellMsg{ServerID: s.selected})
		}
	case "d", "right", "l":
		if s.selected != "" {
			return emit(ui.ShowDetailMsg{ServerID: s.selected})
		}
	}
	return nil
}

func (s *servers) testCmd() tea.Cmd {
	return pollServer(s.d, s.fleet, s.selected)
}

// newServerForm builds the form, prefilled from srv. The password field
// is shown only when canInstall.
func newServerForm(title string, srv store.Server, canInstall bool) *serverForm {
	field := func(label, placeholder, help, value string) formField {
		in := newInput(placeholder)
		in.SetValue(value)
		return formField{label: label, help: help, input: in}
	}
	port := ""
	if srv.Port != 0 {
		port = strconv.Itoa(srv.Port)
	}
	f := &serverForm{
		orig: srv,
		form: form{
			title: title,
			fields: []formField{
				fieldName:     field("Name", "e.g. web-01", "a short unique label", srv.Name),
				fieldHost:     field("Host/IP", "e.g. 10.0.0.12 or web.example.com", "hostname, IPv4 or IPv6 address", srv.Host),
				fieldPort:     field("Port", "22", "SSH port, default 22", port),
				fieldUser:     field("User", "e.g. ubuntu", "remote login user", srv.User),
				fieldPassword: {label: "Password", help: passwordHelp, input: newMaskedInput("optional"), hidden: !canInstall},
				fieldKey:      field("SSH key", "manager key (or file name in <data>/keys)", "optional private key file name in <data>/keys", srv.KeyPath),
				fieldTags:     field("Tags", "prod, web", "comma separated", strings.Join(srv.Tags, ", ")),
			},
		},
	}
	f.syncPassword()
	return f
}

// syncPassword disables the password field while a per-server key is
// set: only the manager key can be installed.
func (f *serverForm) syncPassword() {
	f.fields[fieldPassword].disabled = ""
	if f.Value(fieldKey) != "" {
		f.fields[fieldPassword].disabled = passwordDisabled
	}
}

// takePassword returns the entered password and clears the field.
func (f *serverForm) takePassword() string {
	password := f.fields[fieldPassword].input.Value()
	f.fields[fieldPassword].input.Clear()
	return password
}

func (s *servers) openAdd() tea.Cmd {
	if isReadOnly(s.d) {
		return refuseReadOnly()
	}
	if limit := s.maxServers(); limit > 0 && len(s.fleet.Servers) >= limit {
		return ui.Toast(theme.ToneWarn, serverLimitError(limit).Error())
	}
	s.openForm(newServerForm("Add server", store.Server{}, s.installKey() != nil))
	return nil
}

func (s *servers) openEdit() tea.Cmd {
	if isReadOnly(s.d) {
		return refuseReadOnly()
	}
	srv, ok := s.selectedServer()
	if !ok {
		return nil
	}
	s.openForm(newServerForm("Edit "+clean(srv.Name), srv, s.installKey() != nil))
	return nil
}

// openForm shows f with a new sequence number.
func (s *servers) openForm(f *serverForm) {
	s.formSeq++
	f.seq = s.formSeq
	s.form = f
}

func (s *servers) openDelete() tea.Cmd {
	if isReadOnly(s.d) {
		return refuseReadOnly()
	}
	srv, ok := s.selectedServer()
	if !ok {
		return nil
	}
	s.confirm = &confirmDialog{
		title:  "Delete server",
		prompt: "Delete " + clean(srv.Name) + "? y/n",
		target: srv.ID,
	}
	return nil
}

// maxServers is the inventory cap of the session, 0 for none.
func (s *servers) maxServers() int {
	if s.d == nil {
		return 0
	}
	return s.d.MaxServers
}

func (s *servers) confirmKey(msg tea.KeyPressMsg) tea.Cmd {
	switch s.confirm.Update(msg) {
	case confirmYes:
		id := s.confirm.target
		s.confirm = nil
		return deleteServerCmd(s.d, id, s.nameOf(id))
	case confirmNo:
		s.confirm = nil
	}
	return nil
}

func (s *servers) nameOf(id string) string {
	for _, srv := range s.fleet.Servers {
		if srv.ID == id {
			return clean(srv.Name)
		}
	}
	return id
}

func (s *servers) formKey(msg tea.KeyPressMsg) tea.Cmd {
	if s.form.busy {
		if msg.String() == "esc" {
			s.form = nil
		}
		return nil
	}
	switch s.form.Update(msg) {
	case formCancel:
		s.form.takePassword()
		s.form = nil
	case formSubmit:
		return s.submit()
	default:
		s.form.syncPassword()
	}
	return nil
}

// submit parses the form locally and starts the store write. When a
// password was entered, the manager key is installed after a successful
// save; the password leaves the model as soon as that command exists.
func (s *servers) submit() tea.Cmd {
	f := s.form
	f.clearErrors()
	srv, ok := parseServerForm(&f.form, f.orig)
	if !ok {
		return nil
	}
	f.busy = true
	save := saveServerCmd(s.d, srv, f.orig.ID != "", f.seq)
	password := f.takePassword()
	install := s.installKey()
	if password == "" || srv.KeyPath != "" || install == nil {
		return save
	}
	return withKeyInstall(save, install, password)
}

// withKeyInstall makes a successful save result carry the command that
// installs the manager key on the saved server.
func withKeyInstall(save tea.Cmd, install ui.InstallKeyFunc, password string) tea.Cmd {
	return func() tea.Msg {
		msg := save()
		saved, ok := msg.(serverSavedMsg)
		if !ok || saved.err != nil {
			return msg
		}
		saved.install = installKeyCmd(install, saved.srv, password)
		return saved
	}
}

// installKeyCmd installs the manager key on srv off the UI goroutine.
func installKeyCmd(install ui.InstallKeyFunc, srv store.Server, password string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), installKeyTimeout)
		defer cancel()
		return keyInstalledMsg{id: srv.ID, name: clean(srv.Name), err: install(ctx, srv, password)}
	}
}

// installingToast announces a key installation on the server name.
func installingToast(name string) tea.Cmd {
	return ui.Toast(theme.ToneInfo, "installing key on "+name+"…")
}

// openKeyPrompt asks for the password that installs the manager key on
// the selected server.
func (s *servers) openKeyPrompt() tea.Cmd {
	if isReadOnly(s.d) {
		return refuseReadOnly()
	}
	srv, ok := s.selectedServer()
	if !ok {
		return nil
	}
	if s.installKey() == nil {
		return ui.Toast(theme.ToneWarn, installMissing)
	}
	if srv.KeyPath != "" {
		return ui.Toast(theme.ToneWarn, clean(srv.Name)+" uses a custom key; install it manually")
	}
	s.prompt = &keyPrompt{
		srv: srv,
		form: form{
			title: "Install manager key on " + clean(srv.Name),
			keys:  "enter install · esc cancel",
			fields: []formField{{
				label: "Password",
				help:  "of " + clean(srv.User) + " · used once, never stored",
				input: newMaskedInput("SSH password"),
			}},
		},
	}
	return nil
}

// promptKey handles a key press while the password prompt is open.
func (s *servers) promptKey(msg tea.KeyPressMsg) tea.Cmd {
	p := s.prompt
	switch p.Update(msg) {
	case formCancel:
		p.fields[0].input.Clear()
		s.prompt = nil
	case formSubmit:
		password := p.fields[0].input.Value()
		p.fields[0].input.Clear()
		if password == "" {
			p.fail(0, "required")
			return nil
		}
		s.prompt = nil
		install := s.installKey()
		if install == nil {
			return ui.Toast(theme.ToneWarn, installMissing)
		}
		return tea.Batch(installingToast(clean(p.srv.Name)), installKeyCmd(install, p.srv, password))
	}
	return nil
}

// keyInstalled reports the result of installKeyCmd and polls the server.
func (s *servers) keyInstalled(msg keyInstalledMsg) tea.Cmd {
	toast := ui.Toast(theme.ToneGood, "key installed on "+msg.name)
	if msg.err != nil {
		toast = ui.Toast(theme.ToneBad, "key install on "+msg.name+" failed: "+clean(msg.err.Error()))
	}
	return tea.Batch(toast, pollNowCmd(s.d, msg.id))
}

// pollNowCmd polls server id right away; it is nil when polling is not
// available.
func pollNowCmd(d *ui.Deps, id string) tea.Cmd {
	if d == nil || d.PollNow == nil || id == "" {
		return nil
	}
	poll := d.PollNow
	return func() tea.Msg {
		poll(id)
		return nil
	}
}

// parseServerForm converts the form into a server, recording field errors
// for values that cannot be parsed. orig supplies ID and timestamps.
func parseServerForm(f *form, orig store.Server) (store.Server, bool) {
	srv := orig
	srv.Name = f.Value(fieldName)
	srv.Host = f.Value(fieldHost)
	srv.User = f.Value(fieldUser)
	srv.KeyPath = f.Value(fieldKey)
	srv.Tags = splitTags(f.Value(fieldTags))
	srv.Port = defaultSSHPort
	if raw := f.Value(fieldPort); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			f.fail(fieldPort, "must be a number between 1 and 65535")
		}
		srv.Port = port
	}
	if srv.Name == "" {
		f.fail(fieldName, "required")
	}
	if srv.Host == "" {
		f.fail(fieldHost, "required")
	}
	if srv.User == "" {
		f.fail(fieldUser, "required")
	}
	if f.errorCount() > 0 {
		f.focus = slices.IndexFunc(f.fields, func(fl formField) bool { return fl.err != "" })
		return srv, false
	}
	return srv, true
}

// splitTags parses a comma separated tag list, dropping blanks and
// duplicates.
func splitTags(raw string) []string {
	var tags []string
	for part := range strings.SplitSeq(raw, ",") {
		tag := strings.TrimSpace(part)
		if tag != "" && !slices.Contains(tags, tag) {
			tags = append(tags, tag)
		}
	}
	return tags
}

// saveServerCmd validates and persists a server off the UI goroutine for
// the form numbered seq. It refuses read-only sessions and additions
// beyond d.MaxServers.
func saveServerCmd(d *ui.Deps, srv store.Server, edited bool, seq int) tea.Cmd {
	var st *store.Store
	limit := 0
	if d != nil {
		st, limit = d.Store, d.MaxServers
	}
	readOnly := isReadOnly(d)
	return func() tea.Msg {
		msg := saveServer(st, srv, edited, limit, readOnly)
		msg.seq = seq
		return msg
	}
}

// saveServer performs the store write of saveServerCmd.
func saveServer(st *store.Store, srv store.Server, edited bool, limit int, readOnly bool) serverSavedMsg {
	switch {
	case readOnly:
		return serverSavedMsg{srv: srv, edited: edited, err: errReadOnly}
	case st == nil:
		return serverSavedMsg{srv: srv, edited: edited, err: errNoStore}
	}
	if err := srv.Validate(); err != nil {
		return serverSavedMsg{srv: srv, edited: edited, err: err}
	}
	if edited {
		return serverSavedMsg{srv: srv, edited: true, err: st.Update(srv)}
	}
	if limit > 0 && len(st.Servers()) >= limit {
		return serverSavedMsg{srv: srv, err: serverLimitError(limit)}
	}
	stored, err := st.Add(srv)
	return serverSavedMsg{srv: stored, err: err}
}

// deleteServerCmd removes a server off the UI goroutine.
func deleteServerCmd(d *ui.Deps, id, name string) tea.Cmd {
	var st *store.Store
	if d != nil {
		st = d.Store
	}
	readOnly := isReadOnly(d)
	return func() tea.Msg {
		switch {
		case readOnly:
			return serverDeletedMsg{id: id, name: name, err: errReadOnly}
		case st == nil:
			return serverDeletedMsg{id: id, name: name, err: errNoStore}
		}
		return serverDeletedMsg{id: id, name: name, err: st.Delete(id)}
	}
}

// saved handles the result of saveServerCmd. Results of a form that was
// closed while saving only produce a toast (and a refresh on success); they
// never touch a form opened since.
func (s *servers) saved(msg serverSavedMsg) tea.Cmd {
	current := s.form != nil && s.form.seq == msg.seq
	if msg.err != nil {
		if current {
			s.form.busy = false
			s.showSaveError(msg.err)
			return nil
		}
		return ui.Toast(theme.ToneBad, "Save failed: "+clean(msg.err.Error()))
	}
	if current {
		s.form = nil
	}
	if s.form == nil {
		s.selected = msg.srv.ID
	}
	verb := "Added "
	if msg.edited {
		verb = "Updated "
	}
	cmds := []tea.Cmd{ui.Toast(theme.ToneGood, verb+clean(msg.srv.Name)), ui.Refresh, pollNowCmd(s.d, msg.srv.ID)}
	if msg.install != nil {
		cmds = append(cmds, installingToast(clean(msg.srv.Name)), msg.install)
	}
	return tea.Batch(cmds...)
}

// showSaveError attaches a store error to the matching field when the
// message names one, and always shows it at the bottom of the form.
func (s *servers) showSaveError(err error) {
	text := clean(err.Error())
	s.form.err = text
	lower := strings.ToLower(text)
	keywords := []struct {
		field int
		words []string
	}{
		{fieldName, []string{"name"}},
		{fieldHost, []string{"host", "address"}},
		{fieldPort, []string{"port"}},
		{fieldUser, []string{"user"}},
		{fieldKey, []string{"key"}},
		{fieldTags, []string{"tag"}},
	}
	for _, k := range keywords {
		if slices.ContainsFunc(k.words, func(w string) bool { return strings.Contains(lower, w) }) {
			s.form.fail(k.field, text)
			s.form.err = ""
			return
		}
	}
}

// deleted handles the result of deleteServerCmd.
func (s *servers) deleted(msg serverDeletedMsg) tea.Cmd {
	if msg.err != nil {
		return ui.Toast(theme.ToneBad, "Delete "+msg.name+" failed: "+clean(msg.err.Error()))
	}
	if s.selected == msg.id {
		s.move(1)
		if s.selected == msg.id {
			s.move(-1)
		}
	}
	return tea.Batch(ui.Toast(theme.ToneGood, "Deleted "+msg.name), ui.Refresh)
}

// Hints implements ui.Screen.
func (s *servers) Hints() []ui.Hint {
	switch {
	case s.confirm != nil:
		return []ui.Hint{hint("y", "delete"), hint("n", "keep")}
	case s.prompt != nil:
		return []ui.Hint{hint("enter", "install"), hint("esc", "cancel")}
	case s.form != nil:
		return formHints()
	case isReadOnly(s.d) && len(s.fleet.Servers) == 0:
		return nil
	case isReadOnly(s.d):
		return []ui.Hint{hint("j/k", "move"), hint("enter", "shell"), hint("t", "test"), hint("d", "detail")}
	case len(s.fleet.Servers) == 0:
		return []ui.Hint{hint("a", "add server")}
	default:
		hints := []ui.Hint{
			hint("j/k", "move"), hint("a", "add"), hint("e", "edit"), hint("x", "delete"),
			hint("enter", "shell"), hint("t", "test"), hint("d", "detail"),
		}
		if s.installKey() != nil {
			hints = append(hints, hint("p", "install key"))
		}
		return hints
	}
}

// Commands implements ui.Screen.
func (s *servers) Commands() []ui.Command {
	act := func(a serversAction) func() tea.Cmd {
		return func() tea.Cmd { return emit(serversActionMsg{action: a}) }
	}
	test := ui.Command{Title: "Test connection", Hint: "t", Run: act(serversTest)}
	if isReadOnly(s.d) {
		return []ui.Command{test}
	}
	cmds := []ui.Command{
		{Title: "Add server", Hint: "a", Run: act(serversAdd)},
		{Title: "Edit selected server", Hint: "e", Run: act(serversEdit)},
		{Title: "Delete selected server", Hint: "x", Run: act(serversDelete)},
		test,
	}
	if srv, ok := s.selectedServer(); ok && srv.KeyPath == "" && s.installKey() != nil {
		cmds = append(cmds, ui.Command{Title: "Install manager key on " + clean(srv.Name), Hint: "p", Run: act(serversInstallKey)})
	}
	return cmds
}

// View implements ui.Screen.
func (s *servers) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	base := s.inventory(width, height)
	switch {
	case s.form != nil:
		pw, ph := s.form.Size(width, height)
		return overlayCentered(base, s.form.View(s.t, pw, ph), pw, ph, width, height)
	case s.confirm != nil:
		pw, ph := s.confirm.Size(width, height)
		return overlayCentered(base, s.confirm.View(s.t, pw, ph), pw, ph, width, height)
	case s.prompt != nil:
		pw, ph := s.prompt.Size(width, height)
		return overlayCentered(base, s.prompt.View(s.t, pw, ph), pw, ph, width, height)
	}
	return base
}

// inventory renders the table and the access hint panel.
func (s *servers) inventory(w, h int) string {
	focused := !s.Capturing()
	switch {
	case w >= accessSideMinWidth:
		tableW := w - accessPanelWidth - 1
		return widgets.JoinH(
			s.table(tableW, h, focused),
			widgets.Blank(1, h),
			s.access(accessPanelWidth, h),
		)
	case h >= accessMinHeight:
		return widgets.JoinV(
			s.table(w, h-accessBottomH, focused),
			s.access(w, accessBottomH),
		)
	default:
		return s.table(w, h, focused)
	}
}

func (s *servers) table(w, h int, focused bool) string {
	t := s.t
	rows := s.rows()
	iw, ih := widgets.PanelInner(w, h)
	cols := fitColumns(s.columns(), iw)
	columns, cells := tableData(cols, rows)
	empty := "No servers yet — press a to add one"
	if isReadOnly(s.d) {
		empty = "No servers"
	}
	return widgets.Panel(t, widgets.PanelOpts{
		Width:   w,
		Height:  h,
		Title:   "Inventory",
		Info:    strconv.Itoa(len(rows)) + " servers",
		Focused: focused,
		Body: widgets.Table(t, widgets.TableOpts{
			Width: iw, Height: ih, Columns: columns, Rows: cells,
			Selected: rowIndex(rows, s.selected),
			Empty:    empty,
		}),
	})
}

func (s *servers) columns() []prioColumn[serverRow] {
	t := s.t
	return []prioColumn[serverRow]{
		{widgets.Column{Width: 1}, 100, func(r serverRow) string {
			return fg(t.ToneColor(r.Status.Tone), glyphDot)
		}},
		{widgets.Column{Title: "NAME", Min: 10, Flex: 2}, 99, func(r serverRow) string {
			return fg(t.P.Text, r.Name())
		}},
		{widgets.Column{Title: "HOST", Min: 12, Flex: 2}, 95, func(r serverRow) string {
			return fg(t.P.Muted, clean(r.Srv.Host))
		}},
		{widgets.Column{Title: "PORT", Width: 5, Align: widgets.AlignRightCol}, 70, func(r serverRow) string {
			return fg(t.P.Muted, strconv.Itoa(r.Srv.Port))
		}},
		{widgets.Column{Title: "USER", Min: 6, Flex: 1}, 90, func(r serverRow) string {
			return fg(t.P.Text, clean(r.Srv.User))
		}},
		{widgets.Column{Title: "KEY", Min: 11, Flex: 1}, 60, func(r serverRow) string {
			if r.Srv.KeyPath == "" {
				return fg(t.P.Faint, "manager key")
			}
			return fg(t.P.Accent2, clean(r.Srv.KeyPath))
		}},
		{widgets.Column{Title: "TAGS", Min: 8, Flex: 2}, 50, func(r serverRow) string {
			tags := make([]string, len(r.Srv.Tags))
			for i, tag := range r.Srv.Tags {
				tags[i] = clean(tag)
			}
			return fg(t.P.Accent, strings.Join(tags, " "))
		}},
		{widgets.Column{Title: "CREATED", Width: 10}, 40, func(r serverRow) string {
			if r.Srv.CreatedAt.IsZero() {
				return fg(t.P.Faint, glyphNone)
			}
			return fg(t.P.Faint, r.Srv.CreatedAt.Format("2006-01-02"))
		}},
	}
}

// access explains how to install the manager key on targets.
func (s *servers) access(w, h int) string {
	t := s.t
	iw, ih := widgets.PanelInner(w, h)
	key := strings.TrimSpace(clean(s.managerKey()))
	keyLine := fg(t.P.Faint, "manager key unavailable")
	if key != "" {
		keyLine = fg(t.P.Accent2, widgets.FitLine(key, iw))
	}
	introText := "vigil reads metrics over SSH, no agent needed. Add the manager public key to ~/.ssh/authorized_keys of each target user:"
	shortText := "Add the manager key to ~/.ssh/authorized_keys of each target user:"
	if s.installKey() != nil {
		introText = "vigil reads metrics over SSH, no agent needed. Press p to install the manager key with a one-time password, or add it to ~/.ssh/authorized_keys of each target user:"
		shortText = "Press p to install the manager key with a password, or add it to ~/.ssh/authorized_keys:"
	}
	intro := paint(t.P.Muted, wrapWords(introText, iw))
	outro := paint(t.P.Faint, wrapWords("A per-server SSH key (a file name in <data>/keys) overrides the manager key. The full key is shown in the Settings tab.", iw))
	lines := append(append(intro, keyLine, ""), outro...)
	if ih < len(lines) {
		short := paint(t.P.Muted, wrapWords(shortText, iw))
		lines = append(short, keyLine)
		if ih > len(lines)+1 {
			lines = append(lines, "", fg(t.P.Faint, "Full key in the Settings tab."))
		}
	}
	return widgets.Panel(t, widgets.PanelOpts{
		Width: w, Height: h, Title: "Access", Info: "authorized_keys",
		Body: strings.Join(lines, "\n"),
	})
}

func (s *servers) managerKey() string {
	if s.d == nil {
		return ""
	}
	return s.d.ManagerKey
}
