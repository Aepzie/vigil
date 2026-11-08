package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/CraguS/vigil/internal/auth"
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

	svc, err := openServices(dataDir)
	if err != nil {
		return err
	}
	authn, err := auth.New(dataDir)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if !authn.HasCredentials() {
		password, err := randomPassword(bootstrapPassLen)
		if err != nil {
			return err
		}
		if err := authn.SetPassword(bootstrapUser, password); err != nil {
			return fmt.Errorf("create %s user: %w", bootstrapUser, err)
		}
		printFirstStart(std.out, password, svc.dialer.PublicKey(), *listen)
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

// printFirstStart shows the generated admin password once, in a box, and
// the manager key to install on targets.
func printFirstStart(w io.Writer, password, managerKey, listen string) {
	port := listen
	if i := strings.LastIndexByte(listen, ':'); i >= 0 {
		port = listen[i+1:]
	}
	lines := []string{
		"vigil first start: created dashboard user \"" + bootstrapUser + "\"",
		"",
		"    password:  " + password,
		"",
		"This password is shown only once. Log in with",
		"    ssh " + bootstrapUser + "@<this-host> -p " + port,
		"and change it with: vigil user passwd " + bootstrapUser,
	}
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
	if managerKey != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Install the manager key on every monitored server (~/.ssh/authorized_keys):")
		fmt.Fprintln(w)
		fmt.Fprintln(w, managerKey)
	}
	fmt.Fprintln(w)
}
