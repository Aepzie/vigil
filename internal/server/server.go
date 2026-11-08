// Package server exposes the dashboard as an SSH application (wish).
package server

import (
	"context"
	"time"

	"github.com/CraguS/vigil/internal/alerts"
	"github.com/CraguS/vigil/internal/auth"
	"github.com/CraguS/vigil/internal/collector"
	"github.com/CraguS/vigil/internal/sshclient"
	"github.com/CraguS/vigil/internal/store"
	"github.com/CraguS/vigil/internal/theme"
)

// Config configures the SSH server.
type Config struct {
	// Addr is the listen address, e.g. ":2222".
	Addr string
	// DataDir holds the host key (<data>/host_ed25519, generated on first
	// start), users.json and authorized_keys.
	DataDir string
	Version string
	// IdleTimeout disconnects idle sessions; 0 disables.
	IdleTimeout time.Duration
	// MaxSessions limits concurrent sessions; 0 means unlimited.
	MaxSessions int
}

// Deps are the long-lived services shared by all sessions.
type Deps struct {
	Theme     *theme.Theme
	Store     *store.Store
	Hub       *collector.Hub
	Alerts    *alerts.Engine
	Dialer    *sshclient.Dialer
	Auth      *auth.Authenticator
	StartedAt time.Time
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func Run(ctx context.Context, cfg Config, d Deps) error {
	<-ctx.Done()
	return nil
}
