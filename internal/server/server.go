// Package server exposes the dashboard as an SSH application (wish).
//
// Every session gets its own Bubble Tea program running the TUI. Instead
// of wish's stock bubbletea middleware, sessions are driven by a custom
// handler that keeps resizing working while a remote shell runs through
// tea.Exec (when the program's event loop is blocked): window changes are
// applied to the active shell directly and forwarded to the program
// without ever blocking the reader.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/log/v2"
	"charm.land/ssh"
	"charm.land/wish/v2"
	"charm.land/wish/v2/bubbletea"
	"charm.land/wish/v2/logging"
	"charm.land/wish/v2/ratelimiter"
	wishrecover "charm.land/wish/v2/recover"
	"github.com/charmbracelet/colorprofile"
	gossh "golang.org/x/crypto/ssh"

	"github.com/CraguS/vigil/internal/alerts"
	"github.com/CraguS/vigil/internal/auth"
	"github.com/CraguS/vigil/internal/collector"
	"github.com/CraguS/vigil/internal/compat"
	"github.com/CraguS/vigil/internal/sanitize"
	"github.com/CraguS/vigil/internal/sshclient"
	"github.com/CraguS/vigil/internal/store"
	"github.com/CraguS/vigil/internal/theme"
	"github.com/CraguS/vigil/internal/tui"
	"github.com/CraguS/vigil/internal/tui/ui"
)

// HostKeyFile is the name of the server host key inside the data
// directory.
const HostKeyFile = "host_ed25519"

// ShutdownTimeout is how long Run waits for sessions to end after ctx is
// cancelled before closing the remaining connections.
const ShutdownTimeout = 5 * time.Second

// Rate limit for new sessions per client IP.
const (
	sessionRate  = 1
	sessionBurst = 10
	rateEntries  = 1024
)

// Config configures the SSH server.
type Config struct {
	// Addr is the listen address, e.g. ":2222".
	Addr string
	// DataDir holds the host key (<data>/host_ed25519, generated on first
	// start), users.json and authorized_keys.
	DataDir string
	Version string
	// IdleTimeout disconnects sessions whose client sent no input or
	// window change for this long (output does not count, the dashboard
	// redraws continuously), and connections without any traffic; 0
	// disables.
	IdleTimeout time.Duration
	// MaxTimeout is the absolute connection lifetime; 0 disables.
	MaxTimeout time.Duration
	// MaxSessions limits concurrent sessions; 0 means unlimited.
	MaxSessions int
	// Logger receives connection and error logs; nil logs to stderr.
	Logger *log.Logger
	// Public lets anyone log in without credentials. It exists for
	// `vigil demo --serve --public` and is refused unless Deps.Dialer is
	// nil, so no remote shell can be reached through a public server.
	// Public sessions are read-only (ui.Deps.ReadOnly).
	Public bool
}

// Deps are the long-lived services shared by all sessions.
type Deps struct {
	Theme  *theme.Theme
	Store  *store.Store
	Hub    *collector.Hub
	Alerts *alerts.Engine
	// Dialer opens remote shells; nil disables shells (demo mode).
	Dialer    *sshclient.Dialer
	Auth      *auth.Authenticator
	StartedAt time.Time
	// Source, when non-nil, replaces Hub as the metrics source (demo mode).
	Source collector.Source
	// PollNow, when non-nil, replaces Hub.PollNow.
	PollNow func(id string)
	// MaxServers caps how many servers sessions can add (demo mode); 0
	// means no limit.
	MaxServers int
}

// NewLogger returns the default server logger writing to stderr.
func NewLogger() *log.Logger {
	return log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: true,
		TimeFormat:      time.DateTime,
		Prefix:          "vigil",
	})
}

// Run listens on cfg.Addr and serves until ctx is cancelled, then shuts
// down gracefully.
func Run(ctx context.Context, cfg Config, d Deps) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return Serve(ctx, ln, cfg, d)
}

// Serve is like Run but accepts connections on ln, which it closes.
func Serve(ctx context.Context, ln net.Listener, cfg Config, d Deps) error {
	defer ln.Close()
	if d.Store == nil || d.Alerts == nil {
		return errors.New("server: Store and Alerts are required")
	}
	if cfg.Public && d.Dialer != nil {
		return errors.New("server: a public server must not have a Dialer")
	}
	if !cfg.Public && d.Auth == nil {
		return errors.New("server: Auth is required")
	}
	if d.Hub == nil && d.Source == nil {
		return errors.New("server: Hub or Source is required")
	}
	if d.Theme == nil {
		d.Theme = theme.Default()
	}
	if cfg.Logger == nil {
		cfg.Logger = NewLogger()
	}
	if cfg.Addr == "" {
		cfg.Addr = ln.Addr().String()
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}

	s := &server{
		ctx:           ctx,
		cfg:           cfg,
		deps:          d,
		log:           cfg.Logger,
		failures:      newFailureLimiter(failureLimit, failureWindow),
		preAuth:       newConnLimiter(maxPreAuthPerClient, maxPreAuthTotal),
		passwordSlots: make(chan struct{}, maxPasswordChecks),
	}
	srv, err := s.build()
	if err != nil {
		return err
	}
	if err := secureHostKey(filepath.Join(cfg.DataDir, HostKeyFile), s.log); err != nil {
		return err
	}

	s.log.Info("listening", "addr", ln.Addr().String())
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		if errors.Is(err, ssh.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	// Sessions see ctx and end their programs; wait for them, then close
	// the connections that are left without sessions.
	s.log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	go func() { _ = srv.Shutdown(shutdownCtx) }()
	select {
	case <-s.sessions.drain():
	case <-shutdownCtx.Done():
		s.log.Warn("sessions did not end in time; closing connections")
	}
	_ = srv.Close()
	if err := <-errc; err != nil && !errors.Is(err, ssh.ErrServerClosed) {
		return err
	}
	return nil
}

// secureHostKey makes sure the private host key is only readable by its
// owner. Windows does not use Unix permission bits, so it is skipped
// there.
func secureHostKey(path string, logger *log.Logger) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("host key: %w", err)
	}
	if runtime.GOOS == "windows" || fi.Mode().Perm()&0o077 == 0 {
		return nil
	}
	logger.Warn("tightening host key permissions", "path", path, "mode", fi.Mode().Perm())
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("host key: %w", err)
	}
	return nil
}

type server struct {
	// ctx is cancelled on shutdown; sessions end their programs then.
	ctx      context.Context
	cfg      Config
	deps     Deps
	log      *log.Logger
	failures *failureLimiter
	// preAuth counts connections that have not authenticated yet.
	preAuth *connLimiter
	// passwordSlots is a semaphore bounding concurrent password checks.
	passwordSlots chan struct{}
	sessions      sessionTracker
}

func (s *server) build() (*ssh.Server, error) {
	authOpts := []ssh.Option{
		wish.WithPasswordAuth(s.checkPassword),
		wish.WithPublicKeyAuth(s.checkKey),
	}
	if s.cfg.Public {
		authOpts = []ssh.Option{
			wish.WithPublicKeyAuth(func(ssh.Context, ssh.PublicKey) bool { return true }),
			wish.WithKeyboardInteractiveAuth(func(ssh.Context, gossh.KeyboardInteractiveChallenge) bool { return true }),
		}
	}
	srv, err := wish.NewServer(append(authOpts,
		s.limitPreAuth(),
		wish.WithHostKeyPath(filepath.Join(s.cfg.DataDir, HostKeyFile)),
		wish.WithIdleTimeout(s.cfg.IdleTimeout),
		wish.WithMaxTimeout(s.cfg.MaxTimeout),
		wish.WithMiddleware(
			wishrecover.MiddlewareWithLogger(s.log,
				s.tuiMiddleware(),
				limitSessions(s.cfg.MaxSessions),
				requirePTY(),
				ratelimiter.Middleware(ratelimiter.NewRateLimiter(sessionRate, sessionBurst, rateEntries)),
				logging.StructuredMiddlewareWithLogger(s.log, log.InfoLevel),
				s.trackSessions(),
			),
		),
	)...)
	if err != nil {
		return nil, fmt.Errorf("ssh server: %w", err)
	}
	return srv, nil
}

// limitPreAuth installs the pre-authentication connection limits: a
// connection holds a slot of s.preAuth from accept until it authenticates
// or closes, and is closed right away when no slot is free.
func (s *server) limitPreAuth() ssh.Option {
	return func(srv *ssh.Server) error {
		srv.ConnCallback = s.admitConn
		srv.ServerConfigCallback = s.serverConfig
		return nil
	}
}

// preAuthReleaseKey is the ssh.Context key of the function that frees a
// connection's pre-authentication slot.
type preAuthReleaseKey struct{}

// admitConn is the ConnCallback: it reserves a pre-authentication slot
// for the client and frees it when the connection closes. Returning nil
// makes the ssh server close the connection.
func (s *server) admitConn(ctx ssh.Context, conn net.Conn) net.Conn {
	release, ok := s.preAuth.acquire(clientKey(conn.RemoteAddr()))
	if !ok {
		s.log.Warn("connection refused: too many unauthenticated connections", "remote-addr", remoteIP(conn.RemoteAddr()))
		return nil
	}
	ctx.SetValue(preAuthReleaseKey{}, release)
	return &releasingConn{Conn: conn, release: release}
}

// serverConfig frees the pre-authentication slot of a connection as soon
// as it authenticated successfully.
func (s *server) serverConfig(ctx ssh.Context) *gossh.ServerConfig {
	return &gossh.ServerConfig{
		AuthLogCallback: func(_ gossh.ConnMetadata, _ string, err error) {
			if err != nil {
				return
			}
			if release, ok := ctx.Value(preAuthReleaseKey{}).(func()); ok {
				release()
			}
		},
	}
}

func (s *server) checkPassword(ctx ssh.Context, password string) bool {
	return s.passwordLogin(ctx, ctx.User(), ctx.RemoteAddr(), password)
}

// passwordLogin checks a password attempt of user from addr, applying the
// failure limits, the bound on concurrent checks and the failure penalty.
func (s *server) passwordLogin(ctx context.Context, user string, addr net.Addr, password string) bool {
	ip := remoteIP(addr)
	key := clientKey(addr)
	if err := s.failures.blocked(key); err != nil {
		s.log.Warn("password attempt rejected: "+err.Error(), "user", user, "remote-addr", ip)
		penalize(ctx)
		return false
	}
	select {
	case s.passwordSlots <- struct{}{}:
	default:
		s.log.Warn("password attempt rejected: too many concurrent checks", "user", user, "remote-addr", ip)
		penalize(ctx)
		return false
	}
	ok := s.deps.Auth.CheckPassword(user, password)
	<-s.passwordSlots
	if ok {
		return true
	}
	s.failures.fail(key)
	s.log.Warn("password authentication failed", "user", user, "remote-addr", ip)
	penalize(ctx)
	return false
}

func (s *server) checkKey(ctx ssh.Context, key ssh.PublicKey) bool {
	return s.deps.Auth.CheckKey(ctx.User(), key)
}

// tuiMiddleware runs the dashboard for a PTY session.
func (s *server) tuiMiddleware() wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sess ssh.Session) {
			s.serveTUI(sess)
			next(sess)
		}
	}
}

func (s *server) serveTUI(sess ssh.Session) {
	pty, winch, ok := sess.Pty()
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(sess.Context())
	defer cancel()
	stopOnShutdown := context.AfterFunc(s.ctx, cancel)
	defer stopOnShutdown()

	st := newSession(pty.Window.Width, pty.Window.Height, rawOutput(sess))
	go st.readInput(sess)
	context.AfterFunc(ctx, st.in.close)

	environ := append(sess.Environ(), "TERM="+pty.Term)
	model := tui.New(s.uiDeps(sess, pty.Term, st), tui.Options{
		Compat: compat.Parse(s.deps.Store.Settings().Compat),
		Env:    clientEnv(environ),
	})
	opts := append(bubbletea.MakeOptions(sess),
		tea.WithInput(st.in.program()),
		tea.WithContext(ctx),
		tea.WithEnvironment(environ),
		tea.WithColorProfile(colorprofile.Env(environ)),
		tea.WithWindowSize(pty.Window.Width, pty.Window.Height),
	)
	p := tea.NewProgram(model, opts...)

	var wg sync.WaitGroup
	wg.Go(func() {
		defer s.recoverSession(cancel)
		st.watchWindow(ctx, winch)
	})
	wg.Go(func() {
		defer s.recoverSession(cancel)
		st.forwardSizes(ctx, p.Send)
	})
	var idle atomic.Bool
	wg.Go(func() {
		defer s.recoverSession(cancel)
		st.watchIdle(ctx, s.cfg.IdleTimeout, func() {
			idle.Store(true)
			cancel()
		})
	})

	_, err := p.Run()
	p.Kill()
	cancel()
	wg.Wait()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) && !errors.Is(err, context.Canceled) {
		s.log.Error("session ended with error", "user", sess.User(), "err", err)
	}
	switch {
	case s.ctx.Err() != nil:
		wish.Println(sess, "\nvigil is shutting down. Please reconnect in a moment.")
	case idle.Load():
		s.log.Info("idle session closed", "user", sess.User(), "remote-addr", sess.RemoteAddr().String())
		wish.Printf(sess, "\nvigil: disconnected after %s without input.\n", s.cfg.IdleTimeout)
	}
}

// recoverSession logs a panic in a session goroutine (which the recover
// middleware cannot see) and ends the session instead of the process.
func (s *server) recoverSession(cancel context.CancelFunc) {
	if r := recover(); r != nil {
		s.log.Error("panic in session", "panic", r, "stack", string(debug.Stack()))
		cancel()
	}
}

func (s *server) uiDeps(sess ssh.Session, term string, st *session) *ui.Deps {
	d := &ui.Deps{
		Theme:     s.deps.Theme,
		Store:     s.deps.Store,
		Source:    s.deps.Source,
		Alerts:    s.deps.Alerts,
		PollNow:   s.deps.PollNow,
		User:      sanitizedUser(sess),
		Version:   s.cfg.Version,
		StartedAt: s.deps.StartedAt,
		DataDir:   s.cfg.DataDir,
		Listen:    s.cfg.Addr,
		Term:      sanitize.Line(term),
		// Anonymous viewers of a public server share one store.
		ReadOnly:   s.cfg.Public,
		MaxServers: s.deps.MaxServers,
	}
	if d.Source == nil {
		d.Source = s.deps.Hub
	}
	if d.PollNow == nil && s.deps.Hub != nil {
		d.PollNow = s.deps.Hub.PollNow
	}
	if d.PollNow == nil {
		d.PollNow = func(string) {}
	}
	if dialer := s.deps.Dialer; dialer != nil {
		d.ManagerKey = dialer.PublicKey()
		d.Shell = st.shellFunc(func(srv store.Server, width, height int) remoteShell {
			return dialer.NewShell(srv, d.Term, width, height)
		})
	}
	return d
}

func sanitizedUser(sess ssh.Session) string {
	return sanitize.Line(sess.User())
}

// clientEnv extracts the variables relevant for compatibility mode from an
// environment list; later entries win.
func clientEnv(environ []string) compat.Env {
	var env compat.Env
	for _, kv := range environ {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		value = sanitize.Line(value)
		switch key {
		case "TERM":
			env.Term = value
		case "COLORTERM":
			env.ColorTerm = value
		case "TERM_PROGRAM":
			env.TermProgram = value
		case "LC_ALL":
			env.LCAll = value
		case "LC_CTYPE":
			env.LCCtype = value
		case "LANG":
			env.Lang = value
		}
	}
	return env
}
