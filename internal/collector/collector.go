// Package collector polls every managed server over SSH (reading /proc,
// no agent on the target) and keeps the latest snapshot and history per
// server.
package collector

import (
	"context"
	"time"

	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/sshclient"
	"github.com/CraguS/vigil/internal/store"
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
	OnSnapshot func(srv store.Server, snap metrics.Snapshot)
	// OnRemove is called when a server is removed from the store.
	OnRemove func(id string)
}

// Hub manages one poller per server. It is safe for concurrent use.
type Hub struct {
	opts Options
}

// New creates a hub; call Run to start polling.
func New(opts Options) (*Hub, error) {
	return &Hub{opts: opts}, nil
}

// Run polls until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Snapshot implements Source.
func (h *Hub) Snapshot(id string) (metrics.Snapshot, bool) {
	return metrics.Snapshot{}, false
}

// History implements Source.
func (h *Hub) History(id string) (metrics.Series, bool) {
	return metrics.Series{Step: time.Second}, false
}

// PollNow triggers an immediate poll of one server (e.g. after editing).
func (h *Hub) PollNow(id string) {}
