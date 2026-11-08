package collector

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Aepzie/vigil/internal/metrics"
	"github.com/Aepzie/vigil/internal/sshclient"
	"github.com/Aepzie/vigil/internal/store"
)

// Timing and size limits.
const (
	defaultInterval   = 3 * time.Second
	minInterval       = time.Second
	maxInterval       = time.Hour
	defaultHistoryMin = 15
	minHistory        = 30
	maxHistory        = 24 * 60 * 60
	minPollTimeout    = 5 * time.Second
	maxPollTimeout    = 15 * time.Second
	dialTimeout       = sshclient.DefaultDialTimeout
	versionCheckEvery = time.Second
	backoffMin        = time.Second
	backoffMax        = 30 * time.Second
)

// Source is the read side consumed by the TUI.
type Source interface {
	// Snapshot returns the latest snapshot of a server.
	Snapshot(id string) (metrics.Snapshot, bool)
	// History returns a copy of the server's history.
	History(id string) (metrics.Series, bool)
}

// Options configure a Hub.
type Options struct {
	// Store provides the server list and settings (poll interval,
	// history length). The hub re-syncs when Store.Version changes.
	Store *store.Store
	// Dialer opens authenticated SSH connections to servers.
	Dialer *sshclient.Dialer
	// OnSnapshot is called after every poll (e.g. alerts evaluation).
	// It runs on the server's poller goroutine, outside all hub locks,
	// and receives its own copy of the snapshot.
	OnSnapshot func(srv store.Server, snap metrics.Snapshot)
	// OnRemove is called when a server is removed from the store, after
	// its poller has stopped (no OnSnapshot for it follows).
	OnRemove func(id string)
}

// inventory is the subset of *store.Store the hub uses.
type inventory interface {
	Servers() []store.Server
	Settings() store.Settings
	Version() uint64
}

// dialFunc opens an authenticated SSH connection.
type dialFunc func(ctx context.Context, srv store.Server) (*ssh.Client, error)

// Hub manages one poller per server. It is safe for concurrent use.
type Hub struct {
	opts       Options
	inv        inventory
	dial       dialFunc
	watchEvery time.Duration

	running atomic.Bool
	wg      sync.WaitGroup

	mu       sync.RWMutex
	entries  map[string]*entry
	step     time.Duration
	capacity int
	// wake is closed (and replaced) when the poll interval changes so
	// waiting pollers reschedule immediately.
	wake chan struct{}
}

// entry is the published state of one server. Fields are guarded by
// Hub.mu; poller is fixed at creation.
type entry struct {
	srv    store.Server
	snap   metrics.Snapshot
	hist   *metrics.History
	poller *poller
}

// New creates a hub; call Run to start polling.
func New(opts Options) (*Hub, error) {
	if opts.Store == nil {
		return nil, errors.New("collector: Options.Store is required")
	}
	if opts.Dialer == nil {
		return nil, errors.New("collector: Options.Dialer is required")
	}
	return newHub(opts, opts.Store, opts.Dialer.Dial), nil
}

func newHub(opts Options, inv inventory, dial dialFunc) *Hub {
	return &Hub{
		opts:       opts,
		inv:        inv,
		dial:       dial,
		watchEvery: versionCheckEvery,
		entries:    make(map[string]*entry),
		step:       defaultInterval,
		capacity:   historyCapacity(defaultHistoryMin, defaultInterval),
		wake:       make(chan struct{}),
	}
}

// Run polls until ctx is cancelled. It starts a poller per server, then
// checks Store.Version every second and adds, removes or restarts
// pollers as the inventory and settings change. On return all pollers
// have stopped and their connections are closed. Run may be called once.
func (h *Hub) Run(ctx context.Context) error {
	if !h.running.CompareAndSwap(false, true) {
		return errors.New("collector: Run called more than once")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer h.wg.Wait()
	defer cancel()

	version := h.inv.Version()
	h.sync(ctx)
	ticker := time.NewTicker(h.watchEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if v := h.inv.Version(); v != version {
				version = v
				h.sync(ctx)
			}
		}
	}
}

// sync reconciles pollers and histories with the inventory.
func (h *Hub) sync(ctx context.Context) {
	settings := h.inv.Settings()
	interval := pollInterval(settings.PollInterval)
	capacity := historyCapacity(settings.HistoryMinutes, interval)
	desired := make(map[string]store.Server)
	for _, srv := range h.inv.Servers() {
		if _, dup := desired[srv.ID]; srv.ID == "" || dup {
			continue
		}
		desired[srv.ID] = srv
	}

	var removed, started []*entry
	h.mu.Lock()
	if interval != h.step || capacity != h.capacity {
		if interval != h.step {
			close(h.wake)
			h.wake = make(chan struct{})
		}
		h.step, h.capacity = interval, capacity
		for _, e := range h.entries {
			e.hist = metrics.NewHistory(capacity, interval)
		}
	}
	for id, e := range h.entries {
		srv, ok := desired[id]
		switch {
		case !ok:
			delete(h.entries, id)
			removed = append(removed, e)
		case connectionChanged(e.srv, srv):
			e.poller.cancel()
			ne := h.newEntryLocked(ctx, srv, e.poller.done)
			h.entries[id] = ne
			started = append(started, ne)
		default:
			e.srv = srv
		}
	}
	for id, srv := range desired {
		if _, ok := h.entries[id]; ok {
			continue
		}
		ne := h.newEntryLocked(ctx, srv, nil)
		h.entries[id] = ne
		started = append(started, ne)
	}
	h.mu.Unlock()

	for _, e := range removed {
		e.poller.cancel()
		h.wg.Go(func() {
			<-e.poller.done
			if h.opts.OnRemove != nil {
				h.opts.OnRemove(e.poller.id)
			}
		})
	}
	for _, e := range started {
		h.wg.Go(e.poller.run)
	}
}

// newEntryLocked creates the state and (not yet running) poller for srv.
// When after is non-nil the poller waits for it to close first, so two
// pollers of one server never overlap.
func (h *Hub) newEntryLocked(ctx context.Context, srv store.Server, after <-chan struct{}) *entry {
	e := &entry{
		srv:  srv,
		snap: metrics.Snapshot{ServerID: srv.ID, State: metrics.StateConnecting},
		hist: metrics.NewHistory(h.capacity, h.step),
	}
	e.poller = newPoller(ctx, h, e, srv, after)
	return e
}

func connectionChanged(a, b store.Server) bool {
	return a.Host != b.Host || a.Port != b.Port || a.User != b.User || a.KeyPath != b.KeyPath
}

// publish stores snap as the latest state of e and appends it to the
// history, unless e has been removed or replaced meanwhile. OnSnapshot
// is called outside the lock.
func (h *Hub) publish(e *entry, snap metrics.Snapshot) {
	h.mu.Lock()
	if h.entries[e.poller.id] != e {
		h.mu.Unlock()
		return
	}
	e.snap = snap
	e.hist.Add(snap)
	srv := e.srv
	h.mu.Unlock()
	if h.opts.OnSnapshot != nil {
		h.opts.OnSnapshot(srv, cloneSnapshot(snap))
	}
}

// timing returns the poll interval and the channel closed when it next
// changes.
func (h *Hub) timing() (time.Duration, <-chan struct{}) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.step, h.wake
}

// pollTimeout bounds one poll: twice the interval, within 5s..15s.
func (h *Hub) pollTimeout() time.Duration {
	step, _ := h.timing()
	return min(max(2*step, minPollTimeout), maxPollTimeout)
}

// Snapshot implements Source. Known servers that have not been polled
// yet report StateConnecting.
func (h *Hub) Snapshot(id string) (metrics.Snapshot, bool) {
	h.mu.RLock()
	e, ok := h.entries[id]
	var snap metrics.Snapshot
	if ok {
		snap = e.snap
	}
	h.mu.RUnlock()
	if !ok {
		return metrics.Snapshot{}, false
	}
	return cloneSnapshot(snap), true
}

// History implements Source.
func (h *Hub) History(id string) (metrics.Series, bool) {
	h.mu.RLock()
	e, ok := h.entries[id]
	var hist *metrics.History
	if ok {
		hist = e.hist
	}
	step := h.step
	h.mu.RUnlock()
	if !ok {
		return metrics.Series{Step: step}, false
	}
	return hist.Series(), true
}

// PollNow triggers an immediate poll of one server (e.g. after editing).
// It never blocks; a reconnect backoff in progress is skipped.
func (h *Hub) PollNow(id string) {
	h.mu.RLock()
	e, ok := h.entries[id]
	h.mu.RUnlock()
	if ok {
		e.poller.poke()
	}
}

// pollInterval normalizes the configured interval.
func pollInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultInterval
	}
	return min(max(d, minInterval), maxInterval)
}

// historyCapacity is the number of samples covering minutes at interval,
// at least minHistory.
func historyCapacity(minutes int, interval time.Duration) int {
	if minutes <= 0 {
		minutes = defaultHistoryMin
	}
	minutes = min(minutes, maxHistory)
	samples := int64(time.Duration(minutes) * time.Minute / interval)
	return int(min(max(samples, minHistory), maxHistory))
}

func cloneSnapshot(s metrics.Snapshot) metrics.Snapshot {
	s.Disks = slices.Clone(s.Disks)
	s.Procs = slices.Clone(s.Procs)
	s.CPU.Cores = slices.Clone(s.CPU.Cores)
	return s
}
