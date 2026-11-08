package server

import (
	"container/list"
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"net/netip"
	"runtime"
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
	// globalFailureLimit failures from all clients together within
	// globalFailureWindow suspend password logins for everyone until the
	// window ends, which caps a distributed guessing attack.
	globalFailureLimit  = 200
	globalFailureWindow = time.Minute
)

// Reasons a password attempt is rejected without being checked.
var (
	errClientFailures = errors.New("too many failures from this client")
	errGlobalFailures = errors.New("too many failures from all clients")
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

// failureLimiter counts failed password attempts per client (see
// clientKey) in fixed windows. Once a client reaches the limit, its
// password attempts are rejected without checking until the window ends.
// On top of that, a global budget counts failures from all clients: once
// it is exhausted, password attempts from everyone are rejected until the
// global window ends. Public key logins are not affected.
//
// At most maxEntries clients are tracked; when full, expired entries are
// dropped first and then the least recently seen ones, so a flood of new
// addresses cannot wipe the counters of active clients.
type failureLimiter struct {
	mu           sync.Mutex
	limit        int
	window       time.Duration
	maxEntries   int
	globalLimit  int
	globalWindow time.Duration
	now          func() time.Time

	entries map[string]*list.Element
	// recent orders entries by last use, most recent first.
	recent      *list.List
	globalCount int
	globalStart time.Time
}

type failureEntry struct {
	key   string
	count int
	start time.Time
}

func newFailureLimiter(limit int, window time.Duration) *failureLimiter {
	return &failureLimiter{
		limit:        limit,
		window:       window,
		maxEntries:   maxTrackedIPs,
		globalLimit:  globalFailureLimit,
		globalWindow: globalFailureWindow,
		now:          time.Now,
		entries:      make(map[string]*list.Element),
		recent:       list.New(),
	}
}

// blocked returns a non-nil reason when password attempts from the
// client key must be rejected now.
func (l *failureLimiter) blocked(key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.globalStart) < l.globalWindow && l.globalCount >= l.globalLimit {
		return errGlobalFailures
	}
	el, ok := l.entries[key]
	if !ok {
		return nil
	}
	e := el.Value.(*failureEntry)
	if now.Sub(e.start) >= l.window {
		l.removeLocked(el)
		return nil
	}
	l.recent.MoveToFront(el)
	if e.count >= l.limit {
		return errClientFailures
	}
	return nil
}

// fail records a failed attempt from the client key.
func (l *failureLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.globalStart) >= l.globalWindow {
		l.globalStart, l.globalCount = now, 0
	}
	l.globalCount++

	if el, ok := l.entries[key]; ok {
		e := el.Value.(*failureEntry)
		if now.Sub(e.start) >= l.window {
			e.count, e.start = 0, now
		}
		e.count++
		l.recent.MoveToFront(el)
		return
	}
	if len(l.entries) >= l.maxEntries {
		l.evictLocked(now)
	}
	l.entries[key] = l.recent.PushFront(&failureEntry{key: key, count: 1, start: now})
}

// evictLocked makes room for one entry: it drops every expired entry and,
// if none expired, the least recently used one.
func (l *failureLimiter) evictLocked(now time.Time) {
	for el := l.recent.Back(); el != nil; {
		prev := el.Prev()
		if now.Sub(el.Value.(*failureEntry).start) >= l.window {
			l.removeLocked(el)
		}
		el = prev
	}
	for len(l.entries) >= l.maxEntries {
		l.removeLocked(l.recent.Back())
	}
}

func (l *failureLimiter) removeLocked(el *list.Element) {
	delete(l.entries, el.Value.(*failureEntry).key)
	l.recent.Remove(el)
}

// clientKey identifies the client behind addr for rate limiting: an IPv4
// address as is, an IPv6 address by its /64 prefix (one subscriber
// usually controls a whole /64, so per-address counting would give them
// unlimited attempts). Other addresses are used verbatim.
func clientKey(addr net.Addr) string {
	host := remoteIP(addr)
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	ip = ip.Unmap().WithZone("")
	if ip.Is4() {
		return ip.String()
	}
	prefix, err := ip.Prefix(64)
	if err != nil {
		return host
	}
	return prefix.String()
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

// Limits for work an unauthenticated client can cause.
const (
	// maxPreAuthPerClient and maxPreAuthTotal bound connections that have
	// not authenticated yet, per client (see clientKey) and overall.
	maxPreAuthPerClient = 8
	maxPreAuthTotal     = 256
)

// maxPasswordChecks bounds concurrent password hash checks; each takes
// tens of milliseconds of CPU at bcrypt cost 12.
var maxPasswordChecks = 2 * runtime.NumCPU()

// connLimiter bounds concurrent connections per client key and in total.
type connLimiter struct {
	mu        sync.Mutex
	perClient int
	total     int
	active    int
	byClient  map[string]int
}

func newConnLimiter(perClient, total int) *connLimiter {
	return &connLimiter{perClient: perClient, total: total, byClient: make(map[string]int)}
}

// acquire reserves a slot for key. It fails when key or the whole server
// is at its limit. release frees the slot; it may be called repeatedly.
func (l *connLimiter) acquire(key string) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active >= l.total || l.byClient[key] >= l.perClient {
		return nil, false
	}
	l.active++
	l.byClient[key]++
	var once sync.Once
	return func() { once.Do(func() { l.release(key) }) }, true
}

func (l *connLimiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active--
	l.byClient[key]--
	if l.byClient[key] <= 0 {
		delete(l.byClient, key)
	}
}

// releasingConn calls release once the connection is closed.
type releasingConn struct {
	net.Conn
	release func()
}

func (c *releasingConn) Close() error {
	err := c.Conn.Close()
	c.release()
	return err
}
