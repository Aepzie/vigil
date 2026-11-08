package collector

import (
	"context"
	"errors"
	"io"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/sshclient"
	"github.com/CraguS/vigil/internal/sshclient/sshtest"
	"github.com/CraguS/vigil/internal/store"
)

// fakeInventory is an in-memory stand-in for *store.Store.
type fakeInventory struct {
	mu       sync.Mutex
	servers  []store.Server
	settings store.Settings
	version  uint64
}

func newInventory(settings store.Settings, servers ...store.Server) *fakeInventory {
	return &fakeInventory{servers: servers, settings: settings, version: 1}
}

func (f *fakeInventory) Servers() []store.Server {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.Server(nil), f.servers...)
}

func (f *fakeInventory) Settings() store.Settings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings
}

func (f *fakeInventory) Version() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version
}

func (f *fakeInventory) update(fn func(servers []store.Server, settings *store.Settings) []store.Server) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.servers = fn(f.servers, &f.settings)
	f.version++
}

var nonceRE = regexp.MustCompile(`(?m)^N=([a-z0-9]+)$`)

// fixtureExec answers poll scripts with poll1 first and poll2 afterwards.
func fixtureExec(t *testing.T) (sshtest.ExecFunc, *atomic.Int32) {
	var calls atomic.Int32
	return func(command string, stdin io.Reader, stdout io.Writer) uint32 {
		script, err := io.ReadAll(stdin)
		if err != nil || command != remoteCommand {
			return 1
		}
		m := nonceRE.FindSubmatch(script)
		if m == nil {
			return 2
		}
		poll := "poll2"
		if calls.Add(1) == 1 {
			poll = "poll1"
		}
		io.WriteString(stdout, fixtureOutput(t, poll, string(m[1])))
		return 0
	}, &calls
}

func serverOf(id string, s *sshtest.Server) store.Server {
	return store.Server{ID: id, Name: id, Host: s.Host(), Port: s.Port(), User: "monitor"}
}

// recorder collects hub callbacks.
type recorder struct {
	mu      sync.Mutex
	snaps   map[string][]metrics.Snapshot
	names   map[string]string
	removed []string
}

func newRecorder() *recorder {
	return &recorder{snaps: make(map[string][]metrics.Snapshot), names: make(map[string]string)}
}

func (r *recorder) onSnapshot(srv store.Server, snap metrics.Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snaps[srv.ID] = append(r.snaps[srv.ID], snap)
	r.names[srv.ID] = srv.Name
}

func (r *recorder) onRemove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, id)
}

func (r *recorder) last(id string) (metrics.Snapshot, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.snaps[id]
	if len(s) == 0 {
		return metrics.Snapshot{}, 0
	}
	return s[len(s)-1], len(s)
}

func (r *recorder) removedIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.removed...)
}

func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// startHub runs h until the test ends and fails the test if Run does not
// return promptly after cancellation.
func startHub(t *testing.T, h *Hub) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})
}

func testSettings() store.Settings {
	s := store.DefaultSettings()
	s.PollInterval = time.Second
	s.HistoryMinutes = 1
	return s
}

func newTestDialer(t *testing.T) *sshclient.Dialer {
	t.Helper()
	d, err := sshclient.NewDialer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestHubPollsOverSSH(t *testing.T) {
	exec, calls := fixtureExec(t)
	srv := sshtest.Start(t, sshtest.Options{Exec: exec})
	stranger := sshtest.NewSigner(t)
	locked := sshtest.Start(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{stranger.PublicKey()}})

	inv := newInventory(testSettings(), serverOf("web", srv), serverOf("locked", locked))
	rec := newRecorder()
	h := newHub(Options{OnSnapshot: rec.onSnapshot, OnRemove: rec.onRemove}, inv, newTestDialer(t).Dial)
	h.watchEvery = 20 * time.Millisecond
	startHub(t, h)

	eventually(t, 5*time.Second, "first online snapshot", func() bool {
		s, n := rec.last("web")
		return n >= 1 && s.State == metrics.StateOnline
	})
	h.PollNow("web")
	eventually(t, 5*time.Second, "second poll with CPU deltas", func() bool {
		s, _ := rec.last("web")
		return calls.Load() >= 2 && s.CPU.Total > 0
	})

	snap, ok := h.Snapshot("web")
	if !ok {
		t.Fatal("Snapshot(web) not found")
	}
	if snap.State != metrics.StateOnline || snap.Hostname != "web-01" || !approx(snap.CPU.Total, 45) ||
		snap.Latency < 0 || snap.LastSeen.IsZero() || len(snap.Procs) != topProcesses {
		t.Errorf("snapshot = %+v", snap)
	}
	snap.Disks[0].Mount = "mutated"
	snap.Procs[0].Command = "mutated"
	again, _ := h.Snapshot("web")
	if again.Disks[0].Mount == "mutated" || again.Procs[0].Command == "mutated" {
		t.Error("Snapshot returned shared slices")
	}

	series, ok := h.History("web")
	if !ok || series.Step != time.Second || len(series.CPU) < 2 {
		t.Errorf("history = %+v, %v", series, ok)
	}

	eventually(t, 5*time.Second, "locked server offline", func() bool {
		s, _ := rec.last("locked")
		return s.State == metrics.StateOffline
	})
	locked1, _ := h.Snapshot("locked")
	if locked1.Err != "auth failed" || !locked1.LastSeen.IsZero() {
		t.Errorf("locked snapshot = %+v", locked1)
	}

	if _, ok := h.Snapshot("nope"); ok {
		t.Error("unknown server reported")
	}
	if s, ok := h.History("nope"); ok || s.Step != time.Second {
		t.Errorf("History(nope) = %+v, %v", s, ok)
	}
	h.PollNow("nope")
}

func TestHubReconcilesInventory(t *testing.T) {
	exec, _ := fixtureExec(t)
	srv := sshtest.Start(t, sshtest.Options{Exec: exec})
	other := sshtest.Start(t, sshtest.Options{Exec: exec})

	inv := newInventory(testSettings(), serverOf("a", srv), serverOf("b", srv))
	rec := newRecorder()
	h := newHub(Options{OnSnapshot: rec.onSnapshot, OnRemove: rec.onRemove}, inv, newTestDialer(t).Dial)
	h.watchEvery = 20 * time.Millisecond
	startHub(t, h)

	for _, id := range []string{"a", "b"} {
		eventually(t, 5*time.Second, id+" online", func() bool {
			s, _ := h.Snapshot(id)
			return s.State == metrics.StateOnline
		})
	}

	inv.update(func(servers []store.Server, _ *store.Settings) []store.Server {
		return servers[:1]
	})
	eventually(t, 5*time.Second, "OnRemove(b)", func() bool {
		ids := rec.removedIDs()
		return len(ids) == 1 && ids[0] == "b"
	})
	if _, ok := h.Snapshot("b"); ok {
		t.Error("removed server still has a snapshot")
	}
	_, nb := rec.last("b")
	time.Sleep(1500 * time.Millisecond)
	if _, nb2 := rec.last("b"); nb2 != nb {
		t.Error("OnSnapshot called for a removed server")
	}

	inv.update(func(servers []store.Server, _ *store.Settings) []store.Server {
		servers[0].Name = "renamed"
		return servers
	})
	eventually(t, 5*time.Second, "rename propagated to OnSnapshot", func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.names["a"] == "renamed"
	})

	before := len(other.Execs())
	inv.update(func(servers []store.Server, _ *store.Settings) []store.Server {
		servers[0].Port = other.Port()
		return servers
	})
	eventually(t, 5*time.Second, "poller restarted against new port", func() bool {
		s, _ := h.Snapshot("a")
		return len(other.Execs()) > before && s.State == metrics.StateOnline
	})

	inv.update(func(servers []store.Server, set *store.Settings) []store.Server {
		set.PollInterval = 2 * time.Second
		set.HistoryMinutes = 2
		return servers
	})
	eventually(t, 5*time.Second, "history recreated with new step", func() bool {
		s, ok := h.History("a")
		return ok && s.Step == 2*time.Second
	})
	if len(rec.removedIDs()) != 1 {
		t.Errorf("restart must not call OnRemove: %v", rec.removedIDs())
	}
}

func TestHubSlowServerDoesNotBlockOthers(t *testing.T) {
	release := make(chan struct{})
	hung := sshtest.Start(t, sshtest.Options{Exec: func(_ string, stdin io.Reader, _ io.Writer) uint32 {
		io.Copy(io.Discard, stdin)
		<-release
		return 0
	}})
	t.Cleanup(func() { close(release) })
	exec, _ := fixtureExec(t)
	fast := sshtest.Start(t, sshtest.Options{Exec: exec})

	inv := newInventory(testSettings(), serverOf("hung", hung), serverOf("fast", fast))
	rec := newRecorder()
	h := newHub(Options{OnSnapshot: rec.onSnapshot}, inv, newTestDialer(t).Dial)
	startHub(t, h)

	eventually(t, 4*time.Second, "fast server polled repeatedly", func() bool {
		_, n := rec.last("fast")
		return n >= 3
	})
	if s, _ := h.Snapshot("hung"); s.State != metrics.StateConnecting {
		t.Errorf("hung server state = %v before its poll timeout", s.State)
	}
	eventually(t, 2*minPollTimeout, "hung server times out", func() bool {
		s, _ := h.Snapshot("hung")
		return s.State == metrics.StateOffline && s.Err == "timeout"
	})
}

func TestHubOfflineWithBackoff(t *testing.T) {
	var dials atomic.Int32
	refused := &sshclient.DialError{Addr: "x:22", Kind: sshclient.ErrRefused, Err: errors.New("connect: connection refused")}
	dial := func(ctx context.Context, srv store.Server) (*ssh.Client, error) {
		dials.Add(1)
		return nil, refused
	}
	inv := newInventory(testSettings(), store.Server{ID: "down", Host: "x", User: "u"})
	rec := newRecorder()
	h := newHub(Options{OnSnapshot: rec.onSnapshot}, inv, dial)
	startHub(t, h)

	eventually(t, 5*time.Second, "offline snapshots every interval", func() bool {
		_, n := rec.last("down")
		return n >= 3
	})
	s, n := rec.last("down")
	if s.State != metrics.StateOffline || s.Err != "connection refused" || !s.LastSeen.IsZero() {
		t.Errorf("snapshot = %+v", s)
	}
	if d := int(dials.Load()); d > n {
		t.Errorf("dials = %d for %d polls; backoff not applied", d, n)
	}
	series, _ := h.History("down")
	for _, v := range series.CPU {
		if v != 0 {
			t.Errorf("offline history must record zeros, got %v", series.CPU)
		}
	}

	before := dials.Load()
	h.PollNow("down")
	eventually(t, 2*time.Second, "PollNow forces a dial", func() bool { return dials.Load() > before })
}

func TestRunTwice(t *testing.T) {
	h := newHub(Options{}, newInventory(testSettings()), nil)
	startHub(t, h)
	eventually(t, time.Second, "hub running", h.running.Load)
	if err := h.Run(t.Context()); err == nil {
		t.Error("second Run succeeded")
	}
}

func TestNewValidatesOptions(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Error("New without store succeeded")
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Store: st}); err == nil {
		t.Error("New without dialer succeeded")
	}
	if _, err := New(Options{Store: st, Dialer: newTestDialer(t)}); err != nil {
		t.Errorf("New: %v", err)
	}
}

func TestPollInterval(t *testing.T) {
	tests := []struct{ in, want time.Duration }{
		{0, defaultInterval}, {-time.Second, defaultInterval}, {time.Millisecond, minInterval},
		{5 * time.Second, 5 * time.Second}, {48 * time.Hour, maxInterval},
	}
	for _, tt := range tests {
		if got := pollInterval(tt.in); got != tt.want {
			t.Errorf("pollInterval(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestHistoryCapacity(t *testing.T) {
	tests := []struct {
		minutes  int
		interval time.Duration
		want     int
	}{
		{15, 3 * time.Second, 300},
		{1, 5 * time.Second, minHistory},
		{0, 3 * time.Second, 300},
		{60, time.Second, 3600},
		{1 << 30, time.Second, maxHistory},
	}
	for _, tt := range tests {
		if got := historyCapacity(tt.minutes, tt.interval); got != tt.want {
			t.Errorf("historyCapacity(%d, %v) = %d, want %d", tt.minutes, tt.interval, got, tt.want)
		}
	}
}

func TestPollTimeout(t *testing.T) {
	h := newHub(Options{}, newInventory(testSettings()), nil)
	for _, tt := range []struct{ step, want time.Duration }{
		{time.Second, minPollTimeout}, {5 * time.Second, 10 * time.Second}, {time.Minute, maxPollTimeout},
	} {
		h.step = tt.step
		if got := h.pollTimeout(); got != tt.want {
			t.Errorf("pollTimeout(step %v) = %v, want %v", tt.step, got, tt.want)
		}
	}
}

func TestBackoff(t *testing.T) {
	b := newBackoff(time.Second, 30*time.Second)
	wantMax := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30}
	for i, m := range wantMax {
		m *= time.Second
		d := b.next()
		if d < m/2 || d > m {
			t.Errorf("attempt %d: %v not in [%v, %v]", i, d, m/2, m)
		}
	}
	for range 100 {
		b.next()
	}
	if d := b.next(); d > 30*time.Second {
		t.Errorf("backoff overflowed: %v", d)
	}
	b.reset()
	if d := b.next(); d > time.Second {
		t.Errorf("after reset: %v", d)
	}
}

func TestReason(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{errNoProc, "no /proc (not Linux?)"},
		{errIncomplete, "incomplete output"},
		{errOutputTooLarge, "output too large"},
		{context.DeadlineExceeded, "timeout"},
		{&sshclient.DialError{Kind: sshclient.ErrHostKeyMismatch, Err: sshclient.ErrHostKeyMismatch}, "host key mismatch!"},
	}
	for _, tt := range tests {
		if got := reason(tt.err); got != tt.want {
			t.Errorf("reason(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

func TestConnectionChanged(t *testing.T) {
	base := store.Server{ID: "a", Name: "n", Host: "h", Port: 22, User: "u", KeyPath: "k", Tags: []string{"x"}}
	same := base
	same.Name, same.Tags = "other", nil
	if connectionChanged(base, same) {
		t.Error("name/tag change must not restart")
	}
	for _, mut := range []func(*store.Server){
		func(s *store.Server) { s.Host = "h2" },
		func(s *store.Server) { s.Port = 2222 },
		func(s *store.Server) { s.User = "u2" },
		func(s *store.Server) { s.KeyPath = "" },
	} {
		changed := base
		mut(&changed)
		if !connectionChanged(base, changed) {
			t.Errorf("change %+v not detected", changed)
		}
	}
}
