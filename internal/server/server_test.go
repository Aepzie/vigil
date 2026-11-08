package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/log/v2"
	"charm.land/ssh"
	gossh "golang.org/x/crypto/ssh"

	"github.com/Aepzie/vigil/internal/alerts"
	"github.com/Aepzie/vigil/internal/auth"
	"github.com/Aepzie/vigil/internal/collector"
	"github.com/Aepzie/vigil/internal/demo"
	"github.com/Aepzie/vigil/internal/sshclient"
	"github.com/Aepzie/vigil/internal/store"
)

const (
	testUser     = "admin"
	testPassword = "correct horse battery"
)

type testServer struct {
	addr    string
	dataDir string
	cancel  context.CancelFunc
	done    chan error
}

// startServer runs Serve on 127.0.0.1:0 with a fresh data directory that
// contains one user, and stops it when the test ends.
func startServer(t *testing.T, tweak func(*Config)) *testServer {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	dialer, err := sshclient.NewDialer(dir)
	if err != nil {
		t.Fatal(err)
	}
	hub, err := collector.New(collector.Options{Store: st, Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	authn, err := auth.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := authn.SetPassword(testUser, testPassword); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		DataDir:     dir,
		Version:     "test",
		IdleTimeout: time.Minute,
		Logger:      log.New(io.Discard),
	}
	if tweak != nil {
		tweak(&cfg)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ts := &testServer{addr: ln.Addr().String(), dataDir: dir, cancel: cancel, done: make(chan error, 1)}
	go func() {
		ts.done <- Serve(ctx, ln, cfg, Deps{
			Store:     st,
			Hub:       hub,
			Alerts:    alerts.NewEngine(alerts.Thresholds{}),
			Dialer:    dialer,
			Auth:      authn,
			StartedAt: time.Now(),
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-ts.done:
		case <-time.After(ShutdownTimeout + 5*time.Second):
			t.Error("server did not shut down")
		}
	})
	return ts
}

func (ts *testServer) dial(t *testing.T, user string, methods ...gossh.AuthMethod) (*gossh.Client, error) {
	t.Helper()
	return gossh.Dial("tcp", ts.addr, &gossh.ClientConfig{
		User:            user,
		Auth:            methods,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	})
}

func (ts *testServer) login(t *testing.T) *gossh.Client {
	t.Helper()
	c, err := ts.dial(t, testUser, gossh.Password(testPassword))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// syncBuffer collects output written by a goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// openTUI starts a PTY shell session and waits for the first output.
func openTUI(t *testing.T, c *gossh.Client) (*gossh.Session, *syncBuffer, *syncBuffer) {
	t.Helper()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	var stdout, stderr syncBuffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	if err := sess.RequestPty("xterm-256color", 30, 100, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}
	return sess, &stdout, &stderr
}

func TestSessionStartsWithPassword(t *testing.T) {
	ts := startServer(t, nil)
	c := ts.login(t)
	sess, stdout, _ := openTUI(t, c)
	waitFor(t, "dashboard output", func() bool { return stdout.Len() > 0 })
	if err := sess.WindowChange(40, 120); err != nil {
		t.Fatalf("window change: %v", err)
	}
	if err := sess.WindowChange(41, 121); err != nil {
		t.Fatalf("window change: %v", err)
	}
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func TestIdleSessionIsClosed(t *testing.T) {
	ts := startServer(t, func(c *Config) { c.IdleTimeout = 2 * time.Second })
	c := ts.login(t)
	sess, stdout, _ := openTUI(t, c)
	waitFor(t, "dashboard output", func() bool { return stdout.Len() > 0 })
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("idle session was not closed")
	}
}

func TestHostKeyIsPrivate(t *testing.T) {
	ts := startServer(t, nil)
	_ = ts.login(t)
	fi, err := os.Stat(filepath.Join(ts.dataDir, HostKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("host key mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestRejectsBadCredentials(t *testing.T) {
	ts := startServer(t, nil)
	tests := []struct {
		name, user, password string
	}{
		{"wrong password", testUser, "wrong password"},
		{"unknown user", "mallory", testPassword},
		{"empty password", testUser, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			c, err := ts.dial(t, tt.user, gossh.Password(tt.password))
			if err == nil {
				_ = c.Close()
				t.Fatal("login succeeded")
			}
			if elapsed := time.Since(start); elapsed < failurePenalty {
				t.Errorf("failed login took %v, want at least %v", elapsed, failurePenalty)
			}
		})
	}
}

func newSigner(t *testing.T) gossh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestPublicKeyAuth(t *testing.T) {
	ts := startServer(t, nil)
	allowed, other := newSigner(t), newSigner(t)
	line := gossh.MarshalAuthorizedKey(allowed.PublicKey())
	if err := os.WriteFile(filepath.Join(ts.dataDir, auth.AuthorizedKeysFile), line, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := ts.dial(t, "anyone", gossh.PublicKeys(allowed))
	if err != nil {
		t.Fatalf("listed key rejected: %v", err)
	}
	_ = c.Close()
	if c, err := ts.dial(t, testUser, gossh.PublicKeys(other)); err == nil {
		_ = c.Close()
		t.Fatal("unlisted key accepted")
	}
}

func TestRequiresPTY(t *testing.T) {
	ts := startServer(t, nil)
	c := ts.login(t)
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	var stderr bytes.Buffer
	sess.Stderr = &stderr
	err = sess.Run("")
	var exit *gossh.ExitError
	if !errors.As(err, &exit) || exit.ExitStatus() != 1 {
		t.Fatalf("Run = %v, want exit status 1", err)
	}
	if !strings.Contains(stderr.String(), "ssh -t") {
		t.Fatalf("stderr = %q, want a hint mentioning ssh -t", stderr.String())
	}
}

func TestMaxSessions(t *testing.T) {
	ts := startServer(t, func(c *Config) { c.MaxSessions = 1 })
	c := ts.login(t)
	_, first, _ := openTUI(t, c)
	waitFor(t, "first session output", func() bool { return first.Len() > 0 })

	_, _, busy := openTUI(t, c)
	waitFor(t, "busy message", func() bool { return strings.Contains(busy.String(), "busy") })
}

func TestShutdownEndsSessions(t *testing.T) {
	ts := startServer(t, nil)
	c := ts.login(t)
	sess, stdout, _ := openTUI(t, c)
	waitFor(t, "dashboard output", func() bool { return stdout.Len() > 0 })

	start := time.Now()
	ts.cancel()
	select {
	case err := <-ts.done:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
		ts.done <- nil
	case <-time.After(ShutdownTimeout + 5*time.Second):
		t.Fatal("Serve did not return")
	}
	if elapsed := time.Since(start); elapsed >= ShutdownTimeout {
		t.Errorf("shutdown took %v; sessions should end without hitting the timeout", elapsed)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- sess.Wait() }()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("client session still open after shutdown")
	}
	if !strings.Contains(stdout.String(), "shutting down") {
		t.Errorf("no shutdown notice in output")
	}
}

// TestRawOutput checks that rawOutput reaches the SSH channel beneath an
// emulated PTY, so remote shell output is not rewritten.
func TestRawOutput(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &ssh.Server{
		Handler: func(sess ssh.Session) {
			_, _ = io.WriteString(rawOutput(sess), "raw\nline")
			_, _ = io.WriteString(sess, "|cooked\nline")
		},
		PasswordHandler: func(ssh.Context, string) bool { return true },
	}
	if err := srv.SetOption(ssh.HostKeyPEM(testHostKeyPEM(t))); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	c, err := gossh.Dial("tcp", ln.Addr().String(), &gossh.ClientConfig{
		User:            "x",
		Auth:            []gossh.AuthMethod{gossh.Password("x")},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.RequestPty("xterm", 24, 80, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	out, _ := sess.Output("")
	if got, want := string(out), "raw\nline|cooked\r\nline"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestPublicMode(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	dialer, err := sshclient.NewDialer(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: dir, Public: true, Logger: log.New(io.Discard)}
	deps := Deps{Store: st, Alerts: alerts.NewEngine(alerts.Thresholds{}), Source: demo.New(st, nil, 1), Dialer: dialer}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := Serve(context.Background(), ln, cfg, deps); err == nil {
		t.Fatal("public server with a Dialer was accepted")
	}

	deps.Dialer = nil
	ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, cfg, deps) }()
	defer func() {
		cancel()
		<-done
	}()
	noQuestions := gossh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) { return nil, nil })
	c, err := gossh.Dial("tcp", ln.Addr().String(), &gossh.ClientConfig{
		User:            "guest",
		Auth:            []gossh.AuthMethod{noQuestions},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	})
	if err != nil {
		t.Fatalf("public login failed: %v", err)
	}
	defer c.Close()
	_, stdout, _ := openTUI(t, c)
	waitFor(t, "dashboard output", func() bool { return stdout.Len() > 0 })
}

// namedSession is an ssh.Session that only knows its user.
type namedSession struct {
	ssh.Session
	user string
}

func (s namedSession) User() string { return s.user }

func TestUIDepsReadOnlyWhenPublic(t *testing.T) {
	for _, public := range []bool{false, true} {
		s := &server{cfg: Config{Public: public}, deps: Deps{MaxServers: 50}}
		d := s.uiDeps(namedSession{user: "guest"}, "xterm", newSession(80, 24, io.Discard))
		if d.ReadOnly != public {
			t.Errorf("public=%v: ReadOnly = %v", public, d.ReadOnly)
		}
		if d.MaxServers != 50 {
			t.Errorf("public=%v: MaxServers = %d, want 50", public, d.MaxServers)
		}
	}
}

func TestUIDepsInstallKey(t *testing.T) {
	dialer, err := sshclient.NewDialer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		public bool
		dialer *sshclient.Dialer
		want   bool
	}{
		{"with dialer", false, dialer, true},
		{"without dialer", false, nil, false},
		{"public", true, dialer, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{cfg: Config{Public: tt.public}, deps: Deps{Dialer: tt.dialer}}
			d := s.uiDeps(namedSession{user: "admin"}, "xterm", newSession(80, 24, io.Discard))
			if got := d.InstallKey != nil; got != tt.want {
				t.Fatalf("InstallKey available = %v, want %v", got, tt.want)
			}
		})
	}
}

func testHostKeyPEM(t *testing.T) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := gossh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block)
}
