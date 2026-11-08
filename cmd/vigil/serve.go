package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sync/errgroup"

	"github.com/CraguS/vigil/internal/auth"
	"github.com/CraguS/vigil/internal/fsperm"
	"github.com/CraguS/vigil/internal/server"
	"github.com/CraguS/vigil/internal/theme"
)

// Defaults of `vigil serve`.
const (
	defaultListen      = ":2222"
	defaultIdleTimeout = 30 * time.Minute
	defaultMaxSessions = 32
	bootstrapUser      = "admin"
	bootstrapPassLen   = 20
)

// passwordAlphabet avoids look-alike characters (0/O, 1/l/I).
const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func cmdServe(args []string, std stdio) error {
	fs := newFlagSet("serve", "serve [--listen ADDR] [--data DIR] [--idle-timeout D] [--max-sessions N]", std)
	listen := fs.String("listen", defaultListen, "SSH listen address")
	data := fs.String("data", "", dataDirHelp)
	idle := fs.Duration("idle-timeout", defaultIdleTimeout, "disconnect idle sessions after this long (0 disables)")
	maxSessions := fs.Int("max-sessions", defaultMaxSessions, "maximum concurrent sessions (0 = unlimited)")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return handleParseErr(err)
	}
	if err := expectArgs(fs, rest, 0); err != nil {
		return err
	}
	dataDir, err := resolveDataDir(*data)
	if err != nil {
		return err
	}
	if err := secureDataDir(dataDir, std.err); err != nil {
		return err
	}

	svc, err := openServices(dataDir)
	if err != nil {
		return err
	}
	authn, err := auth.New(dataDir)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if !authn.HasCredentials() {
		err := bootstrapAdmin(authn, std.out, isTerminal(std.out), dataDir, svc.dialer.PublicKey(), *listen)
		if err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return ignoreCanceled(svc.hub.Run(ctx)) })
	g.Go(func() error {
		return server.Run(ctx, server.Config{
			Addr:        *listen,
			DataDir:     dataDir,
			Version:     versionString(),
			IdleTimeout: *idle,
			MaxSessions: *maxSessions,
		}, server.Deps{
			Theme:     theme.Default(),
			Store:     svc.store,
			Hub:       svc.hub,
			Alerts:    svc.engine,
			Dialer:    svc.dialer,
			Auth:      authn,
			StartedAt: time.Now(),
		})
	})
	return g.Wait()
}

// secureDataDir creates the data directory if needed and, on Unix,
// requires it to be owned by the current user. Permissions looser than
// 0700 are tightened, with a notice on w.
func secureDataDir(dataDir string, w io.Writer) error {
	tightened, err := fsperm.EnsurePrivateDir(dataDir)
	if err != nil {
		return fmt.Errorf("data directory: %w", err)
	}
	if tightened {
		fmt.Fprintf(w, "vigil: tightened permissions of data directory %s to 0700\n", dataDir)
	}
	return nil
}

func ignoreCanceled(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// randomPassword returns n characters from passwordAlphabet chosen with
// crypto/rand (rejection sampling, no modulo bias).
func randomPassword(n int) (string, error) {
	const limit = 256 - 256%len(passwordAlphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, 2*n)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("random password: %w", err)
		}
		for _, b := range buf {
			if int(b) < limit && len(out) < n {
				out = append(out, passwordAlphabet[int(b)%len(passwordAlphabet)])
			}
		}
	}
	return string(out), nil
}

// initialPasswordFile receives the first-start admin password, inside the
// data directory, when stdout is not a terminal.
const initialPasswordFile = "initial-admin-password"

// passwordSetter is the part of *auth.Authenticator bootstrapAdmin needs.
type passwordSetter interface {
	SetPassword(user, password string) error
}

// isTerminal reports whether w is a terminal. Anything else (a pipe to
// journald, docker logs, a file) may be persisted by a log collector.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// bootstrapAdmin creates the bootstrap user with a random password. On a
// terminal the password is printed. Otherwise it is written to
// <dataDir>/initial-admin-password (mode 0600) and only that path is
// printed, so the password does not end up in log storage.
func bootstrapAdmin(authn passwordSetter, w io.Writer, tty bool, dataDir, managerKey, listen string) error {
	password, err := randomPassword(bootstrapPassLen)
	if err != nil {
		return err
	}
	if tty {
		if err := authn.SetPassword(bootstrapUser, password); err != nil {
			return fmt.Errorf("create %s user: %w", bootstrapUser, err)
		}
		printFirstStart(w, password, managerKey, listen)
		return nil
	}
	path := filepath.Join(dataDir, initialPasswordFile)
	if err := writeInitialPassword(path, password, listen); err != nil {
		return err
	}
	if err := authn.SetPassword(bootstrapUser, password); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("create %s user: %w", bootstrapUser, err)
	}
	printFirstStartFile(w, path, managerKey, listen)
	return nil
}

// writeInitialPassword (re)creates path with mode 0600 holding the
// bootstrap credentials.
func writeInitialPassword(path, password, listen string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("initial password file: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("initial password file: %w", err)
	}
	content := strings.Join([]string{
		"vigil first start: dashboard credentials",
		"",
		"user:      " + bootstrapUser,
		"password:  " + password,
		"",
		"Log in with: ssh " + bootstrapUser + "@<this-host> -p " + listenPort(listen),
		"Change the password with: vigil user passwd " + bootstrapUser,
		"Then delete this file.",
		"",
	}, "\n")
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("initial password file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("initial password file: %w", err)
	}
	return nil
}

// printFirstStart shows the generated admin password once, in a box, and
// the manager key to install on targets.
func printFirstStart(w io.Writer, password, managerKey, listen string) {
	printBox(w, []string{
		"vigil first start: created dashboard user \"" + bootstrapUser + "\"",
		"",
		"    password:  " + password,
		"",
		"This password is shown only once. Log in with",
		"    ssh " + bootstrapUser + "@<this-host> -p " + listenPort(listen),
		"and change it with: vigil user passwd " + bootstrapUser,
	})
	printManagerKey(w, managerKey)
}

// printFirstStartFile is printFirstStart for a non-terminal stdout: it
// points to the file holding the password instead of showing it.
func printFirstStartFile(w io.Writer, path, managerKey, listen string) {
	printBox(w, []string{
		"vigil first start: created dashboard user \"" + bootstrapUser + "\"",
		"",
		"Its password was written to (readable only by the vigil user):",
		"    " + path,
		"",
		"Read it, log in with",
		"    ssh " + bootstrapUser + "@<this-host> -p " + listenPort(listen),
		"change the password with: vigil user passwd " + bootstrapUser,
		"and then delete the file.",
	})
	printManagerKey(w, managerKey)
}

func listenPort(listen string) string {
	if i := strings.LastIndexByte(listen, ':'); i >= 0 {
		return listen[i+1:]
	}
	return listen
}

func printBox(w io.Writer, lines []string) {
	width := 0
	for _, l := range lines {
		width = max(width, len(l))
	}
	border := "+" + strings.Repeat("-", width+2) + "+"
	fmt.Fprintln(w, border)
	for _, l := range lines {
		fmt.Fprintf(w, "| %-*s |\n", width, l)
	}
	fmt.Fprintln(w, border)
}

func printManagerKey(w io.Writer, managerKey string) {
	if managerKey != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Install the manager key on every monitored server (~/.ssh/authorized_keys):")
		fmt.Fprintln(w)
		fmt.Fprintln(w, managerKey)
	}
	fmt.Fprintln(w)
}
