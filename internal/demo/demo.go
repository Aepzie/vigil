// Package demo provides a synthetic fleet with realistic, evolving metrics
// for `vigil demo`, documentation screenshots and UI tests.
package demo

import (
	"context"
	"sync"
	"time"

	"github.com/CraguS/vigil/internal/alerts"
	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/store"
)

const (
	// Step is the sampling interval of the generator.
	Step = time.Second
	// HistoryCapacity is the number of samples kept per server
	// (15 minutes at one sample per second).
	HistoryCapacity = 900
)

// Source implements collector.Source with generated data. It is safe for
// concurrent use.
type Source struct {
	st   *store.Store
	eng  *alerts.Engine
	seed uint64
	// now is the clock; nil means time.Now.
	now func() time.Time

	mu           sync.RWMutex
	nodes        map[string]*node
	order        []string
	storeVersion uint64
	synced       bool
}

// New creates a generator for the servers in st. eng may be nil; when set,
// every generated snapshot is evaluated for alerts. seed makes the output
// deterministic: two sources with the same seed, server names and sample
// times produce identical data.
func New(st *store.Store, eng *alerts.Engine, seed int64) *Source {
	s := &Source{st: st, eng: eng, seed: uint64(seed), nodes: make(map[string]*node)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncLocked()
	return s
}

// Prime synchronously generates n samples per server (at most
// HistoryCapacity) with timestamps one Step apart ending now, so charts
// and alerts are populated immediately (used by `vigil render`). Call it
// before Run.
func (s *Source) Prime(n int) {
	n = min(n, HistoryCapacity)
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncLocked()
	now := s.clock()
	for i := n - 1; i >= 0; i-- {
		s.generateLocked(now.Add(-time.Duration(i) * Step))
	}
}

// Run generates one sample per server per Step until ctx is done. If no
// sample exists yet, the first one is generated immediately.
func (s *Source) Run(ctx context.Context) {
	s.mu.Lock()
	if !s.hasSamplesLocked() {
		s.syncLocked()
		s.generateLocked(s.clock())
	}
	s.mu.Unlock()

	ticker := time.NewTicker(Step)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			s.syncLocked()
			s.generateLocked(s.clock())
			s.mu.Unlock()
		}
	}
}

// PollNow is a no-op hook matching ui.Deps.PollNow.
func (s *Source) PollNow(id string) {}

// Snapshot implements collector.Source.
func (s *Source) Snapshot(id string) (metrics.Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.nodes[id]
	if !ok || !n.hasLast {
		return metrics.Snapshot{}, false
	}
	return cloneSnapshot(n.last), true
}

// History implements collector.Source.
func (s *Source) History(id string) (metrics.Series, bool) {
	s.mu.RLock()
	n, ok := s.nodes[id]
	s.mu.RUnlock()
	if !ok {
		return metrics.Series{Step: Step}, false
	}
	return n.hist.Series(), true
}

func (s *Source) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Source) hasSamplesLocked() bool {
	for _, n := range s.nodes {
		if n.hasLast {
			return true
		}
	}
	return false
}

// syncLocked creates nodes for new servers, drops removed ones and
// follows renames. The caller holds s.mu for writing.
func (s *Source) syncLocked() {
	if s.st == nil {
		return
	}
	version := s.st.Version()
	if s.synced && version == s.storeVersion {
		return
	}
	servers := s.st.Servers()
	present := make(map[string]bool, len(servers))
	s.order = s.order[:0]
	for _, srv := range servers {
		present[srv.ID] = true
		s.order = append(s.order, srv.ID)
		if n, ok := s.nodes[srv.ID]; ok {
			n.srv = srv
			continue
		}
		s.nodes[srv.ID] = newNode(srv, s.seed, HistoryCapacity)
	}
	for id := range s.nodes {
		if present[id] {
			continue
		}
		delete(s.nodes, id)
		if s.eng != nil {
			s.eng.Forget(id)
		}
	}
	s.storeVersion, s.synced = version, true
}

// generateLocked appends one sample at time at for every server, in name
// order. The caller holds s.mu for writing.
func (s *Source) generateLocked(at time.Time) {
	for _, id := range s.order {
		n := s.nodes[id]
		snap := n.step(at)
		n.hist.Add(snap)
		n.last, n.hasLast = snap, true
		if s.eng != nil {
			s.eng.Evaluate(n.srv.Name, snap)
		}
	}
}
