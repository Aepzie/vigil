package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/Aepzie/vigil/internal/compat"
	"github.com/Aepzie/vigil/internal/sanitize"
	"github.com/Aepzie/vigil/internal/sshclient"
	"github.com/Aepzie/vigil/internal/store"
	"github.com/Aepzie/vigil/internal/theme"
	"github.com/Aepzie/vigil/internal/tui"
	"github.com/Aepzie/vigil/internal/tui/ui"
)

// localResizePoll is how often the terminal size is checked while a
// remote shell runs locally (portable; Windows has no SIGWINCH).
const localResizePoll = 250 * time.Millisecond

func cmdLocal(args []string, std stdio) error {
	fs := newFlagSet("local", "local [--data DIR]", std)
	data := fs.String("data", "", dataDirHelp)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hubDone := make(chan error, 1)
	hubCtx, cancelHub := context.WithCancel(ctx)
	go func() { hubDone <- svc.hub.Run(hubCtx) }()
	defer func() {
		cancelHub()
		<-hubDone
	}()

	termName := sanitize.Line(os.Getenv("TERM"))
	deps := &ui.Deps{
		Theme:      theme.Default(),
		Store:      svc.store,
		Source:     svc.hub,
		Alerts:     svc.engine,
		PollNow:    svc.hub.PollNow,
		User:       localUser(),
		Version:    versionString(),
		StartedAt:  time.Now(),
		ManagerKey: svc.dialer.PublicKey(),
		DataDir:    dataDir,
		Term:       termName,
		Shell: func(srv store.Server) tea.ExecCommand {
			return &localShell{dialer: svc.dialer, srv: srv, term: termName}
		},
	}
	p := tea.NewProgram(tui.New(deps, tui.Options{
		Compat: compat.Parse(svc.store.Settings().Compat),
		Env:    localEnv(),
	}), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("dashboard: %w", err)
	}
	return nil
}

// localEnv describes the current terminal for compatibility mode.
func localEnv() compat.Env {
	env := compat.Env{
		Term:        os.Getenv("TERM"),
		ColorTerm:   os.Getenv("COLORTERM"),
		TermProgram: os.Getenv("TERM_PROGRAM"),
		LCAll:       os.Getenv("LC_ALL"),
		LCCtype:     os.Getenv("LC_CTYPE"),
		Lang:        os.Getenv("LANG"),
	}
	env.WindowsConsole = runtime.GOOS == "windows" &&
		os.Getenv("WT_SESSION") == "" &&
		env.TermProgram == "" &&
		env.Term == ""
	return env
}

// terminalSize returns the size of the terminal on stdout, or 80×24.
func terminalSize() (int, int) {
	w, h, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

// localShell opens a remote shell sized to the local terminal and keeps
// the remote PTY in sync when the terminal is resized.
type localShell struct {
	dialer *sshclient.Dialer
	srv    store.Server
	term   string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func (s *localShell) SetStdin(r io.Reader)  { s.stdin = r }
func (s *localShell) SetStdout(w io.Writer) { s.stdout = w }
func (s *localShell) SetStderr(w io.Writer) { s.stderr = w }

// Run implements tea.ExecCommand.
func (s *localShell) Run() error {
	w, h := terminalSize()
	shell := s.dialer.NewShell(s.srv, s.term, w, h)
	shell.SetStdin(s.stdin)
	shell.SetStdout(s.stdout)
	shell.SetStderr(s.stderr)

	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(localResizePoll)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if nw, nh := terminalSize(); nw != w || nh != h {
					w, h = nw, nh
					shell.Resize(w, h)
				}
			}
		}
	}()
	return shell.Run()
}
