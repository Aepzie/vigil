package sshclient

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Aepzie/vigil/internal/sshclient/sshtest"
)

const testPassword = "c0rrect horse"

// installTarget is a test server whose exec requests run the install
// command against a temporary home directory and whose public key logins
// are checked against that home's authorized_keys.
type installTarget struct {
	srv  *sshtest.Server
	home string
	// realShell reports whether the command ran in a real sh rather
	// than being emulated.
	realShell bool
}

func (it installTarget) authorizedKeys(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(it.home, ".ssh", "authorized_keys"))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// startInstallTarget starts a server with opts. Exec requests run in sh
// when one is available; otherwise the install command is emulated by
// appending the line read from stdin.
func startInstallTarget(t *testing.T, opts sshtest.Options) installTarget {
	t.Helper()
	home := t.TempDir()
	opts.AuthorizedKeysFile = filepath.Join(home, ".ssh", "authorized_keys")
	sh, err := exec.LookPath("sh")
	realShell := err == nil
	if opts.Exec == nil {
		if realShell {
			opts.Exec = shellExec(sh, t.TempDir(), home)
		} else {
			opts.Exec = emulatedInstall(opts.AuthorizedKeysFile)
		}
	}
	return installTarget{srv: sshtest.Start(t, opts), home: home, realShell: realShell}
}

// shellExec runs each command with sh and HOME=home. The command is
// written to a script file in scriptDir so that it reaches sh unchanged
// on every platform.
func shellExec(sh, scriptDir, home string) sshtest.ExecFunc {
	return func(command string, stdin io.Reader, stdout io.Writer) uint32 {
		script, err := os.CreateTemp(scriptDir, "cmd-*.sh")
		if err != nil {
			return 126
		}
		_, werr := script.WriteString(command + "\n")
		if err := errors.Join(werr, script.Close()); err != nil {
			return 126
		}
		cmd := exec.Command(sh, script.Name())
		cmd.Env = append(os.Environ(), "HOME="+filepath.ToSlash(home))
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stdout
		err = cmd.Run()
		var exitErr *exec.ExitError
		switch {
		case err == nil:
			return 0
		case errors.As(err, &exitErr):
			return uint32(exitErr.ExitCode())
		default:
			return 127
		}
	}
}

// emulatedInstall appends the first stdin line to path unless listed.
func emulatedInstall(path string) sshtest.ExecFunc {
	return func(command string, stdin io.Reader, _ io.Writer) uint32 {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil {
			return 1
		}
		raw, _ := os.ReadFile(path)
		if strings.Contains("\n"+string(raw), "\n"+line) {
			return 0
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return 1
		}
		if err := os.WriteFile(path, append(raw, line...), 0o600); err != nil {
			return 1
		}
		return 0
	}
}

func countLine(content, line string) int {
	n := 0
	for l := range strings.SplitSeq(content, "\n") {
		if l == line {
			n++
		}
	}
	return n
}

func TestInstallKey(t *testing.T) {
	tests := []struct {
		name     string
		opts     sshtest.Options
		password string
		keyPath  string
		// otherHostKey records a different host key for the server first.
		otherHostKey bool
		want         error
		wantText     string
		installed    bool
		logins       int
	}{
		{name: "password", opts: sshtest.Options{Password: testPassword}, password: testPassword, installed: true, logins: 1},
		{name: "keyboard-interactive only", opts: sshtest.Options{KeyboardInteractive: testPassword}, password: testPassword, installed: true, logins: 1},
		{name: "wrong password", opts: sshtest.Options{Password: testPassword}, password: "wrong pass", want: ErrPasswordRejected, wantText: "password rejected", logins: 1},
		{name: "no password login offered", opts: sshtest.Options{}, password: testPassword, want: ErrPasswordRejected, wantText: "password rejected"},
		{name: "custom key", opts: sshtest.Options{Password: testPassword}, password: testPassword, keyPath: "deploy_ed25519", want: ErrCustomKey, wantText: "install it manually"},
		{name: "host key mismatch", opts: sshtest.Options{Password: testPassword}, password: testPassword, otherHostKey: true, want: ErrHostKeyMismatch, wantText: "host key mismatch"},
		{
			name: "key login fails afterwards",
			opts: sshtest.Options{
				Password: testPassword,
				Exec: func(_ string, stdin io.Reader, _ io.Writer) uint32 {
					io.Copy(io.Discard, stdin)
					return 0
				},
			},
			password: testPassword, want: ErrKeyLoginFailed, wantText: "PubkeyAuthentication", logins: 1,
		},
		{
			name: "remote command fails",
			opts: sshtest.Options{
				Password: testPassword,
				Exec: func(_ string, stdin io.Reader, out io.Writer) uint32 {
					io.Copy(io.Discard, stdin)
					io.WriteString(out, "mkdir: cannot create directory: Read-only file system\x1b[31m\nmore\n")
					return 1
				},
			},
			password: testPassword, wantText: "status 1: mkdir: cannot create directory: Read-only file system", logins: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, dir := newTestDialer(t)
			target := startInstallTarget(t, tt.opts)
			if tt.otherHostKey {
				line := knownhosts.Line([]string{target.srv.Addr()}, sshtest.NewSigner(t).PublicKey())
				if err := os.WriteFile(filepath.Join(dir, "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			srv := serverFor(target.srv)
			srv.KeyPath = tt.keyPath

			err := d.InstallKey(dialCtx(t, 10*time.Second), srv, tt.password)

			switch {
			case tt.want == nil && tt.wantText == "" && err != nil:
				t.Fatalf("InstallKey: %v", err)
			case tt.want != nil && !errors.Is(err, tt.want):
				t.Fatalf("err = %v, want %v", err, tt.want)
			case tt.wantText != "" && (err == nil || !strings.Contains(err.Error(), tt.wantText)):
				t.Fatalf("err = %v, want text %q", err, tt.wantText)
			}
			if err != nil {
				for _, secret := range []string{tt.password, testPassword} {
					if strings.Contains(err.Error(), secret) {
						t.Fatalf("error reveals the password: %v", err)
					}
				}
				if strings.ContainsAny(err.Error(), "\x1b\n") {
					t.Fatalf("error not sanitized: %q", err)
				}
			}
			if got := target.srv.PasswordAttempts(); got != tt.logins {
				t.Errorf("password attempts = %d, want %d", got, tt.logins)
			}
			if got := countLine(target.authorizedKeys(t), d.PublicKey()); tt.installed != (got == 1) || got > 1 {
				t.Errorf("manager key listed %d times, installed = %v", got, tt.installed)
			}
		})
	}
}

func TestInstallKeyPasswordRejectedMatchesAuthFailed(t *testing.T) {
	d, _ := newTestDialer(t)
	target := startInstallTarget(t, sshtest.Options{Password: testPassword})
	err := d.InstallKey(dialCtx(t, 10*time.Second), serverFor(target.srv), "nope")
	var de *DialError
	if !errors.As(err, &de) || !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %#v, want *DialError matching ErrAuthFailed", err)
	}
	if got := Reason(err); got != "password rejected" {
		t.Errorf("Reason = %q", got)
	}
}

func TestInstallKeyIdempotent(t *testing.T) {
	d, _ := newTestDialer(t)
	target := startInstallTarget(t, sshtest.Options{Password: testPassword})
	if !target.realShell {
		t.Skip("no sh available to run the install command")
	}
	other := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOtherKeyOtherKeyOtherKeyOtherKeyOtherKeyOth other@host"
	path := filepath.Join(target.home, ".ssh", "authorized_keys")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := serverFor(target.srv)
	for i := range 2 {
		if err := d.InstallKey(dialCtx(t, 10*time.Second), srv, testPassword); err != nil {
			t.Fatalf("InstallKey #%d: %v", i+1, err)
		}
	}
	want := other + "\n" + d.PublicKey() + "\n"
	if got := target.authorizedKeys(t); got != want {
		t.Fatalf("authorized_keys = %q, want %q", got, want)
	}
	for _, cmd := range target.srv.Execs() {
		if strings.Contains(cmd, "AAAA") {
			t.Fatalf("key interpolated into the command: %q", cmd)
		}
	}
}

func TestInstallKeyCreatesPrivateFiles(t *testing.T) {
	d, _ := newTestDialer(t)
	target := startInstallTarget(t, sshtest.Options{Password: testPassword})
	if !target.realShell {
		t.Skip("no sh available to run the install command")
	}
	if err := d.InstallKey(dialCtx(t, 10*time.Second), serverFor(target.srv), testPassword); err != nil {
		t.Fatalf("InstallKey: %v", err)
	}
	if got := target.authorizedKeys(t); got != d.PublicKey()+"\n" {
		t.Fatalf("authorized_keys = %q", got)
	}
	if os.PathSeparator != '/' {
		return
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(target.home, ".ssh"):                    0o700,
		filepath.Join(target.home, ".ssh", "authorized_keys"): 0o600,
	} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s mode %o, want %o", path, got, want)
		}
	}
}

func TestInstallKeyCommandQuoting(t *testing.T) {
	if strings.Contains(installKeyScript, "'") {
		t.Fatal("installKeyScript must not contain single quotes")
	}
	if strings.Contains(installKeyScript, "!") {
		t.Fatal("installKeyScript must not contain '!' (csh history expansion)")
	}
}

func TestAnswerWithPassword(t *testing.T) {
	tests := []struct {
		name      string
		questions []string
		echos     []bool
		want      []string
	}{
		{"no questions", nil, nil, []string{}},
		{"password only", []string{"Password: "}, []bool{false}, []string{"pw"}},
		{"echoed and hidden", []string{"Login: ", "Password: "}, []bool{true, false}, []string{"", "pw"}},
		{"two hidden", []string{"Password: ", "OTP: "}, []bool{false, false}, []string{"pw", "pw"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := answerWithPassword("pw")("", "", tt.questions, tt.echos)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") || len(got) != len(tt.want) {
				t.Fatalf("answers %q, want %q", got, tt.want)
			}
		})
	}
	answer := answerWithPassword("pw")
	for range maxPasswordPrompts {
		if _, err := answer("", "", []string{"Password: "}, []bool{false}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := answer("", "", []string{"Password: "}, []bool{false}); err == nil {
		t.Fatal("endless password prompts were answered")
	}
}
