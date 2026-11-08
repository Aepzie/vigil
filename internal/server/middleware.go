package server

import (
	"context"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	"charm.land/ssh"
	"charm.land/wish/v2"
)

// requirePTY rejects sessions without a terminal with a hint on how to
// connect. It replaces wish's activeterm middleware, whose message cannot
// be customized.
func requirePTY() wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sess ssh.Session) {
			if _, _, ok := sess.Pty(); ok {
				next(sess)
				return
			}
			wish.Errorln(sess, "vigil is an interactive dashboard and needs a terminal.")
			wish.Errorln(sess, "Connect with a TTY, e.g.: ssh -t "+sanitizedUser(sess)+"@<host> -p <port>")
			_ = sess.Exit(1)
		}
	}
}

// limitSessions allows at most limit concurrent sessions; further sessions
// are turned away with a message. limit <= 0 disables it.
func limitSessions(limit int) wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		if limit <= 0 {
			return next
		}
		slots := make(chan struct{}, limit)
		return func(sess ssh.Session) {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
				next(sess)
			default:
				wish.Errorf(sess, "vigil: the server is busy (%d sessions open). Please try again later.\r\n", limit)
				_ = sess.Exit(1)
			}
		}
	}
}

// sessionTracker counts running session handlers so shutdown can wait
// for them.
type sessionTracker struct {
	mu       sync.Mutex
	active   int
	draining bool
	idle     chan struct{}
}

// enter registers a session; it fails once draining started.
func (t *sessionTracker) enter() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.draining {
		return false
	}
	t.active++
	return true
}

// leave unregisters a session.
func (t *sessionTracker) leave() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active--
	if t.draining && t.active == 0 {
		close(t.idle)
	}
}

// drain refuses new sessions and returns a channel that is closed once no
// session is running.
func (t *sessionTracker) drain() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.draining {
		t.draining = true
		t.idle = make(chan struct{})
		if t.active == 0 {
			close(t.idle)
		}
	}
	return t.idle
}

// trackSessions is the outermost middleware: it registers every session
// with the tracker and turns sessions away during shutdown.
func (s *server) trackSessions() wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sess ssh.Session) {
			if !s.sessions.enter() {
				wish.Errorln(sess, "vigil is shutting down. Please reconnect in a moment.")
				_ = sess.Exit(1)
				return
			}
			defer s.sessions.leave()
			next(sess)
		}
	}
}

// Brute-force protection for password logins.
const (
	failurePenalty = 300 * time.Millisecond
	failureJitter  = 200 * time.Millisecond
	failureLimit   = 10
	failureWindow  = 5 * time.Minute
	maxTrackedIPs  = 4096
)

// penalize delays a failed password attempt by a jittered amount, or until
// ctx is done.
func penalize(ctx context.Context) {
	d := failurePenalty + rand.N(failureJitter)
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// failureLimiter counts failed password attempts per client IP in fixed
// windows. Once an IP reaches the limit, its password attempts are
// rejected without checking until the window ends. Public key logins are
// not affected.
type failureLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	now     func() time.Time
	entries map[string]failureEntry
}

type failureEntry struct {
	count int
	start time.Time
}

func newFailureLimiter(limit int, window time.Duration) *failureLimiter {
	return &failureLimiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		entries: make(map[string]failureEntry),
	}
}

// blocked reports whether ip exhausted its failures in the current window.
func (l *failureLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[ip]
	if !ok {
		return false
	}
	if l.now().Sub(e.start) >= l.window {
		delete(l.entries, ip)
		return false
	}
	return e.count >= l.limit
}

// fail records a failed attempt from ip.
func (l *failureLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[ip]
	if !ok || now.Sub(e.start) >= l.window {
		if len(l.entries) >= maxTrackedIPs {
			l.pruneLocked(now)
		}
		e = failureEntry{start: now}
	}
	e.count++
	l.entries[ip] = e
}

// pruneLocked drops expired entries, and if that is not enough, all of
// them, so memory stays bounded under a distributed attack.
func (l *failureLimiter) pruneLocked(now time.Time) {
	for ip, e := range l.entries {
		if now.Sub(e.start) >= l.window {
			delete(l.entries, ip)
		}
	}
	if len(l.entries) >= maxTrackedIPs {
		clear(l.entries)
	}
}

// remoteIP returns the IP of addr without the port.
func remoteIP(addr net.Addr) string {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.IP.String()
	}
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}
