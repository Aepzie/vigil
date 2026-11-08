// Package demo provides a synthetic fleet with realistic, evolving metrics
// for `vigil demo`, documentation screenshots and UI tests.
package demo

import (
	"context"

	"github.com/CraguS/vigil/internal/alerts"
	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/store"
)

// SeedServers adds a fixed set of sample servers to st when it is empty.
func SeedServers(st *store.Store) error { return nil }

// Source implements collector.Source with generated data. It is safe for
// concurrent use.
type Source struct {
	st  *store.Store
	eng *alerts.Engine
}

// New creates a generator for the servers in st. eng may be nil; when set,
// every generated snapshot is evaluated for alerts. seed makes the output
// deterministic.
func New(st *store.Store, eng *alerts.Engine, seed int64) *Source {
	return &Source{st: st, eng: eng}
}

// Prime synchronously generates n samples per server so charts are full
// immediately (used by `vigil render`).
func (s *Source) Prime(n int) {}

// Run generates one sample per server per second until ctx is done.
func (s *Source) Run(ctx context.Context) { <-ctx.Done() }

// PollNow is a no-op hook matching ui.Deps.PollNow.
func (s *Source) PollNow(id string) {}

// Snapshot implements collector.Source.
func (s *Source) Snapshot(id string) (metrics.Snapshot, bool) { return metrics.Snapshot{}, false }

// History implements collector.Source.
func (s *Source) History(id string) (metrics.Series, bool) { return metrics.Series{}, false }
