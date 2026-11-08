package server

import (
	"errors"
	"math"
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
		if err := l.blocked("1.2.3.4"); err != nil {
			t.Fatalf("blocked after %d failures: %v", i, err)
		}
		l.fail("1.2.3.4")
	}
	if err := l.blocked("1.2.3.4"); !errors.Is(err, errClientFailures) {
		t.Fatalf("blocked = %v after reaching the limit, want %v", err, errClientFailures)
	}
	if err := l.blocked("5.6.7.8"); err != nil {
		t.Fatalf("other IP blocked: %v", err)
	}
	now = now.Add(time.Minute)
	if err := l.blocked("1.2.3.4"); err != nil {
		t.Fatalf("still blocked after the window: %v", err)
	}
}

func TestFailureLimiterBounded(t *testing.T) {
	l := newFailureLimiter(3, time.Minute)
	l.globalLimit = math.MaxInt
	for i := range maxTrackedIPs + 10 {
		l.fail(net.IPv4(10, byte(i>>16), byte(i>>8), byte(i)).String())
	}
	if n := len(l.entries); n > maxTrackedIPs {
		t.Fatalf("tracking %d IPs, limit %d", n, maxTrackedIPs)
	}
	if n := l.recent.Len(); n != len(l.entries) {
		t.Fatalf("recency list has %d entries, map %d", n, len(l.entries))
	}
}

// TestFailureLimiterFloodKeepsActiveClients checks that a flood of new
// addresses evicts the least recently seen clients instead of wiping
// every counter, so a blocked client that keeps trying stays blocked.
func TestFailureLimiterFloodKeepsActiveClients(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := newFailureLimiter(3, time.Minute)
	l.now = func() time.Time { return now }
	l.maxEntries = 64
	l.globalLimit = math.MaxInt

	const attacker = "203.0.113.7"
	for range 3 {
		l.fail(attacker)
	}
	l.fail("198.51.100.1")
	for i := range 10 * l.maxEntries {
		l.fail(net.IPv4(10, 0, byte(i>>8), byte(i)).String())
		if i%(l.maxEntries/2) == 0 {
			if err := l.blocked(attacker); !errors.Is(err, errClientFailures) {
				t.Fatalf("attacker unblocked after %d new addresses: %v", i, err)
			}
		}
	}
	if len(l.entries) > l.maxEntries {
		t.Fatalf("tracking %d entries, limit %d", len(l.entries), l.maxEntries)
	}
	if _, ok := l.entries["198.51.100.1"]; ok {
		t.Fatal("least recently seen client was not evicted")
	}

	now = now.Add(time.Minute)
	l.fail("192.0.2.1")
	if n := len(l.entries); n != 1 {
		t.Fatalf("expired entries not dropped first: %d entries left", n)
	}
}

func TestFailureLimiterGlobalBudget(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := newFailureLimiter(3, 5*time.Minute)
	l.now = func() time.Time { return now }
	l.globalLimit = 5

	for i := range 5 {
		if err := l.blocked("fresh"); err != nil {
			t.Fatalf("blocked after %d global failures: %v", i, err)
		}
		l.fail(net.IPv4(10, 0, 0, byte(i)).String())
	}
	if err := l.blocked("fresh"); !errors.Is(err, errGlobalFailures) {
		t.Fatalf("blocked = %v with the global budget exhausted, want %v", err, errGlobalFailures)
	}
	now = now.Add(time.Minute)
	if err := l.blocked("fresh"); err != nil {
		t.Fatalf("still blocked after the global window: %v", err)
	}
}

func TestClientKey(t *testing.T) {
	tcp := func(ip string) net.Addr { return &net.TCPAddr{IP: net.ParseIP(ip), Port: 4000} }
	tests := []struct {
		addr net.Addr
		want string
	}{
		{tcp("192.0.2.1"), "192.0.2.1"},
		{tcp("::ffff:192.0.2.1"), "192.0.2.1"},
		{tcp("2001:db8:1:2::1"), "2001:db8:1:2::/64"},
		{tcp("2001:db8:1:2:ffff:ffff:ffff:ffff"), "2001:db8:1:2::/64"},
		{tcp("2001:db8:1:3::1"), "2001:db8:1:3::/64"},
		{&net.TCPAddr{IP: net.ParseIP("fe80::1"), Port: 1, Zone: "eth0"}, "fe80::/64"},
		{&net.UnixAddr{Name: "/tmp/sock", Net: "unix"}, "/tmp/sock"},
		{nil, ""},
	}
	for _, tt := range tests {
		if got := clientKey(tt.addr); got != tt.want {
			t.Errorf("clientKey(%v) = %q, want %q", tt.addr, got, tt.want)
		}
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
