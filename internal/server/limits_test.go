package server

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/log/v2"
	gossh "golang.org/x/crypto/ssh"

	"github.com/Aepzie/vigil/internal/auth"
)

func TestConnLimiter(t *testing.T) {
	l := newConnLimiter(2, 3)
	a1, ok1 := l.acquire("a")
	_, ok2 := l.acquire("a")
	if !ok1 || !ok2 {
		t.Fatal("slots within the per-client limit refused")
	}
	if _, ok := l.acquire("a"); ok {
		t.Fatal("per-client limit not enforced")
	}
	b1, ok := l.acquire("b")
	if !ok {
		t.Fatal("other client refused")
	}
	if _, ok := l.acquire("c"); ok {
		t.Fatal("total limit not enforced")
	}
	a1()
	a1()
	if l.active != 2 || l.byClient["a"] != 1 {
		t.Fatalf("double release: active %d, a %d", l.active, l.byClient["a"])
	}
	if _, ok := l.acquire("c"); !ok {
		t.Fatal("released slot not reusable")
	}
	b1()
	if _, ok := l.byClient["b"]; ok {
		t.Fatal("empty client entry kept")
	}
}

func TestPasswordChecksBounded(t *testing.T) {
	authn, err := auth.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := authn.SetPassword(testUser, testPassword); err != nil {
		t.Fatal(err)
	}
	s := &server{
		log:           log.New(io.Discard),
		deps:          Deps{Auth: authn},
		failures:      newFailureLimiter(failureLimit, failureWindow),
		passwordSlots: make(chan struct{}, 1),
	}
	addr := &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 4000}
	done, cancel := context.WithCancel(context.Background())
	cancel()

	s.passwordSlots <- struct{}{}
	if s.passwordLogin(done, testUser, addr, testPassword) {
		t.Fatal("password checked although all check slots were busy")
	}
	if len(s.failures.entries) != 0 {
		t.Fatal("a busy rejection was counted as a failed password")
	}
	<-s.passwordSlots
	if !s.passwordLogin(done, testUser, addr, testPassword) {
		t.Fatal("correct password rejected with a free slot")
	}
	if len(s.passwordSlots) != 0 {
		t.Fatal("check slot not released")
	}
}

// dialRaw opens a TCP connection and reports whether the server sent its
// SSH version banner, i.e. accepted the connection.
func dialRaw(t *testing.T, addr string) (net.Conn, bool) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.SetReadDeadline(time.Time{})
	return conn, err == nil && strings.HasPrefix(line, "SSH-2.0-")
}

func TestPreAuthConnectionLimit(t *testing.T) {
	ts := startServer(t, nil)
	signer := newSigner(t)
	line := gossh.MarshalAuthorizedKey(signer.PublicKey())
	if err := os.WriteFile(filepath.Join(ts.dataDir, auth.AuthorizedKeysFile), line, 0o600); err != nil {
		t.Fatal(err)
	}

	for i := range maxPreAuthPerClient {
		c, err := ts.dial(t, testUser, gossh.PublicKeys(signer))
		if err != nil {
			t.Fatalf("login %d: %v", i, err)
		}
		t.Cleanup(func() { _ = c.Close() })
	}

	var pending []net.Conn
	for i := range maxPreAuthPerClient {
		conn, ok := dialRaw(t, ts.addr)
		if !ok {
			t.Fatalf("unauthenticated connection %d refused; authenticated ones must not count", i)
		}
		pending = append(pending, conn)
	}
	if _, ok := dialRaw(t, ts.addr); ok {
		t.Fatalf("connection %d accepted beyond the per-client limit", maxPreAuthPerClient+1)
	}

	_ = pending[0].Close()
	waitFor(t, "a freed pre-auth slot", func() bool {
		conn, ok := dialRaw(t, ts.addr)
		_ = conn.Close()
		return ok
	})
}
