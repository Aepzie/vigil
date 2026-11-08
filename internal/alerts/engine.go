package alerts

import (
	"cmp"
	"slices"
	"sync"
	"time"

	"github.com/CraguS/vigil/internal/metrics"
)

const (
	// MaxEvents is how many events the log retains.
	MaxEvents = 500
	// MaxResolved is how many resolved alerts Recent can return.
	MaxResolved = 200
	// SpikeSamples is how many consecutive online samples CPU must stay at
	// or above a threshold before an alert fires or escalates.
	SpikeSamples = 3
	// Hysteresis is how many percentage points a value must drop below a
	// threshold before the alert de-escalates or resolves (capped at half
	// the threshold).
	Hysteresis = 5.0
)

// Engine is safe for concurrent use.
type Engine struct {
	mu         sync.RWMutex
	thresholds Thresholds
	seq        uint64

	// now is the clock used when a snapshot has no timestamp and for
	// Forget; nil means time.Now.
	now      func() time.Time
	servers  map[string]*serverState
	resolved []Alert
	events   [MaxEvents]Event
}

type alertKey struct {
	kind    Kind
	subject string
}

// serverState is the per-server tracking state.
type serverState struct {
	id           string
	name         string
	firstOffline time.Time
	cpuWarnRun   int
	cpuCritRun   int
	active       map[alertKey]*Alert
}

// NewEngine creates an engine with the given thresholds.
func NewEngine(th Thresholds) *Engine {
	return &Engine{thresholds: th, servers: make(map[string]*serverState)}
}

// SetThresholds replaces the thresholds; they apply on the next Evaluate.
func (e *Engine) SetThresholds(th Thresholds) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.thresholds = th
}

// Thresholds returns the current thresholds.
func (e *Engine) Thresholds() Thresholds {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.thresholds
}

// Evaluate checks one snapshot and updates alert state. serverName is
// used for messages; when it changes, active alerts of the server are
// renamed. snap.At is used as the current time (the engine clock when
// zero). Snapshots without a ServerID are ignored.
//
// Rules: an offline alert (crit) fires once the server has been offline
// for at least OfflineGrace and resolves when it is online again. CPU
// alerts need SpikeSamples consecutive online samples at or above a
// threshold to fire or escalate; memory and disk (per mount) fire
// immediately. Alerts de-escalate or resolve only after the value drops
// Hysteresis points below the threshold. While a server is offline or
// connecting its utilization alerts are left unchanged.
func (e *Engine) Evaluate(serverName string, snap metrics.Snapshot) {
	if snap.ServerID == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	now := snap.At
	if now.IsZero() {
		now = e.clock()
	}
	ss := e.serverState(snap.ServerID, serverName)
	switch snap.State {
	case metrics.StateOffline:
		e.evalOffline(ss, snap, now)
	case metrics.StateOnline:
		e.evalOnline(ss, snap, now)
	}
}

// Forget drops all alerts of a removed server (resolving them and
// emitting resolved events) and its tracking state.
func (e *Engine) Forget(serverID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ss, ok := e.servers[serverID]
	if !ok {
		return
	}
	now := e.clock()
	keys := make([]alertKey, 0, len(ss.active))
	for k := range ss.active {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b alertKey) int {
		return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.subject, b.subject))
	})
	for _, k := range keys {
		e.resolve(ss, k, now)
	}
	delete(e.servers, serverID)
}

// Active returns unresolved alerts, most severe and newest first.
func (e *Engine) Active() []Alert {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []Alert
	for _, ss := range e.servers {
		for _, a := range ss.active {
			out = append(out, *a)
		}
	}
	slices.SortFunc(out, func(a, b Alert) int {
		return cmp.Or(
			cmp.Compare(b.Severity, a.Severity),
			b.Since.Compare(a.Since),
			cmp.Compare(a.ServerName, b.ServerName),
			cmp.Compare(a.ID, b.ID),
		)
	})
	return out
}

// Recent returns up to n alerts including the last MaxResolved resolved
// ones, newest first by ResolvedAt (resolved) or Since (active).
func (e *Engine) Recent(n int) []Alert {
	if n <= 0 {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := slices.Clone(e.resolved)
	for _, ss := range e.servers {
		for _, a := range ss.active {
			out = append(out, *a)
		}
	}
	slices.SortStableFunc(out, func(a, b Alert) int {
		return cmp.Or(
			recencyOf(b).Compare(recencyOf(a)),
			cmp.Compare(a.ID, b.ID),
		)
	})
	return out[:min(n, len(out))]
}

// Ack marks an active or recently resolved alert as acknowledged. Unknown
// IDs are ignored. An acknowledged alert that escalates becomes
// unacknowledged again.
func (e *Engine) Ack(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ss := range e.servers {
		for _, a := range ss.active {
			if a.ID == id {
				a.Acked = true
				return
			}
		}
	}
	for i := range e.resolved {
		if e.resolved[i].ID == id {
			e.resolved[i].Acked = true
			return
		}
	}
}

// EventsSince returns events with Seq > seq, oldest first, and the latest
// sequence number. Callers start with seq = LatestSeq() to skip history.
// When seq is older than the retained log, the retained events are
// returned.
func (e *Engine) EventsSince(seq uint64) ([]Event, uint64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if seq >= e.seq {
		return nil, e.seq
	}
	oldest := uint64(1)
	if e.seq > MaxEvents {
		oldest = e.seq - MaxEvents + 1
	}
	start := max(seq+1, oldest)
	out := make([]Event, 0, e.seq-start+1)
	for s := start; s <= e.seq; s++ {
		out = append(out, e.events[(s-1)%MaxEvents])
	}
	return out, e.seq
}

// LatestSeq returns the current sequence number.
func (e *Engine) LatestSeq() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.seq
}

func (e *Engine) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

// serverState returns the state for id, creating it if needed, and
// applies a server rename to its active alerts.
func (e *Engine) serverState(id, name string) *serverState {
	if e.servers == nil {
		e.servers = make(map[string]*serverState)
	}
	ss, ok := e.servers[id]
	if !ok {
		ss = &serverState{id: id, name: cmp.Or(name, id), active: make(map[alertKey]*Alert)}
		e.servers[id] = ss
		return ss
	}
	if name != "" && name != ss.name {
		ss.name = name
		for _, a := range ss.active {
			a.ServerName = name
		}
	}
	return ss
}

func (e *Engine) emit(typ EventType, a Alert, at time.Time) {
	e.seq++
	e.events[(e.seq-1)%MaxEvents] = Event{Seq: e.seq, Type: typ, Alert: a, At: at}
}

func recencyOf(a Alert) time.Time {
	if a.Resolved {
		return a.ResolvedAt
	}
	return a.Since
}
