package sshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"

	"github.com/Aepzie/vigil/internal/sanitize"
)

// ErrHostKeyMismatch is returned when a server presents a host key that
// differs from the one recorded in known_hosts.
var ErrHostKeyMismatch = errors.New("host key mismatch")

// Error categories returned (wrapped in a *DialError) by Dialer.Dial.
// Test with errors.Is.
var (
	ErrAuthFailed  = errors.New("authentication failed")
	ErrTimeout     = errors.New("timeout")
	ErrRefused     = errors.New("connection refused")
	ErrUnreachable = errors.New("host unreachable")
	ErrUnknownHost = errors.New("unknown host")
	ErrClosed      = errors.New("connection closed by server")
	// ErrKeyNotUsable means the per-server key named by
	// store.Server.KeyPath cannot be used. The cause (invalid name,
	// missing file, insecure permissions, symlink leaving the keys
	// directory, unparsable key, ...) is deliberately not reported.
	ErrKeyNotUsable = errors.New("key not usable")
	// ErrPasswordRejected is the category of Dialer.InstallKey when the
	// server refuses the password (or offers neither password nor
	// keyboard-interactive login). It matches ErrAuthFailed as well.
	ErrPasswordRejected = fmt.Errorf("password rejected: %w", ErrAuthFailed)
)

// Errors returned by Dialer.InstallKey besides *DialError values.
var (
	// ErrCustomKey means the server uses a per-server key
	// (store.Server.KeyPath), which InstallKey cannot install.
	ErrCustomKey = errors.New("server uses a custom key; install it manually")
	// ErrKeyLoginFailed means the key was written to authorized_keys but
	// logging in with it failed afterwards.
	ErrKeyLoginFailed = errors.New("key written to authorized_keys, but key login failed")
)

// DialError describes why connecting to a server failed. Kind is one of
// the Err* categories (or nil when the failure is not classified) and Err
// is the underlying error. errors.Is matches both.
type DialError struct {
	User string
	Addr string
	Kind error
	Err  error
}

func (e *DialError) Error() string {
	target := e.Addr
	if e.User != "" {
		target = e.User + "@" + e.Addr
	}
	switch {
	case errors.Is(e.Kind, ErrPasswordRejected):
		return fmt.Sprintf("ssh %s: password rejected; check the password and that the server allows password or keyboard-interactive login", target)
	case errors.Is(e.Kind, ErrAuthFailed):
		return fmt.Sprintf("ssh %s: authentication failed; add the vigil public key to ~/.ssh/authorized_keys of %q on the server", target, e.User)
	case errors.Is(e.Kind, ErrHostKeyMismatch):
		return fmt.Sprintf("ssh %s: %v", target, e.Err)
	case errors.Is(e.Kind, ErrKeyNotUsable):
		return fmt.Sprintf("ssh %s: key not usable; it must be an unencrypted private key file in the vigil keys directory, owned by the vigil user with mode 0600", target)
	case e.Kind != nil:
		return fmt.Sprintf("ssh %s: %v (%v)", target, e.Kind, e.Err)
	default:
		return fmt.Sprintf("ssh %s: %v", target, e.Err)
	}
}

// Unwrap exposes both the category and the cause to errors.Is/As.
func (e *DialError) Unwrap() []error {
	if e.Kind == nil {
		return []error{e.Err}
	}
	return []error{e.Kind, e.Err}
}

// classify maps a low-level dial or handshake error to a category.
func classify(err error) error {
	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.Is(err, ErrHostKeyMismatch):
		return ErrHostKeyMismatch
	case errors.Is(err, ErrKeyNotUsable):
		return ErrKeyNotUsable
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return ErrTimeout
	case errors.As(err, &dnsErr) && !dnsErr.IsTimeout:
		return ErrUnknownHost
	case errors.As(err, &netErr) && netErr.Timeout():
		return ErrTimeout
	case isErrno(err, refusedErrnos):
		return ErrRefused
	case isErrno(err, unreachableErrnos):
		return ErrUnreachable
	case strings.Contains(err.Error(), "unable to authenticate"):
		return ErrAuthFailed
	// When the server hangs up mid-handshake, x/crypto/ssh closes the
	// connection itself and may report its own read on the closed socket
	// (net.ErrClosed) instead of EOF. Dial handles context cancellation,
	// which also closes the socket, before classifying.
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed), isErrno(err, resetErrnos):
		return ErrClosed
	default:
		return nil
	}
}

func isErrno(err error, errnos []syscall.Errno) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	for _, e := range errnos {
		if errno == e {
			return true
		}
	}
	return false
}

// Reason returns a short, single-line, sanitized description of err
// suitable for status columns, such as "auth failed", "timeout",
// "connection refused" or "host key mismatch!".
func Reason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrHostKeyMismatch):
		return "host key mismatch!"
	case errors.Is(err, ErrPasswordRejected):
		return "password rejected"
	case errors.Is(err, ErrAuthFailed):
		return "auth failed"
	case errors.Is(err, ErrKeyNotUsable):
		return "key not usable"
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrRefused):
		return "connection refused"
	case errors.Is(err, ErrUnreachable):
		return "unreachable"
	case errors.Is(err, ErrUnknownHost):
		return "unknown host"
	case errors.Is(err, ErrClosed), errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		return "connection lost"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	if c := classify(err); c != nil {
		return Reason(c)
	}
	return truncate(sanitize.Line(err.Error()), 80)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
