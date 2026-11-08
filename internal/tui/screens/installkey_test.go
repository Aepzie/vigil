package screens

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui/ui"
)

const typedPassword = "hunter2pw"

// installCall is one recorded ui.Deps.InstallKey call.
type installCall struct {
	srv         store.Server
	password    string
	hasDeadline bool
}

// installRecorder fakes ui.Deps.InstallKey and PollNow.
type installRecorder struct {
	calls  []installCall
	polled []string
	err    error
}

func (r *installRecorder) install(ctx context.Context, srv store.Server, password string) error {
	_, ok := ctx.Deadline()
	r.calls = append(r.calls, installCall{srv: srv, password: password, hasDeadline: ok})
	return r.err
}

// newInstallServers returns a Servers screen whose session can install
// keys.
func newInstallServers(t *testing.T) (*servers, *installRecorder) {
	t.Helper()
	rec := &installRecorder{}
	d := testDeps(t)
	d.InstallKey = rec.install
	d.PollNow = func(id string) { rec.polled = append(rec.polled, id) }
	s := newServers(d)
	s.Update(ui.FleetMsg{Fleet: fakeFleet()})
	return s, rec
}

// selectServer moves the selection to the server with id.
func selectServer(t *testing.T, s *servers, id string) {
	t.Helper()
	s.selected = id
	if _, ok := s.selectedServer(); !ok {
		t.Fatalf("no server %s", id)
	}
}

// assertHidden fails if the rendered view contains the secret.
func assertHidden(t *testing.T, view, secret string) {
	t.Helper()
	if strings.Contains(view, secret) || strings.Contains(ansi.Strip(view), secret) {
		t.Fatalf("view reveals the password:\n%s", ansi.Strip(view))
	}
}

func TestServersPasswordField(t *testing.T) {
	tests := []struct {
		name       string
		install    bool
		key        string
		edit       bool
		wantHidden bool
		wantOff    bool
	}{
		{name: "unavailable", install: false, wantHidden: true},
		{name: "add", install: true},
		{name: "add with custom key", install: true, key: "deploy_ed25519", wantOff: true},
		{name: "edit manager key server", install: true, edit: true},
		{name: "edit custom key server", install: true, edit: true, key: "/keys/db", wantOff: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newInstallServers(t)
			if !tt.install {
				s.d.InstallKey = nil
			}
			switch {
			case tt.edit && tt.key != "":
				selectServer(t, s, "s2")
				send(s, "e")
			case tt.edit:
				selectServer(t, s, "s3")
				send(s, "e")
			default:
				send(s, "a")
				s.form.focus = fieldKey
				typeText(s, tt.key)
			}
			field := s.form.fields[fieldPassword]
			if field.hidden != tt.wantHidden || (field.disabled != "") != tt.wantOff {
				t.Fatalf("hidden=%v disabled=%q, want hidden=%v disabled=%v", field.hidden, field.disabled, tt.wantHidden, tt.wantOff)
			}
			if !tt.wantHidden && (field.label != "Password" || field.help != passwordHelp) {
				t.Fatalf("label %q help %q", field.label, field.help)
			}
			for _, size := range [][2]int{{80, 24}, {100, 30}, {150, 44}} {
				out := s.View(size[0], size[1])
				checkSize(t, out, size[0], size[1])
				if got := strings.Contains(ansi.Strip(out), "Password"); got == tt.wantHidden {
					t.Fatalf("%dx%d: password field shown = %v", size[0], size[1], got)
				}
			}
		})
	}
}

func TestServersPasswordFieldReenabledWhenKeyCleared(t *testing.T) {
	s, _ := newInstallServers(t)
	send(s, "a")
	s.form.focus = fieldKey
	typeText(s, "k")
	if s.form.fields[fieldPassword].disabled == "" {
		t.Fatal("password field enabled with a custom key")
	}
	send(s, "backspace")
	if s.form.fields[fieldPassword].disabled != "" {
		t.Fatal("password field still disabled without a custom key")
	}
	s.form.focus = fieldUser
	if send(s, "tab"); s.form.focus != fieldPassword {
		t.Fatalf("tab from user focused %d, want the password field", s.form.focus)
	}
}

func TestServersPasswordMasked(t *testing.T) {
	s, _ := newInstallServers(t)
	send(s, "a")
	fillServerForm(s, "web-02")
	send(s, "tab")
	if s.form.focus != fieldPassword {
		t.Fatalf("focus %d, want password", s.form.focus)
	}
	typeText(s, typedPassword)
	s.Update(tea.PasteMsg{Content: "x\n"})
	if got := s.form.fields[fieldPassword].input.Value(); got != typedPassword+"x" {
		t.Fatalf("password value %q", got)
	}
	for _, size := range [][2]int{{80, 24}, {100, 30}, {150, 44}, {220, 60}} {
		out := s.View(size[0], size[1])
		checkSize(t, out, size[0], size[1])
		assertHidden(t, out, typedPassword)
		if !strings.Contains(ansi.Strip(out), strings.Repeat(string(maskRune), len(typedPassword))) {
			t.Fatalf("%dx%d: masked password not shown", size[0], size[1])
		}
	}
}

func TestServersSaveWithPasswordInstallsKey(t *testing.T) {
	s, rec := newInstallServers(t)
	send(s, "a")
	fillServerForm(s, "web-02")
	send(s, "tab")
	typeText(s, typedPassword)
	_, cmd := send(s, "ctrl+s")
	if cmd == nil || !s.form.busy {
		t.Fatal("ctrl+s did not start saving")
	}
	if got := s.form.fields[fieldPassword].input.Value(); got != "" {
		t.Fatalf("password kept in the form after submit: %q", got)
	}
	assertHidden(t, s.View(100, 30), typedPassword)
	if len(rec.calls) != 0 {
		t.Fatal("key installed before the server was saved")
	}

	saved, ok := cmd().(serverSavedMsg)
	if !ok || saved.err != nil || saved.install == nil {
		t.Fatalf("want a successful save carrying the install, got %#v", saved)
	}
	_, cmd = s.Update(saved)
	out := collect(cmd)
	var texts []string
	for _, m := range out {
		if toast, ok := m.(ui.ToastMsg); ok {
			texts = append(texts, toast.Text)
		}
	}
	if !strings.Contains(strings.Join(texts, "|"), "installing key on web-02…") {
		t.Fatalf("no installing toast: %q", texts)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("InstallKey called %d times", len(rec.calls))
	}
	call := rec.calls[0]
	if call.password != typedPassword || call.srv.ID != saved.srv.ID || call.srv.ID == "" || !call.hasDeadline {
		t.Fatalf("unexpected call %+v", call)
	}
	installed, ok := find[keyInstalledMsg](out)
	if !ok || installed.id != saved.srv.ID || installed.err != nil {
		t.Fatalf("no keyInstalledMsg: %#v", out)
	}
	rec.polled = nil
	_, cmd = s.Update(installed)
	expectToast(t, cmd, "key installed on web-02")
	if len(rec.polled) != 1 || rec.polled[0] != saved.srv.ID {
		t.Fatalf("polled %v after install", rec.polled)
	}
}

func TestServersSaveInstallDecision(t *testing.T) {
	tests := []struct {
		name     string
		password string
		key      string
		want     bool
	}{
		{"no password", "", "", false},
		{"password", typedPassword, "", true},
		{"password with custom key", typedPassword, "deploy_ed25519", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, rec := newInstallServers(t)
			send(s, "a")
			fillServerForm(s, "web-02")
			send(s, "tab")
			typeText(s, tt.password)
			s.form.focus = fieldKey
			typeText(s, tt.key)
			_, cmd := send(s, "ctrl+s")
			saved, ok := cmd().(serverSavedMsg)
			if !ok || saved.err != nil {
				t.Fatalf("save failed: %#v", saved)
			}
			if got := saved.install != nil; got != tt.want {
				t.Fatalf("install command = %v, want %v", got, tt.want)
			}
			collect(func() tea.Msg { _, cmd := s.Update(saved); return cmd() })
			if got := len(rec.calls) == 1; got != tt.want {
				t.Fatalf("InstallKey calls %d", len(rec.calls))
			}
		})
	}
}

func TestServersFailedSaveSkipsInstall(t *testing.T) {
	s, rec := newInstallServers(t)
	failing := func() tea.Msg { return serverSavedMsg{err: errors.New("disk full")} }
	msg := withKeyInstall(failing, s.d.InstallKey, typedPassword)()
	if saved := msg.(serverSavedMsg); saved.install != nil {
		t.Fatal("failed save carries an install command")
	}
	if len(rec.calls) != 0 {
		t.Fatal("key installed after a failed save")
	}
}

func TestServersKeyInstallFailureToast(t *testing.T) {
	s, rec := newInstallServers(t)
	_, cmd := s.Update(keyInstalledMsg{id: "s3", name: "edge-03", err: errors.New("ssh: password rejected\x1b[2J\nnow")})
	toast, ok := find[ui.ToastMsg](collect(cmd))
	if !ok || toast.Tone != theme.ToneBad || toast.Text != "key install on edge-03 failed: ssh: password rejected now" {
		t.Fatalf("toast %#v", toast)
	}
	if len(rec.polled) != 1 || rec.polled[0] != "s3" {
		t.Fatalf("polled %v", rec.polled)
	}
}

func TestServersKeyPrompt(t *testing.T) {
	s, rec := newInstallServers(t)
	selectServer(t, s, "s3")
	send(s, "p")
	if s.prompt == nil || !s.Capturing() {
		t.Fatal("p did not open a capturing prompt")
	}
	if h := s.Hints(); len(h) != 2 || h[0].Key != "enter" || h[1].Key != "esc" {
		t.Fatalf("prompt hints %+v", h)
	}
	if _, cmd := send(s, "enter"); cmd != nil || s.prompt == nil || s.prompt.fields[0].err != "required" {
		t.Fatal("empty password must be refused in the prompt")
	}
	typeText(s, typedPassword)
	if _, cmd := send(s, "q"); cmd != nil || s.prompt == nil {
		t.Fatal("q must be typed into the prompt")
	}
	for _, size := range [][2]int{{80, 24}, {100, 30}, {150, 44}, {40, 8}} {
		out := s.View(size[0], size[1])
		checkSize(t, out, size[0], size[1])
		assertHidden(t, out, typedPassword)
		if size[0] >= 80 && !strings.Contains(ansi.Strip(out), "Install manager key on edge-03") {
			t.Fatalf("%dx%d: prompt title missing", size[0], size[1])
		}
	}
	_, cmd := send(s, "enter")
	if s.prompt != nil || s.Capturing() {
		t.Fatal("enter did not close the prompt")
	}
	out := collect(cmd)
	if toast, ok := find[ui.ToastMsg](out); !ok || toast.Text != "installing key on edge-03…" || toast.Tone != theme.ToneInfo {
		t.Fatalf("installing toast: %#v", out)
	}
	if len(rec.calls) != 1 || rec.calls[0].password != typedPassword+"q" || rec.calls[0].srv.ID != "s3" {
		t.Fatalf("calls %+v", rec.calls)
	}
	if _, ok := find[keyInstalledMsg](out); !ok {
		t.Fatalf("no keyInstalledMsg: %#v", out)
	}
}

func TestServersKeyPromptCancel(t *testing.T) {
	s, rec := newInstallServers(t)
	selectServer(t, s, "s3")
	send(s, "p")
	typeText(s, typedPassword)
	prompt := s.prompt
	if _, cmd := send(s, "esc"); cmd != nil {
		t.Fatal("esc started a command")
	}
	if s.prompt != nil || s.Capturing() {
		t.Fatal("esc did not close the prompt")
	}
	if prompt.fields[0].input.Value() != "" {
		t.Fatal("cancelled prompt kept the password")
	}
	if len(rec.calls) != 0 {
		t.Fatal("cancel installed a key")
	}
}

func TestServersKeyPromptRefused(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		install bool
		toast   string
	}{
		{"custom key", "s2", true, "db-01 uses a custom key; install it manually"},
		{"unavailable", "s3", false, installMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newInstallServers(t)
			if !tt.install {
				s.d.InstallKey = nil
			}
			selectServer(t, s, tt.id)
			_, cmd := send(s, "p")
			if s.prompt != nil {
				t.Fatal("prompt opened")
			}
			expectToast(t, cmd, tt.toast)
		})
	}
}

func TestServersInstallKeyCommand(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		install bool
		want    string
	}{
		{"manager key", "s3", true, "Install manager key on edge-03"},
		{"custom key", "s2", true, ""},
		{"unavailable", "s3", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newInstallServers(t)
			if !tt.install {
				s.d.InstallKey = nil
			}
			selectServer(t, s, tt.id)
			var found *ui.Command
			for _, c := range s.Commands() {
				if strings.HasPrefix(c.Title, "Install manager key") {
					found = &c
				}
			}
			if (found != nil) != (tt.want != "") {
				t.Fatalf("command present = %v, want %q", found != nil, tt.want)
			}
			if found == nil {
				return
			}
			if found.Title != tt.want || found.Hint != "p" {
				t.Fatalf("command %+v", *found)
			}
			s.Update(collect(found.Run())[0])
			if s.prompt == nil {
				t.Fatal("palette command did not open the prompt")
			}
			hasHint := false
			for _, h := range newServersHints(t, s) {
				hasHint = hasHint || h.Key == "p"
			}
			if !hasHint {
				t.Fatal("no p hint")
			}
		})
	}
}

// newServersHints returns the inventory hints of s with no dialog open.
func newServersHints(t *testing.T, s *servers) []ui.Hint {
	t.Helper()
	prompt := s.prompt
	s.prompt = nil
	defer func() { s.prompt = prompt }()
	return s.Hints()
}
