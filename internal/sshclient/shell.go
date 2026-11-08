package sshclient

import (
	"errors"
	"io"

	"github.com/CraguS/vigil/internal/store"
)

// Shell is an interactive remote login shell. It implements
// tea.ExecCommand (Run, SetStdin, SetStdout, SetStderr).
type Shell struct {
	dialer *Dialer
	srv    store.Server
	term   string
	width  int
	height int
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// NewShell prepares a shell on srv with a PTY of the given terminal type
// and size. Nothing is dialed until Run.
func (d *Dialer) NewShell(srv store.Server, term string, width, height int) *Shell {
	return &Shell{dialer: d, srv: srv, term: term, width: width, height: height}
}

// SetStdin implements tea.ExecCommand.
func (s *Shell) SetStdin(r io.Reader) { s.stdin = r }

// SetStdout implements tea.ExecCommand.
func (s *Shell) SetStdout(w io.Writer) { s.stdout = w }

// SetStderr implements tea.ExecCommand.
func (s *Shell) SetStderr(w io.Writer) { s.stderr = w }

// Resize forwards a window change to the remote PTY while Run is active.
func (s *Shell) Resize(width, height int) {}

// Run dials, requests a PTY and shell, and pipes I/O until the remote
// shell exits or stdin closes.
func (s *Shell) Run() error { return errors.New("not implemented") }
