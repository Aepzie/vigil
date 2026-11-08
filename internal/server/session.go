package server

import (
	"context"
	"io"
	"reflect"
	"sync"

	tea "charm.land/bubbletea/v2"
	"charm.land/ssh"
	gossh "golang.org/x/crypto/ssh"

	"github.com/CraguS/vigil/internal/store"
	"github.com/CraguS/vigil/internal/tui/ui"
)

// remoteShell is what a session needs from an interactive shell; it is
// satisfied by *sshclient.Shell.
type remoteShell interface {
	tea.ExecCommand
	Resize(width, height int)
}

// session is the per-connection state shared by the window-change reader,
// the size forwarder and remote shells.
type session struct {
	in  *inputMux
	out io.Writer

	mu     sync.Mutex
	width  int
	height int
	shell  remoteShell

	// resized holds a pending notification for forwardSizes; it has a
	// capacity of one so bursts of window changes coalesce.
	resized chan struct{}
}

func newSession(width, height int, out io.Writer) *session {
	return &session{
		in:      newInputMux(),
		out:     out,
		width:   width,
		height:  height,
		resized: make(chan struct{}, 1),
	}
}

// size returns the latest known terminal size.
func (s *session) size() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.width, s.height
}

// resize records a new window size, applies it to the active shell (whose
// program loop may be blocked in tea.Exec) and schedules a
// tea.WindowSizeMsg. It never blocks on the Bubble Tea program.
func (s *session) resize(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	s.mu.Lock()
	s.width, s.height = width, height
	shell := s.shell
	s.mu.Unlock()
	if shell != nil {
		shell.Resize(width, height)
	}
	select {
	case s.resized <- struct{}{}:
	default:
	}
}

// watchWindow applies window changes until ctx is done or winch closes.
func (s *session) watchWindow(ctx context.Context, winch <-chan ssh.Window) {
	for {
		select {
		case <-ctx.Done():
			return
		case w, ok := <-winch:
			if !ok {
				return
			}
			s.resize(w.Width, w.Height)
		}
	}
}

// forwardSizes delivers the latest size to the program whenever it
// changes. send may block (e.g. while a shell runs); sizes that arrive in
// the meantime are coalesced into one message carrying the newest size.
func (s *session) forwardSizes(ctx context.Context, send func(tea.Msg)) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.resized:
		}
		w, h := s.size()
		send(tea.WindowSizeMsg{Width: w, Height: h})
	}
}

// attach creates a shell with the current size and makes it the target of
// window changes and client input. Creation happens under the lock so no
// resize can slip between reading the size and registering the shell.
func (s *session) attach(start func(width, height int) remoteShell) (remoteShell, *shellInput) {
	s.mu.Lock()
	shell := start(s.width, s.height)
	s.shell = shell
	s.mu.Unlock()
	return shell, s.in.attachShell()
}

// detach unregisters shell and hands input back to the program.
func (s *session) detach(shell remoteShell, in *shellInput) {
	s.mu.Lock()
	if s.shell == shell {
		s.shell = nil
	}
	s.mu.Unlock()
	s.in.detachShell(in)
}

// shellFunc returns a ui.ShellFunc whose commands open shells through
// start.
func (s *session) shellFunc(start func(srv store.Server, width, height int) remoteShell) ui.ShellFunc {
	return func(srv store.Server) tea.ExecCommand {
		return &shellCommand{
			sess: s,
			start: func(width, height int) remoteShell {
				return start(srv, width, height)
			},
		}
	}
}

// shellCommand adapts a remote shell to tea.ExecCommand for one session.
// It ignores the streams offered by Bubble Tea: input comes from the
// session's inputMux, and output goes to the raw SSH channel (Bubble Tea
// would pass the server process's own stderr).
type shellCommand struct {
	sess  *session
	start func(width, height int) remoteShell
}

// SetStdin implements tea.ExecCommand; the session input is used instead.
func (c *shellCommand) SetStdin(io.Reader) {}

// SetStdout implements tea.ExecCommand; the raw channel is used instead.
func (c *shellCommand) SetStdout(io.Writer) {}

// SetStderr implements tea.ExecCommand; the raw channel is used instead.
func (c *shellCommand) SetStderr(io.Writer) {}

// Run starts the shell with the current window size and blocks until it
// exits. While it runs, window changes are forwarded to it directly.
func (c *shellCommand) Run() error {
	shell, in := c.sess.attach(c.start)
	defer c.sess.detach(shell, in)
	shell.SetStdin(in)
	shell.SetStdout(c.sess.out)
	shell.SetStderr(c.sess.out)
	return shell.Run()
}

// rawOutput returns the SSH channel underneath sess when the PTY is
// emulated. The session's own Write turns every "\n" into "\r\n", which is
// right for the dashboard but corrupts output of full-screen programs in a
// remote shell (vim, htop, less) whose remote PTY already handles line
// endings. charm.land/ssh does not expose the channel, so it is located by
// reflection; if that fails the session itself is returned.
func rawOutput(sess ssh.Session) io.Writer {
	if !sess.EmulatedPty() {
		return sess
	}
	v := reflect.ValueOf(sess)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return sess
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return sess
	}
	f := v.FieldByName("Channel")
	if !f.IsValid() || !f.CanInterface() {
		return sess
	}
	ch, ok := f.Interface().(gossh.Channel)
	if !ok || ch == nil {
		return sess
	}
	return ch
}
