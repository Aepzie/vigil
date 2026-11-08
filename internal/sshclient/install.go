package sshclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Aepzie/vigil/internal/sanitize"
	"github.com/Aepzie/vigil/internal/store"
)

// DefaultInstallTimeout bounds InstallKey (both logins and the remote
// command) when the context passed to it has no deadline.
const DefaultInstallTimeout = 30 * time.Second

const (
	// maxInstallOutput bounds how much of the install command's error
	// output is read.
	maxInstallOutput = 4 << 10
	// maxInstallMessage bounds the error output quoted in an error.
	maxInstallMessage = 160
	// maxPasswordPrompts bounds the keyboard-interactive rounds answered
	// with the password, so a server cannot keep asking forever.
	maxPasswordPrompts = 3
)

// installKeyScript appends the authorized_keys line read from stdin to
// ~/.ssh/authorized_keys unless the file already contains it, creating
// the directory (0700) and file (0600) when missing. A last line without
// a trailing newline is terminated first. The key is never part of the
// command line. The script contains no single quotes, so it can be
// quoted for /bin/sh -c as is.
const installKeyScript = `umask 077 && mkdir -p "$HOME/.ssh" && f="$HOME/.ssh/authorized_keys" && touch "$f" && IFS= read -r k && ` +
	`{ grep -qxF -e "$k" "$f" || { if [ -n "$(tail -c 1 "$f")" ]; then echo >> "$f"; fi && printf "%s\n" "$k" >> "$f"; }; }`

// installKeyCommand runs installKeyScript with a POSIX shell regardless
// of the user's login shell.
const installKeyCommand = "env LC_ALL=C /bin/sh -c '" + installKeyScript + "'"

// InstallKey logs in to srv once with password (offered as password and
// keyboard-interactive authentication), appends the manager public key to
// ~/.ssh/authorized_keys of srv.User unless it is already listed, and
// then checks that logging in with the key works. The password is only
// sent to the server and never stored or included in errors.
//
// Servers with a per-server key (srv.KeyPath) are refused with
// ErrCustomKey. Host keys are verified as for Dial. A refused password
// is a *DialError matching ErrPasswordRejected (and ErrAuthFailed); a
// failed key login after a successful install wraps ErrKeyLoginFailed.
// Without a deadline on ctx, DefaultInstallTimeout applies.
func (d *Dialer) InstallKey(ctx context.Context, srv store.Server, password string) error {
	if srv.KeyPath != "" {
		return ErrCustomKey
	}
	if password == "" {
		return errors.New("no password given")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultInstallTimeout)
		defer cancel()
	}
	client, err := d.connect(ctx, srv, func() ([]ssh.AuthMethod, error) {
		return []ssh.AuthMethod{
			ssh.Password(password),
			ssh.KeyboardInteractive(answerWithPassword(password)),
		}, nil
	})
	if err != nil {
		return passwordDialError(err)
	}
	err = runInstallCommand(ctx, client, d.PublicKey())
	client.Close()
	if err != nil {
		return err
	}
	verify, err := d.Dial(ctx, srv)
	if err != nil {
		return fmt.Errorf("%w (%s); check that sshd allows public key login for this user (PubkeyAuthentication, PermitRootLogin) and that the home directory and ~/.ssh are not writable by group or others (StrictModes)",
			ErrKeyLoginFailed, Reason(err))
	}
	return verify.Close()
}

// answerWithPassword answers every non-echoed keyboard-interactive
// question with password and echoed ones with an empty string.
func answerWithPassword(password string) ssh.KeyboardInteractiveChallenge {
	rounds := 0
	return func(_, _ string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		asked := false
		for i := range questions {
			if i < len(echos) && echos[i] {
				continue
			}
			answers[i] = password
			asked = true
		}
		if !asked {
			return answers, nil
		}
		rounds++
		if rounds > maxPasswordPrompts {
			return nil, errors.New("keyboard-interactive: too many password prompts")
		}
		return answers, nil
	}
}

// passwordDialError reports a refused login as ErrPasswordRejected
// instead of the key-related ErrAuthFailed message.
func passwordDialError(err error) error {
	var de *DialError
	if errors.As(err, &de) && errors.Is(de.Kind, ErrAuthFailed) {
		de.Kind = ErrPasswordRejected
	}
	return err
}

// runInstallCommand runs installKeyCommand with key on stdin. Only a
// bounded amount of its output is kept, for the error message.
func runInstallCommand(ctx context.Context, client *ssh.Client, key string) error {
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("install key: open session: %w", errors.Join(ctx.Err(), err))
	}
	defer sess.Close()
	var stdout, stderr boundedBuffer
	sess.Stdin = strings.NewReader(key + "\n")
	sess.Stdout, sess.Stderr = &stdout, &stderr
	err = sess.Run(installKeyCommand)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("install key: %w", ctxErr)
	}
	var exitErr *ssh.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exitErr):
		output := stderr.buf
		if len(strings.TrimSpace(string(output))) == 0 {
			output = stdout.buf
		}
		return fmt.Errorf("install key: remote command failed with status %d%s", exitErr.ExitStatus(), quoteOutput(output))
	default:
		return fmt.Errorf("install key: %w", err)
	}
}

// boundedBuffer keeps the first maxInstallOutput bytes written to it and
// discards the rest.
type boundedBuffer struct{ buf []byte }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := maxInstallOutput - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// quoteOutput renders the first line of remote output as a sanitized,
// shortened ": <text>" suffix, or "" when there is none.
func quoteOutput(output []byte) string {
	first, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
	text := truncate(strings.TrimSpace(sanitize.Line(first)), maxInstallMessage)
	if text == "" {
		return ""
	}
	return ": " + text
}
