package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/ssh"

	"github.com/CraguS/vigil/internal/store"
)

type readResult struct {
	data string
	err  error
}

func readAsync(r io.Reader) <-chan readResult {
	ch := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := r.Read(buf)
		ch <- readResult{string(buf[:n]), err}
	}()
	return ch
}

func expectRead(t *testing.T, ch <-chan readResult, data string, err error) {
	t.Helper()
	select {
	case got := <-ch:
		if got.data != data || !errors.Is(got.err, err) {
			t.Fatalf("read = (%q, %v), want (%q, %v)", got.data, got.err, data, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read did not return")
	}
}

func expectBlocked(t *testing.T, ch <-chan readResult) {
	t.Helper()
	select {
	case got := <-ch:
		t.Fatalf("read returned (%q, %v), want it to block", got.data, got.err)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestInputMuxHandOver(t *testing.T) {
	src, feed := io.Pipe()
	m := newInputMux()
	go m.pump(src)
	program := m.program()

	pending := readAsync(program)
	expectBlocked(t, pending)
	_, _ = feed.Write([]byte("a"))
	expectRead(t, pending, "a", nil)

	// A read left pending by the program (as Bubble Tea's input loop does)
	// must give up without consuming input when a shell takes over.
	stale := readAsync(program)
	expectBlocked(t, stale)
	shell := m.attachShell()
	expectRead(t, stale, "", io.EOF)

	go func() { _, _ = feed.Write([]byte("ls\r")) }()
	expectRead(t, readAsync(shell), "ls\r", nil)

	// Same when the shell ends: its pending read must not eat the next key.
	staleShell := readAsync(shell)
	expectBlocked(t, staleShell)
	m.detachShell(shell)
	expectRead(t, staleShell, "", io.EOF)

	go func() { _, _ = feed.Write([]byte("q")) }()
	expectRead(t, readAsync(program), "q", nil)

	// A second shell gets its own generation; the first stays detached.
	second := m.attachShell()
	m.detachShell(shell)
	go func() { _, _ = feed.Write([]byte("x")) }()
	expectRead(t, readAsync(second), "x", nil)
	expectRead(t, readAsync(shell), "", io.EOF)
	m.detachShell(second)

	m.close()
	expectRead(t, readAsync(program), "", io.EOF)
}

func TestInputMuxSourceError(t *testing.T) {
	m := newInputMux()
	boom := errors.New("boom")
	m.pump(io.MultiReader(bytes.NewReader([]byte("hi")), errReader{boom}))
	expectRead(t, readAsync(m.program()), "hi", nil)
	expectRead(t, readAsync(m.program()), "", boom)
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestInputMuxBackpressure(t *testing.T) {
	m := newInputMux()
	done := make(chan struct{})
	go func() {
		m.pump(bytes.NewReader(make([]byte, 3*maxPendingInput)))
		close(done)
	}()
	waitFor(t, "buffer to fill", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.buf) >= maxPendingInput
	})
	m.mu.Lock()
	if len(m.buf) > maxPendingInput+4096 {
		t.Errorf("buffered %d bytes, limit %d", len(m.buf), maxPendingInput)
	}
	m.mu.Unlock()
	select {
	case <-done:
		t.Fatal("pump finished without a reader")
	default:
	}
	n, _ := io.Copy(io.Discard, m.program())
	<-done
	if n != 3*maxPendingInput {
		t.Fatalf("read %d bytes, want %d", n, 3*maxPendingInput)
	}
}

// fakeShell records calls made by the session.
type fakeShell struct {
	mu      sync.Mutex
	sizes   [][2]int
	stdin   io.Reader
	stdout  io.Writer
	started chan struct{}
	release chan struct{}
}

func newFakeShell() *fakeShell {
	return &fakeShell{started: make(chan struct{}), release: make(chan struct{})}
}

func (f *fakeShell) Resize(w, h int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sizes = append(f.sizes, [2]int{w, h})
}

func (f *fakeShell) lastSize() [2]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sizes) == 0 {
		return [2]int{}
	}
	return f.sizes[len(f.sizes)-1]
}

func (f *fakeShell) SetStdin(r io.Reader)  { f.stdin = r }
func (f *fakeShell) SetStdout(w io.Writer) { f.stdout = w }
func (f *fakeShell) SetStderr(io.Writer)   {}
func (f *fakeShell) Run() error {
	close(f.started)
	<-f.release
	return nil
}

func TestResizeReachesShellWhileProgramBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	s := newSession(80, 24, &out)

	winch := make(chan ssh.Window)
	go s.watchWindow(ctx, winch)

	// The program is "blocked in tea.Exec": nothing receives messages.
	blocked := make(chan tea.Msg)
	go s.forwardSizes(ctx, func(msg tea.Msg) {
		select {
		case blocked <- msg:
		case <-ctx.Done():
		}
	})

	shell := newFakeShell()
	var created [2]int
	cmd := s.shellFunc(func(_ store.Server, w, h int) remoteShell {
		created = [2]int{w, h}
		return shell
	})(store.Server{Name: "web"})
	cmd.SetStdout(io.Discard)
	runDone := make(chan error, 1)
	go func() { runDone <- cmd.Run() }()
	<-shell.started
	if created != [2]int{80, 24} {
		t.Fatalf("shell created with %v, want 80x24", created)
	}
	if shell.stdout != io.Writer(&out) {
		t.Fatal("shell output is not the raw session output")
	}

	// Many window changes while the program cannot receive: the reader
	// must keep going and the shell must see every one of them.
	for i := 1; i <= 50; i++ {
		select {
		case winch <- ssh.Window{Width: 100 + i, Height: 30 + i}:
		case <-time.After(5 * time.Second):
			t.Fatalf("window change %d blocked", i)
		}
	}
	waitFor(t, "shell resize", func() bool { return shell.lastSize() == [2]int{150, 80} })

	close(shell.release)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}

	// Once unblocked, the program receives the newest size.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-blocked:
			if ws, ok := msg.(tea.WindowSizeMsg); ok && ws.Width == 150 && ws.Height == 80 {
				return
			}
		case <-deadline:
			t.Fatal("program never received the latest size")
		}
	}
}

func TestResizeAfterShellEnds(t *testing.T) {
	s := newSession(80, 24, io.Discard)
	shell := newFakeShell()
	close(shell.release)
	cmd := s.shellFunc(func(store.Server, int, int) remoteShell { return shell })(store.Server{})
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	s.resize(120, 40)
	if got := len(shell.sizes); got != 0 {
		t.Fatalf("finished shell got %d resizes", got)
	}
	if w, h := s.size(); w != 120 || h != 40 {
		t.Fatalf("size = %dx%d", w, h)
	}
	s.resize(0, 10)
	if w, h := s.size(); w != 120 || h != 40 {
		t.Fatalf("zero-width resize applied: %dx%d", w, h)
	}
}

// readingShell is a remote shell that reads want from its stdin.
type readingShell struct {
	want    int
	stdin   io.Reader
	started chan time.Time
	got     chan string
}

func newReadingShell(want int) *readingShell {
	return &readingShell{want: want, started: make(chan time.Time, 1), got: make(chan string, 1)}
}

func (r *readingShell) Resize(int, int)       {}
func (r *readingShell) SetStdin(in io.Reader) { r.stdin = in }
func (r *readingShell) SetStdout(io.Writer)   {}
func (r *readingShell) SetStderr(io.Writer)   {}
func (r *readingShell) Run() error {
	r.started <- time.Now()
	buf := make([]byte, r.want)
	n, err := io.ReadFull(r.stdin, buf)
	r.got <- string(buf[:n])
	return err
}

// execModel opens a shell with tea.Exec on "s" and quits when it exits.
type execModel struct {
	ready chan struct{}
	open  func() tea.ExecCommand
}

type shellExitedMsg struct{ err error }

func (m execModel) Init() tea.Cmd { return nil }

func (m execModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "r":
			close(m.ready)
		case "s":
			return m, tea.Exec(m.open(), func(err error) tea.Msg { return shellExitedMsg{err} })
		}
	case shellExitedMsg:
		return m, tea.Quit
	}
	return m, nil
}

func (m execModel) View() tea.View { return tea.NewView("") }

// TestShellHandOverIsFast runs a real Bubble Tea program on the session
// input: opening a shell must not wait for Bubble Tea's 500ms read-loop
// timeout, and keys typed while the program hands over must reach the
// shell.
func TestShellHandOverIsFast(t *testing.T) {
	s := newSession(80, 24, io.Discard)
	src, feed := io.Pipe()
	defer feed.Close()
	go s.readInput(src)
	defer s.in.close()

	shell := newReadingShell(len("ls\r"))
	shellFunc := s.shellFunc(func(store.Server, int, int) remoteShell { return shell })
	built := make(chan struct{})
	model := execModel{
		ready: make(chan struct{}),
		open: func() tea.ExecCommand {
			defer close(built)
			return shellFunc(store.Server{Name: "web"})
		},
	}
	p := tea.NewProgram(model,
		tea.WithInput(s.in.program()),
		tea.WithOutput(io.Discard),
		tea.WithoutSignalHandler(),
		tea.WithWindowSize(80, 24),
	)
	runErr := make(chan error, 1)
	go func() {
		_, err := p.Run()
		runErr <- err
	}()
	defer p.Kill()

	_, _ = feed.Write([]byte("r"))
	select {
	case <-model.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("program did not start reading input")
	}

	pressed := time.Now()
	_, _ = feed.Write([]byte("s"))
	select {
	case <-built:
	case <-time.After(5 * time.Second):
		t.Fatal("shell command was not built")
	}
	_, _ = feed.Write([]byte("ls\r"))

	select {
	case started := <-shell.started:
		if d := started.Sub(pressed); d >= 100*time.Millisecond {
			t.Errorf("shell started %v after the key press, want < 100ms", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shell did not start")
	}
	select {
	case got := <-shell.got:
		if got != "ls\r" {
			t.Fatalf("shell read %q, want %q", got, "ls\r")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("keys typed during the hand-over were lost")
	}
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("program: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("program did not resume after the shell")
	}
}

// TestShellInputReturnsWhenNotRun checks that a shell command that is
// built but never run gives the input back to the program.
func TestShellInputReturnsWhenNotRun(t *testing.T) {
	defer func(d time.Duration) { shellStartTimeout = d }(shellStartTimeout)
	shellStartTimeout = 50 * time.Millisecond

	s := newSession(80, 24, io.Discard)
	src, feed := io.Pipe()
	defer feed.Close()
	go s.readInput(src)
	defer s.in.close()

	pending := readAsync(s.in.program())
	shell := newFakeShell()
	cmd := s.shellFunc(func(store.Server, int, int) remoteShell { return shell })(store.Server{})
	expectRead(t, pending, "", io.EOF)

	time.Sleep(4 * shellStartTimeout)
	go func() { _, _ = feed.Write([]byte("k")) }()
	expectRead(t, readAsync(s.in.program()), "k", nil)

	// A late Run still gets the input and returns it afterwards.
	runDone := make(chan error, 1)
	go func() { runDone <- cmd.Run() }()
	<-shell.started
	go func() { _, _ = feed.Write([]byte("x")) }()
	expectRead(t, readAsync(shell.stdin), "x", nil)
	close(shell.release)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = feed.Write([]byte("q")) }()
	expectRead(t, readAsync(s.in.program()), "q", nil)
}

func TestWatchIdle(t *testing.T) {
	const timeout = 600 * time.Millisecond
	s := newSession(80, 24, io.Discard)
	src, feed := io.Pipe()
	go s.readInput(src)
	go func() { _, _ = io.Copy(io.Discard, s.in.program()) }()

	fired := make(chan time.Time, 1)
	start := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.watchIdle(ctx, timeout, func() { fired <- time.Now() })

	// Keep typing for a while; the watchdog must not fire meanwhile.
	for range 24 {
		time.Sleep(50 * time.Millisecond)
		_, _ = feed.Write([]byte("j"))
	}
	typedUntil := time.Now()
	select {
	case at := <-fired:
		if at.Before(typedUntil) {
			t.Fatalf("fired after %v despite input", at.Sub(start))
		}
		if at.Sub(typedUntil) < timeout-50*time.Millisecond {
			t.Fatalf("fired %v after the last input, want >= %v", at.Sub(typedUntil), timeout)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle watchdog never fired")
	}
	_ = feed.Close()
}

func TestWatchIdleDisabled(t *testing.T) {
	s := newSession(80, 24, io.Discard)
	done := make(chan struct{})
	go func() {
		s.watchIdle(context.Background(), 0, func() { t.Error("fired with timeout 0") })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watchIdle with timeout 0 did not return")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
