package screens

import (
	"errors"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/CraguS/vigil/internal/store"
	"github.com/CraguS/vigil/internal/tui/ui"
)

func newTestServers(t *testing.T) *servers {
	t.Helper()
	s := newServers(testDeps(t))
	s.Update(ui.FleetMsg{Fleet: fakeFleet()})
	return s
}

func TestServersFormCapturingAndCancel(t *testing.T) {
	s := newTestServers(t)
	if s.Capturing() {
		t.Fatal("capturing without form")
	}
	send(s, "a")
	if s.form == nil || !s.Capturing() {
		t.Fatal("a did not open a capturing form")
	}
	if got := len(s.Hints()); got == 0 || s.Hints()[0].Key != "tab" {
		t.Fatalf("form hints: %+v", s.Hints())
	}
	send(s, "esc")
	if s.form != nil || s.Capturing() {
		t.Fatal("esc did not close the form")
	}
}

func TestServersFormFieldCycling(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want int
	}{
		{"tab", []string{"tab"}, fieldHost},
		{"down", []string{"down", "down"}, fieldPort},
		{"shift+tab wraps", []string{"shift+tab"}, fieldTags},
		{"up wraps", []string{"up", "up"}, fieldKey},
		{"tab wraps", []string{"tab", "tab", "tab", "tab", "tab", "tab"}, fieldName},
		{"enter advances", []string{"enter", "enter"}, fieldPort},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServers(t)
			send(s, "a")
			for _, k := range tt.keys {
				send(s, k)
			}
			if s.form.focus != tt.want {
				t.Fatalf("focus %d, want %d", s.form.focus, tt.want)
			}
		})
	}
}

func TestServersFormLocalValidation(t *testing.T) {
	s := newTestServers(t)
	send(s, "a")
	_, cmd := send(s, "ctrl+s")
	if cmd != nil {
		t.Fatal("empty form must not start a save")
	}
	for _, f := range []int{fieldName, fieldHost, fieldUser} {
		if s.form.fields[f].err == "" {
			t.Fatalf("field %d has no error", f)
		}
	}
	if s.form.focus != fieldName {
		t.Fatalf("focus %d, want first invalid field", s.form.focus)
	}
	typeText(s, "web-02")
	if s.form.fields[fieldName].err != "" {
		t.Fatal("typing should clear the field error")
	}
	send(s, "tab")
	typeText(s, "10.0.0.9")
	send(s, "tab")
	typeText(s, "abc")
	send(s, "tab")
	typeText(s, "ops")
	_, cmd = send(s, "ctrl+s")
	if cmd != nil || s.form.fields[fieldPort].err == "" || s.form.focus != fieldPort {
		t.Fatal("bad port must be reported on the port field")
	}
}

func TestServersAddFlow(t *testing.T) {
	s := newTestServers(t)
	send(s, "a")
	typeText(s, "web-02")
	send(s, "tab")
	typeText(s, "10.0.0.9")
	send(s, "tab")
	send(s, "tab")
	typeText(s, "ops")
	send(s, "tab")
	send(s, "tab")
	typeText(s, "prod, web, prod")
	_, cmd := send(s, "enter")
	if cmd == nil || !s.form.busy {
		t.Fatal("enter on the last field should start saving")
	}
	msgs := collect(cmd)
	saved, ok := find[serverSavedMsg](msgs)
	if !ok {
		t.Fatalf("want serverSavedMsg, got %#v", msgs)
	}
	if saved.srv.Port != 22 || !slices.Equal(saved.srv.Tags, []string{"prod", "web"}) || saved.edited {
		t.Fatalf("unexpected server %+v", saved.srv)
	}
	_, cmd = s.Update(saved)
	if saved.err != nil {
		if s.form == nil {
			t.Fatal("form closed despite error")
		}
		return
	}
	if s.form != nil || s.Capturing() {
		t.Fatal("form still open after successful save")
	}
	out := collect(cmd)
	if _, ok := find[ui.ToastMsg](out); !ok {
		t.Fatalf("no toast after save: %#v", out)
	}
	if _, ok := find[ui.RefreshMsg](out); !ok {
		t.Fatalf("no refresh after save: %#v", out)
	}
}

func TestServersSaveErrorShownInForm(t *testing.T) {
	s := newTestServers(t)
	send(s, "a")
	s.form.busy = true
	s.Update(serverSavedMsg{err: errors.New("host: invalid address"), seq: s.form.seq})
	if s.form == nil || s.form.busy {
		t.Fatal("form must stay open and become editable again")
	}
	if s.form.fields[fieldHost].err == "" || s.form.focus != fieldHost {
		t.Fatalf("store error not attached to host field: %+v", s.form.fields[fieldHost])
	}
	s.Update(serverSavedMsg{err: errors.New("disk full"), seq: s.form.seq})
	if s.form.err != "disk full" {
		t.Fatalf("general error %q", s.form.err)
	}
}

// fillServerForm types a valid server into the open add form.
func fillServerForm(s *servers, name string) {
	typeText(s, name)
	send(s, "tab")
	typeText(s, "10.0.0.9")
	send(s, "tab")
	send(s, "tab")
	typeText(s, "ops")
}

func TestServersStaleSaveResultIgnored(t *testing.T) {
	s := newTestServers(t)
	send(s, "a")
	fillServerForm(s, "web-02")
	_, firstSave := send(s, "ctrl+s")
	if firstSave == nil || !s.form.busy {
		t.Fatal("ctrl+s did not start saving")
	}
	send(s, "esc")
	if s.form != nil {
		t.Fatal("esc did not close the busy form")
	}

	send(s, "e")
	second := s.form
	if second == nil {
		t.Fatal("e did not open the edit form")
	}
	firstResult, ok := find[serverSavedMsg](collect(firstSave))
	if !ok {
		t.Fatal("no save result")
	}
	failed := firstResult
	failed.err = errors.New("host: invalid address")
	_, cmd := s.Update(failed)
	if s.form != second || second.err != "" || second.fields[fieldHost].err != "" {
		t.Fatal("stale error landed on the new form")
	}
	if toast, ok := find[ui.ToastMsg](collect(cmd)); !ok || toast.Text != "Save failed: host: invalid address" {
		t.Fatalf("stale error not toasted: %#v", collect(cmd))
	}

	if firstResult.err != nil {
		t.Fatalf("first save failed: %v", firstResult.err)
	}
	_, cmd = s.Update(firstResult)
	if s.form != second {
		t.Fatal("stale success closed the new form")
	}
	out := collect(cmd)
	if toast, ok := find[ui.ToastMsg](out); !ok || toast.Text != "Added web-02" {
		t.Fatalf("stale success not toasted: %#v", out)
	}
	if _, ok := find[ui.RefreshMsg](out); !ok {
		t.Fatalf("stale success did not refresh: %#v", out)
	}
	if s.selected != "s2" {
		t.Fatalf("selection moved under the open form to %q", s.selected)
	}
}

func TestServersEditPrefills(t *testing.T) {
	s := newTestServers(t)
	send(s, "e")
	if s.form == nil {
		t.Fatal("e did not open the form")
	}
	if got := s.form.Value(fieldName); got != "db-01" {
		t.Fatalf("name %q, want db-01", got)
	}
	if got := s.form.Value(fieldPort); got != "2222" {
		t.Fatalf("port %q", got)
	}
	if got := s.form.Value(fieldTags); got != "prod, db" {
		t.Fatalf("tags %q", got)
	}
	srv, ok := parseServerForm(&s.form.form, s.form.orig)
	if !ok || srv.ID != "s2" || srv.KeyPath != "/keys/db" {
		t.Fatalf("parsed %+v ok=%v", srv, ok)
	}
}

func TestServersDeleteConfirm(t *testing.T) {
	s := newTestServers(t)
	send(s, "x")
	if s.confirm == nil || !s.Capturing() {
		t.Fatal("x did not open a capturing confirm dialog")
	}
	if s.confirm.prompt != "Delete db-01? y/n" {
		t.Fatalf("prompt %q", s.confirm.prompt)
	}
	if _, cmd := send(s, "j"); cmd != nil || s.confirm == nil {
		t.Fatal("other keys must be swallowed")
	}
	send(s, "n")
	if s.confirm != nil || s.Capturing() {
		t.Fatal("n did not close the dialog")
	}
	send(s, "delete")
	_, cmd := send(s, "y")
	if s.confirm != nil {
		t.Fatal("y did not close the dialog")
	}
	msg, ok := find[serverDeletedMsg](collect(cmd))
	if !ok || msg.id != "s2" {
		t.Fatalf("want delete of s2, got %#v", collect(cmd))
	}
	_, cmd = s.Update(serverDeletedMsg{id: "s2", name: "db-01"})
	if _, ok := find[ui.RefreshMsg](collect(cmd)); !ok {
		t.Fatal("no refresh after delete")
	}
	if s.selected == "s2" {
		t.Fatal("selection still on deleted server")
	}
}

func TestServersEnterOpensShell(t *testing.T) {
	s := newTestServers(t)
	send(s, "j")
	_, cmd := send(s, "enter")
	msg, ok := find[ui.OpenShellMsg](collect(cmd))
	if !ok || msg.ServerID != "s3" {
		t.Fatalf("want OpenShellMsg{s3}, got %#v", collect(cmd))
	}
}

func TestServersNoStore(t *testing.T) {
	cmd := saveServerCmd(&ui.Deps{}, store.Server{Name: "a"}, false, 0)
	msg, ok := cmd().(serverSavedMsg)
	if !ok || !errors.Is(msg.err, errNoStore) {
		t.Fatalf("got %#v", msg)
	}
}

func TestServersFormPaste(t *testing.T) {
	s := newTestServers(t)
	send(s, "a")
	s.Update(tea.PasteMsg{Content: "db\x07-02\n"})
	if got := s.form.Value(fieldName); got != "db-02" {
		t.Fatalf("pasted %q", got)
	}
}

func TestSplitTags(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{" , ,", nil},
		{"prod", []string{"prod"}},
		{"prod, web ,prod,  db", []string{"prod", "web", "db"}},
	}
	for _, tt := range tests {
		if got := splitTags(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("splitTags(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func expectToast(t *testing.T, cmd tea.Cmd, text string) {
	t.Helper()
	toast, ok := find[ui.ToastMsg](collect(cmd))
	if !ok || toast.Text != text {
		t.Fatalf("want toast %q, got %#v", text, collect(cmd))
	}
}

func TestServersReadOnly(t *testing.T) {
	d := testDeps(t)
	d.ReadOnly = true
	s := newServers(d)
	s.Update(ui.FleetMsg{Fleet: fakeFleet()})

	for _, key := range []string{"a", "e", "x", "delete"} {
		_, cmd := send(s, key)
		if s.Capturing() {
			t.Fatalf("%s opened a dialog in a read-only session", key)
		}
		expectToast(t, cmd, readOnlyNotice)
	}
	for _, a := range []serversAction{serversAdd, serversEdit, serversDelete} {
		_, cmd := s.Update(serversActionMsg{action: a})
		if s.Capturing() {
			t.Fatalf("palette action %d opened a dialog", a)
		}
		expectToast(t, cmd, readOnlyNotice)
	}
	for _, h := range s.Hints() {
		if h.Key == "a" || h.Key == "e" || h.Key == "x" {
			t.Fatalf("mutation hint %+v shown", h)
		}
	}
	if cmds := s.Commands(); len(cmds) != 1 || cmds[0].Title != "Test connection" {
		t.Fatalf("read-only commands: %+v", cmds)
	}
	if _, cmd := send(s, "t"); cmd == nil {
		t.Fatal("test connection must stay available")
	}

	srv := store.Server{Name: "web-09", Host: "10.0.0.9", Port: 22, User: "ops"}
	if msg := saveServerCmd(d, srv, false, 0)().(serverSavedMsg); !errors.Is(msg.err, errReadOnly) {
		t.Fatalf("save in read-only session: %v", msg.err)
	}
	if msg := deleteServerCmd(d, "s1", "web-01")().(serverDeletedMsg); !errors.Is(msg.err, errReadOnly) {
		t.Fatalf("delete in read-only session: %v", msg.err)
	}
	if n := len(d.Store.Servers()); n != 0 {
		t.Fatalf("read-only session changed the store: %d servers", n)
	}
}

func TestServersMaxServers(t *testing.T) {
	d := testDeps(t)
	d.MaxServers = len(fakeFleet().Servers)
	s := newServers(d)
	s.Update(ui.FleetMsg{Fleet: fakeFleet()})
	_, cmd := send(s, "a")
	if s.form != nil {
		t.Fatal("add form opened at the server limit")
	}
	expectToast(t, cmd, "server limit reached (4)")
	if send(s, "e"); s.form == nil {
		t.Fatal("editing must stay possible at the limit")
	}

	d.MaxServers = 1
	first := store.Server{Name: "web-09", Host: "10.0.0.9", Port: 22, User: "ops"}
	saved := saveServerCmd(d, first, false, 0)().(serverSavedMsg)
	if saved.err != nil {
		t.Fatalf("first add: %v", saved.err)
	}
	second := store.Server{Name: "web-10", Host: "10.0.0.10", Port: 22, User: "ops"}
	if msg := saveServerCmd(d, second, false, 0)().(serverSavedMsg); msg.err == nil {
		t.Fatal("add beyond the limit succeeded")
	}
	saved.srv.User = "root"
	if msg := saveServerCmd(d, saved.srv, true, 0)().(serverSavedMsg); msg.err != nil {
		t.Fatalf("update at the limit: %v", msg.err)
	}
}
