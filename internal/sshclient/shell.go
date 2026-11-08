package sshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Aepzie/vigil/internal/store"
)

const (
	defaultTerm   = "xterm-256color"
	defaultWidth  = 80
	defaultHeight = 24
	// stdinUnblockWait is how long Run waits for the stdin forwarder to
	// notice a read deadline before giving up on it.
	stdinUnblockWait = 250 * time.Millisecond
)

// Shell is an interactive remote login shell. It implements
// tea.ExecCommand (Run, SetStdin, SetStdout, SetStderr).
//
// Run forwards the caller's stdin from a background goroutine. Once the
// remote shell exits, that goroutine may still be blocked reading stdin;
// it never writes to the finished session and exits after its read
// returns, which means at most one pending chunk of input (typically one
// keystroke) typed after the shell exited is consumed and discarded. If
// stdin supports SetReadDeadline, Run interrupts the read instead.
type Shell struct {
	dialer *Dialer
	srv    store.Server
	term   string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	mu      sync.Mutex
	width   int
	height  int
	resized chan struct{}
}

// NewShell prepares a shell on srv with a PTY of the given terminal type
// (default xterm-256color) and size. Nothing is dialed until Run.
func (d *Dialer) NewShell(srv store.Server, term string, width, height int) *Shell {
	if term == "" {
		term = defaultTerm
	}
	if width <= 0 || height <= 0 {
		width, height = defaultWidth, defaultHeight
	}
	return &Shell{
		dialer:  d,
		srv:     srv,
		term:    term,
		width:   width,
		height:  height,
		resized: make(chan struct{}, 1),
	}
}

// SetStdin implements tea.ExecCommand.
func (s *Shell) SetStdin(r io.Reader) { s.stdin = r }

// SetStdout implements tea.ExecCommand.
func (s *Shell) SetStdout(w io.Writer) { s.stdout = w }

// SetStderr implements tea.ExecCommand.
func (s *Shell) SetStderr(w io.Writer) { s.stderr = w }

// Resize records the terminal size and forwards it to the remote PTY
// while Run is active. It never blocks and is safe to call concurrently,
// before, during and after Run.
func (s *Shell) Resize(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	s.mu.Lock()
	s.width, s.height = width, height
	s.mu.Unlock()
	select {
	case s.resized <- struct{}{}:
	default:
	}
}

func (s *Shell) size() (width, height int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.width, s.height
}

// Run dials, requests a PTY and shell, and pipes I/O until the remote
// shell exits or stdin closes. A shell that exits normally returns nil,
// whatever its exit status; dial, authentication and session setup
// failures are returned as errors.
func (s *Shell) Run() error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultDialTimeout)
	client, err := s.dialer.Dial(ctx, s.srv)
	cancel()
	if err != nil {
		return err
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("open session on %s: %w", serverAddr(s.srv), err)
	}
	defer sess.Close()

	_ = sess.Setenv("LANG", shellLang())
	select {
	case <-s.resized:
	default:
	}
	width, height := s.size()
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty(s.term, height, width, modes); err != nil {
		return fmt.Errorf("request pty on %s: %w", serverAddr(s.srv), err)
	}
	remoteIn, err := sess.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	sess.Stdout = writerOrDiscard(s.stdout)
	sess.Stderr = writerOrDiscard(s.stderr)
	if err := sess.Shell(); err != nil {
		return fmt.Errorf("start shell on %s: %w", serverAddr(s.srv), err)
	}

	done := make(chan struct{})
	gate := &gatedWriter{w: remoteIn}
	var resizeWG sync.WaitGroup
	resizeWG.Go(func() { s.forwardResizes(sess, done) })
	stdinDone := make(chan struct{})
	if s.stdin != nil {
		go s.forwardStdin(sess, gate, done, stdinDone)
	} else {
		close(stdinDone)
	}

	err = sess.Wait()
	_ = sess.Close()
	gate.close()
	close(done)
	resizeWG.Wait()
	s.interruptStdin(stdinDone)
	return shellExitError(err)
}

func (s *Shell) forwardResizes(sess *ssh.Session, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case <-s.resized:
			width, height := s.size()
			_ = sess.WindowChange(height, width)
		}
	}
}

// forwardStdin copies the caller's stdin into the session. When stdin
// ends, the session is closed so Run returns.
func (s *Shell) forwardStdin(sess *ssh.Session, remote *gatedWriter, done <-chan struct{}, finished chan<- struct{}) {
	defer close(finished)
	buf := make([]byte, 32<<10)
	for {
		n, err := s.stdin.Read(buf)
		if n > 0 {
			if _, werr := remote.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err == nil {
			continue
		}
		select {
		case <-done:
		default:
			_ = sess.Close()
		}
		return
	}
}

// interruptStdin unblocks a pending stdin read when the reader supports
// read deadlines, then clears the deadline so the caller can keep reading.
func (s *Shell) interruptStdin(stdinDone <-chan struct{}) {
	dl, ok := s.stdin.(interface{ SetReadDeadline(time.Time) error })
	if !ok {
		return
	}
	select {
	case <-stdinDone:
		return
	default:
	}
	if dl.SetReadDeadline(time.Now()) != nil {
		return
	}
	select {
	case <-stdinDone:
	case <-time.After(stdinUnblockWait):
	}
	_ = dl.SetReadDeadline(time.Time{})
}

// shellExitError treats any orderly end of the remote shell as success:
// the exit status of the user's last command is not an error of vigil.
func shellExitError(err error) error {
	var exitErr *ssh.ExitError
	var missing *ssh.ExitMissingError
	if err == nil || errors.As(err, &exitErr) || errors.As(err, &missing) || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func shellLang() string {
	if lang := os.Getenv("LANG"); lang != "" {
		return lang
	}
	return "C.UTF-8"
}

func writerOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// gatedWriter forwards writes until closed; afterwards every write fails
// without touching the underlying writer.
type gatedWriter struct {
	mu     sync.Mutex
	w      io.Writer
	closed bool
}

func (g *gatedWriter) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return 0, io.ErrClosedPipe
	}
	return g.w.Write(p)
}

func (g *gatedWriter) close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}
