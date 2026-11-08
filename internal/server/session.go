package server

import (
	"context"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

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

	// lastActive is the time of the last client input or window change
	// in Unix nanoseconds.
	lastActive atomic.Int64
}

func newSession(width, height int, out io.Writer) *session {
	s := &session{
		in:      newInputMux(),
		out:     out,
		width:   width,
		height:  height,
		resized: make(chan struct{}, 1),
	}
	s.touch()
	return s
}

// touch records client activity.
func (s *session) touch() { s.lastActive.Store(time.Now().UnixNano()) }

// idleFor returns how long the client has been inactive at now.
func (s *session) idleFor(now time.Time) time.Duration {
	return now.Sub(time.Unix(0, s.lastActive.Load()))
}

// readInput pumps r into the input mux, recording activity.
func (s *session) readInput(r io.Reader) {
	s.in.pump(readerFunc(func(p []byte) (int, error) {
		n, err := r.Read(p)
		if n > 0 {
			s.touch()
		}
		return n, err
	}))
}

// watchIdle calls onIdle once the client sent neither input nor window
// changes for timeout. Output does not count: the dashboard redraws
// constantly, which is why the SSH server's own idle timeout (reset by
// any traffic) cannot detect an abandoned session.
func (s *session) watchIdle(ctx context.Context, timeout time.Duration, onIdle func()) {
	if timeout <= 0 {
		return
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			idle := s.idleFor(now)
			if idle >= timeout {
				onIdle()
				return
			}
			t.Reset(timeout - idle)
		}
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
	s.touch()
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

// shellStartTimeout is how long a shell command keeps the session input
// reserved while waiting for tea.Exec to run it; after that the input
// returns to the program.
var shellStartTimeout = 5 * time.Second

// attach creates a shell with the current size and makes it the target of
// window changes. Creation happens under the lock so no resize can slip
// between reading the size and registering the shell.
func (s *session) attach(start func(width, height int) remoteShell) remoteShell {
	s.mu.Lock()
	defer s.mu.Unlock()
	shell := start(s.width, s.height)
	s.shell = shell
	return shell
}

// detach unregisters shell.
func (s *session) detach(shell remoteShell) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shell == shell {
		s.shell = nil
	}
}

// shellFunc returns a ui.ShellFunc whose commands open shells through
// start.
func (s *session) shellFunc(start func(srv store.Server, width, height int) remoteShell) ui.ShellFunc {
	return func(srv store.Server) tea.ExecCommand {
		return s.newShellCommand(func(width, height int) remoteShell {
			return start(srv, width, height)
		})
	}
}

// newShellCommand returns a command for a shell opened through start and
// reserves the session input for it right away.
//
// tea.Exec first stops the program's input reader and waits up to 500ms
// for its pending read to return. A read blocked in the inputMux only
// returns when the program loses ownership, so taking ownership when the
// command is built (before tea.Exec runs) ends that read at once, and
// keys typed during the hand-over are buffered for the shell instead of
// being swallowed by the stopping reader. If the command does not start
// within shellStartTimeout, the input returns to the program.
func (s *session) newShellCommand(start func(width, height int) remoteShell) *shellCommand {
	c := &shellCommand{sess: s, start: start, in: s.in.attachShell()}
	c.expiry = time.AfterFunc(shellStartTimeout, c.expire)
	return c
}

// shellCommand adapts a remote shell to tea.ExecCommand for one session.
// It ignores the streams offered by Bubble Tea: input comes from the
// session's inputMux, and output goes to the raw SSH channel (Bubble Tea
// would pass the server process's own stderr).
type shellCommand struct {
	sess  *session
	start func(width, height int) remoteShell

	mu sync.Mutex
	// in is the reserved session input; nil while the program owns it.
	in      *shellInput
	expiry  *time.Timer
	running bool
}

// expire hands the reserved input back to the program unless the shell
// started.
func (c *shellCommand) expire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running || c.in == nil {
		return
	}
	c.sess.in.detachShell(c.in)
	c.in = nil
}

// claimInput marks the command running and returns its input, reserving
// it again if it expired.
func (c *shellCommand) claimInput() *shellInput {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expiry.Stop()
	c.running = true
	if c.in == nil {
		c.in = c.sess.in.attachShell()
	}
	return c.in
}

// releaseInput hands the input back to the program.
func (c *shellCommand) releaseInput() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	if c.in != nil {
		c.sess.in.detachShell(c.in)
		c.in = nil
	}
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
	in := c.claimInput()
	defer c.releaseInput()
	shell := c.sess.attach(c.start)
	defer c.sess.detach(shell)
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
