package sshclient

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/CraguS/vigil/internal/sshclient/sshtest"
	"github.com/CraguS/vigil/internal/store"
)

func newTestDialer(t *testing.T) (*Dialer, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := NewDialer(dir)
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	return d, dir
}

func serverFor(s *sshtest.Server) store.Server {
	return store.Server{ID: "s1", Name: "test", Host: s.Host(), Port: s.Port(), User: "vigil"}
}

func publicKeyOf(t *testing.T, d *Dialer) ssh.PublicKey {
	t.Helper()
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(d.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func dialCtx(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	return ctx
}

func TestDialTrustOnFirstUse(t *testing.T) {
	d, dir := newTestDialer(t)
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{publicKeyOf(t, d)}})
	target := serverFor(srv)

	client, err := d.Dial(dialCtx(t, 5*time.Second), target)
	if err != nil {
		t.Fatalf("first Dial: %v", err)
	}
	client.Close()

	raw, err := os.ReadFile(filepath.Join(dir, "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	wantLine := knownhosts.Line([]string{srv.Addr()}, srv.HostKey().PublicKey())
	if strings.TrimSpace(string(raw)) != wantLine {
		t.Fatalf("known_hosts = %q, want %q", raw, wantLine)
	}
	if !strings.HasPrefix(wantLine, "[127.0.0.1]:") {
		t.Errorf("known_hosts address not normalized with port: %q", wantLine)
	}

	client, err = d.Dial(dialCtx(t, 5*time.Second), target)
	if err != nil {
		t.Fatalf("second Dial: %v", err)
	}
	client.Close()
	raw2, _ := os.ReadFile(filepath.Join(dir, "known_hosts"))
	if string(raw2) != string(raw) {
		t.Errorf("known_hosts changed on second dial: %q", raw2)
	}

	srv.SetHostKey(sshtest.NewSigner(t))
	_, err = d.Dial(dialCtx(t, 5*time.Second), target)
	if !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("Dial after host key change: err = %v, want ErrHostKeyMismatch", err)
	}
	if !strings.Contains(err.Error(), "known_hosts line 1") {
		t.Errorf("mismatch error lacks file position: %v", err)
	}
	if got := Reason(err); got != "host key mismatch!" {
		t.Errorf("Reason = %q", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	client, err = d.Dial(dialCtx(t, 5*time.Second), target)
	if err != nil {
		t.Fatalf("Dial after clearing known_hosts: %v", err)
	}
	client.Close()
}

func TestDialKnownHostWithOtherKeyType(t *testing.T) {
	d, dir := newTestDialer(t)
	srv := sshtest.Start(t, sshtest.Options{})
	other := sshtest.NewSigner(t)
	line := knownhosts.Line([]string{srv.Addr()}, other.PublicKey())
	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), []byte("# comment\n"+line), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := d.Dial(dialCtx(t, 5*time.Second), serverFor(srv))
	if !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("err = %v, want ErrHostKeyMismatch", err)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error should reference line 2: %v", err)
	}
}

func TestHostKeyAlgorithms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaPub, err := ssh.NewPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	rsaLine := knownhosts.Line([]string{"10.0.0.1:2222"}, rsaPub) + "\n"
	if err := os.WriteFile(path, []byte(rsaLine), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := openHostKeyDB(path)
	if err != nil {
		t.Fatal(err)
	}
	remote := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 2222}
	got := db.algorithms("10.0.0.1:2222", remote)
	want := []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("known host algorithms = %v, want %v", got, want)
	}
	unknown := db.algorithms("10.0.0.2:22", &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 22})
	if strings.Join(unknown, ",") != strings.Join(preferredHostKeyAlgorithms, ",") {
		t.Errorf("unknown host algorithms = %v", unknown)
	}
	for _, a := range unknown {
		if strings.Contains(a, "cert") || a == ssh.KeyAlgoRSA {
			t.Errorf("unexpected algorithm offered: %s", a)
		}
	}
}

func TestAppendKeepsExistingContent(t *testing.T) {
	d, dir := newTestDialer(t)
	path := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(path, []byte("# no trailing newline"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := sshtest.Start(t, sshtest.Options{})
	client, err := d.Dial(dialCtx(t, 5*time.Second), serverFor(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	client.Close()
	raw, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 || lines[0] != "# no trailing newline" {
		t.Fatalf("known_hosts = %q", raw)
	}
}

func TestDialErrors(t *testing.T) {
	d, _ := newTestDialer(t)
	stranger := sshtest.NewSigner(t)
	authSrv := sshtest.Start(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{stranger.PublicKey()}})

	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { silent.Close() })
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	t.Cleanup(func() {
		for {
			select {
			case c := <-accepted:
				c.Close()
			default:
				return
			}
		}
	})

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := closed.Addr().(*net.TCPAddr).Port
	closed.Close()

	eofLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eofLn.Close() })
	go func() {
		for {
			c, err := eofLn.Accept()
			if err != nil {
				return
			}
			io.WriteString(c, "SSH-2.0-test\r\n")
			c.Close()
		}
	}()

	keyDir := t.TempDir()
	tests := []struct {
		name    string
		srv     store.Server
		timeout time.Duration
		kind    error
		reason  string
	}{
		{"auth failed", serverFor(authSrv), 5 * time.Second, ErrAuthFailed, "auth failed"},
		{"handshake timeout", store.Server{Host: "127.0.0.1", Port: silent.Addr().(*net.TCPAddr).Port, User: "u"}, 300 * time.Millisecond, ErrTimeout, "timeout"},
		{"refused", store.Server{Host: "127.0.0.1", Port: closedPort, User: "u"}, 10 * time.Second, ErrRefused, "connection refused"},
		{"closed by server", store.Server{Host: "127.0.0.1", Port: eofLn.Addr().(*net.TCPAddr).Port, User: "u"}, 5 * time.Second, ErrClosed, "connection lost"},
		{"missing key file", store.Server{Host: "127.0.0.1", Port: 22, User: "u", KeyPath: filepath.Join(keyDir, "missing")}, time.Second, nil, ""},
		{"no user", store.Server{Host: "127.0.0.1", Port: 22}, time.Second, nil, "no user configured"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			client, err := d.Dial(dialCtx(t, tt.timeout), tt.srv)
			if err == nil {
				client.Close()
				t.Fatal("Dial succeeded")
			}
			var de *DialError
			if !errors.As(err, &de) {
				t.Fatalf("err %T is not *DialError: %v", err, err)
			}
			if tt.kind != nil && !errors.Is(err, tt.kind) {
				t.Errorf("err = %v, want kind %v", err, tt.kind)
			}
			if tt.reason != "" && !strings.Contains(Reason(err), tt.reason) {
				t.Errorf("Reason = %q, want %q", Reason(err), tt.reason)
			}
			if elapsed := time.Since(start); elapsed > tt.timeout+2*time.Second {
				t.Errorf("Dial took %v, timeout was %v", elapsed, tt.timeout)
			}
		})
	}
}

func TestDialCanceledContext(t *testing.T) {
	d, _ := newTestDialer(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			io.Copy(io.Discard, c)
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err = d.Dial(ctx, store.Server{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, User: "u"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("cancel was not honored promptly")
	}
}

func TestDialWithPerServerKey(t *testing.T) {
	d, _ := newTestDialer(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	keyDir := filepath.Join(home, ".ssh")
	other, err := NewDialer(keyDir)
	if err != nil {
		t.Fatal(err)
	}
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{publicKeyOf(t, other)}})
	target := serverFor(srv)

	if _, err := d.Dial(dialCtx(t, 5*time.Second), target); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("manager key should be rejected, got %v", err)
	}
	target.KeyPath = "~/.ssh/id_ed25519"
	client, err := d.Dial(dialCtx(t, 5*time.Second), target)
	if err != nil {
		t.Fatalf("Dial with KeyPath: %v", err)
	}
	client.Close()
}

func TestReason(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{context.DeadlineExceeded, "timeout"},
		{&DialError{Kind: ErrRefused, Err: errors.New("x")}, "connection refused"},
		{io.EOF, "connection lost"},
		{errors.New("weird \x1b[31mred\x1b[0m\nthing"), "weird red thing"},
		{errors.New(strings.Repeat("a", 200)), strings.Repeat("a", 79) + "…"},
	}
	for _, tt := range tests {
		if got := Reason(tt.err); got != tt.want {
			t.Errorf("Reason(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}
