// Package sshclient dials managed servers with the manager's key, verifies
// host keys trust-on-first-use against <data>/known_hosts, and provides an
// interactive remote shell usable with tea.Exec.
package sshclient

import (
	"context"
	"errors"
	"io"

	"golang.org/x/crypto/ssh"

	"github.com/CraguS/vigil/internal/store"
)

// Dialer is safe for concurrent use.
type Dialer struct {
	dataDir string
}

// NewDialer loads <dataDir>/id_ed25519 (generating it with 0600
// permissions on first start) and <dataDir>/known_hosts.
func NewDialer(dataDir string) (*Dialer, error) {
	return &Dialer{dataDir: dataDir}, nil
}

// Dial connects and authenticates to srv. Unknown host keys are recorded
// (trust on first use); a changed host key is rejected with
// ErrHostKeyMismatch.
func (d *Dialer) Dial(ctx context.Context, srv store.Server) (*ssh.Client, error) {
	return nil, errors.New("not implemented")
}

// PublicKey returns the manager public key as an authorized_keys line.
func (d *Dialer) PublicKey() string { return "" }

// ErrHostKeyMismatch is returned when a server presents a host key that
// differs from the one recorded in known_hosts.
var ErrHostKeyMismatch = errors.New("host key mismatch")

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
