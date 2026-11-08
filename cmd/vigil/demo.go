package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Aepzie/vigil/internal/alerts"
	"github.com/Aepzie/vigil/internal/auth"
	"github.com/Aepzie/vigil/internal/compat"
	"github.com/Aepzie/vigil/internal/demo"
	"github.com/Aepzie/vigil/internal/server"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui"
	"github.com/Aepzie/vigil/internal/tui/ui"
)

// demoPrime is how many samples per server are generated up front so
// charts are full immediately.
const demoPrime = 300

// demoMaxServers caps the shared demo inventory so sessions cannot grow it
// without bound.
const demoMaxServers = 50

// demoManagerKey is shown in Settings in demo mode instead of a real key.
const demoManagerKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDemoDemoDemoDemoDemoDemoDemoDemoDemoDemoDemo vigil-demo"

// demoFleet is a synthetic fleet in a temporary store.
type demoFleet struct {
	store  *store.Store
	engine *alerts.Engine
	source *demo.Source
	dir    string
}

// newDemoFleet seeds a temporary store and primes the generator.
func newDemoFleet(seed int64) (*demoFleet, error) {
	dir, err := os.MkdirTemp("", "vigil-demo-*")
	if err != nil {
		return nil, err
	}
	st, err := store.Open(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("demo store: %w", err)
	}
	if err := demo.SeedServers(st); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("demo servers: %w", err)
	}
	engine := alerts.NewEngine(thresholdsFrom(st.Settings()))
	src := demo.New(st, engine, seed)
	src.Prime(demoPrime)
	return &demoFleet{store: st, engine: engine, source: src, dir: dir}, nil
}

// Close removes the temporary store.
func (f *demoFleet) Close() error { return os.RemoveAll(f.dir) }

// deps returns UI dependencies without shells.
func (f *demoFleet) deps(user, listen, term string) *ui.Deps {
	return &ui.Deps{
		Theme:      theme.Default(),
		Store:      f.store,
		Source:     f.source,
		Alerts:     f.engine,
		PollNow:    f.source.PollNow,
		User:       user,
		Version:    versionString(),
		StartedAt:  time.Now(),
		ManagerKey: demoManagerKey,
		DataDir:    "(demo, in memory)",
		Listen:     listen,
		Term:       term,
		MaxServers: demoMaxServers,
	}
}

func cmdDemo(args []string, std stdio) error {
	fs := newFlagSet("demo", "demo [--serve [--listen ADDR] [--data DIR] [--public]]", std)
	serve := fs.Bool("serve", false, "serve the demo over SSH instead of running it here")
	listen := fs.String("listen", defaultListen, "SSH listen address (with --serve)")
	data := fs.String("data", "", "directory for the host key and logins (with --serve); "+dataDirHelp)
	public := fs.Bool("public", false, "let anyone log in without credentials (with --serve)")
	maxSessions := fs.Int("max-sessions", defaultMaxSessions, "maximum concurrent sessions (with --serve)")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return handleParseErr(err)
	}
	if err := expectArgs(fs, rest, 0); err != nil {
		return err
	}

	fleet, err := newDemoFleet(time.Now().UnixNano())
	if err != nil {
		return err
	}
	defer fleet.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srcCtx, cancelSrc := context.WithCancel(ctx)
	srcDone := make(chan struct{})
	go func() {
		defer close(srcDone)
		fleet.source.Run(srcCtx)
	}()
	defer func() {
		cancelSrc()
		<-srcDone
	}()

	if *serve {
		return serveDemo(ctx, fleet, *listen, *data, *public, *maxSessions, std)
	}
	p := tea.NewProgram(tui.New(fleet.deps(localUser(), "", os.Getenv("TERM")), tui.Options{
		Compat: compat.Parse(fleet.store.Settings().Compat),
		Env:    localEnv(),
	}), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("dashboard: %w", err)
	}
	return nil
}

// serveDemo serves the demo fleet over SSH. Without --public, logins are
// checked against the data directory like `vigil serve`.
func serveDemo(ctx context.Context, fleet *demoFleet, listen, data string, public bool, maxSessions int, std stdio) error {
	dataDir, err := resolveDataDir(data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("data directory: %w", err)
	}
	var authn *auth.Authenticator
	if !public {
		authn, err = auth.New(dataDir)
		if err != nil {
			return fmt.Errorf("auth: %w", err)
		}
		if !authn.HasCredentials() {
			return fmt.Errorf("no users in %s: create one with `vigil user add NAME` or use --public", dataDir)
		}
	} else {
		fmt.Fprintln(std.err, "vigil demo: public mode, anyone can log in (read-only, no shells, synthetic data only)")
	}
	return server.Run(ctx, server.Config{
		Addr:        listen,
		DataDir:     dataDir,
		Version:     versionString(),
		IdleTimeout: defaultIdleTimeout,
		MaxSessions: maxSessions,
		Public:      public,
	}, server.Deps{
		Theme:      theme.Default(),
		Store:      fleet.store,
		Alerts:     fleet.engine,
		Auth:       authn,
		Source:     fleet.source,
		PollNow:    fleet.source.PollNow,
		StartedAt:  time.Now(),
		MaxServers: demoMaxServers,
	})
}
