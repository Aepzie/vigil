package server

import (
	"net"
	"testing"
	"time"

	"github.com/CraguS/vigil/internal/compat"
)

func TestFailureLimiter(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := newFailureLimiter(3, time.Minute)
	l.now = func() time.Time { return now }

	for i := range 3 {
		if l.blocked("1.2.3.4") {
			t.Fatalf("blocked after %d failures", i)
		}
		l.fail("1.2.3.4")
	}
	if !l.blocked("1.2.3.4") {
		t.Fatal("not blocked after reaching the limit")
	}
	if l.blocked("5.6.7.8") {
		t.Fatal("other IP blocked")
	}
	now = now.Add(time.Minute)
	if l.blocked("1.2.3.4") {
		t.Fatal("still blocked after the window")
	}
}

func TestFailureLimiterBounded(t *testing.T) {
	l := newFailureLimiter(3, time.Minute)
	for i := range maxTrackedIPs + 10 {
		l.fail(net.IPv4(10, byte(i>>16), byte(i>>8), byte(i)).String())
	}
	if n := len(l.entries); n > maxTrackedIPs {
		t.Fatalf("tracking %d IPs, limit %d", n, maxTrackedIPs)
	}
}

func TestSessionTracker(t *testing.T) {
	var tr sessionTracker
	if !tr.enter() || !tr.enter() {
		t.Fatal("enter failed before drain")
	}
	idle := tr.drain()
	if tr.enter() {
		t.Fatal("enter succeeded while draining")
	}
	tr.leave()
	select {
	case <-idle:
		t.Fatal("idle with one session left")
	default:
	}
	tr.leave()
	select {
	case <-idle:
	default:
		t.Fatal("not idle after last session")
	}
	if tr.drain() != idle {
		t.Fatal("drain is not idempotent")
	}

	var empty sessionTracker
	select {
	case <-empty.drain():
	default:
		t.Fatal("empty tracker not idle")
	}
}

func TestRemoteIP(t *testing.T) {
	tests := []struct {
		addr net.Addr
		want string
	}{
		{&net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 5000}, "192.0.2.1"},
		{&net.TCPAddr{IP: net.ParseIP("2001:db8::1"), Port: 22}, "2001:db8::1"},
		{&net.UnixAddr{Name: "/tmp/sock", Net: "unix"}, "/tmp/sock"},
		{nil, ""},
	}
	for _, tt := range tests {
		if got := remoteIP(tt.addr); got != tt.want {
			t.Errorf("remoteIP(%v) = %q, want %q", tt.addr, got, tt.want)
		}
	}
}

func TestClientEnv(t *testing.T) {
	got := clientEnv([]string{
		"LANG=C",
		"LANG=en_US.UTF-8",
		"LC_ALL=",
		"LC_CTYPE=C.UTF-8",
		"COLORTERM=truecolor",
		"TERM_PROGRAM=Wez\x1b[31mTerm",
		"HOME=/root",
		"garbage",
		"TERM=xterm-256color",
	})
	want := compat.Env{
		Term:        "xterm-256color",
		ColorTerm:   "truecolor",
		TermProgram: "WezTerm",
		LCCtype:     "C.UTF-8",
		Lang:        "en_US.UTF-8",
	}
	if got != want {
		t.Fatalf("clientEnv = %+v, want %+v", got, want)
	}
}
