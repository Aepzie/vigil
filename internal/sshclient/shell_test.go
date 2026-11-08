package sshclient

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/crypto/ssh"

	"github.com/CraguS/vigil/internal/sshclient/sshtest"
	"github.com/CraguS/vigil/internal/store"
)

var _ tea.ExecCommand = (*Shell)(nil)

// syncBuffer is a goroutine-safe bytes.Buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestShellSession(t *testing.T) {
	d, _ := newTestDialer(t)
	srv := sshtest.Start(t, sshtest.Options{})
	sh := d.NewShell(serverFor(srv), "", 0, 0)
	sh.Resize(120, 40)

	stdinR, stdinW := io.Pipe()
	t.Cleanup(func() { stdinW.Close() })
	var out syncBuffer
	sh.SetStdin(stdinR)
	sh.SetStdout(&out)
	sh.SetStderr(&out)

	result := make(chan error, 1)
	go func() { result <- sh.Run() }()

	waitFor(t, "prompt", func() bool { return strings.Contains(out.String(), "$ ") })
	if _, err := io.WriteString(stdinW, "echo hi\r"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "echo", func() bool { return strings.Contains(out.String(), "echo hi") })

	sh.Resize(100, 30)
	waitFor(t, "window change", func() bool {
		r := srv.Resizes()
		return len(r) > 0 && r[len(r)-1] == sshtest.Size{Width: 100, Height: 30}
	})

	if _, err := io.WriteString(stdinW, "exit 3\r"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run = %v, want nil for a normal non-zero exit", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after remote exit")
	}

	ptys := srv.PTYs()
	if len(ptys) != 1 {
		t.Fatalf("pty requests = %d", len(ptys))
	}
	if p := ptys[0]; p.Term != "xterm-256color" || p.Width != 120 || p.Height != 40 {
		t.Errorf("pty = %+v, want xterm-256color 120x40", p)
	}
	if !bytes.Contains(ptys[0].Modes, []byte{ssh.ECHO, 0, 0, 0, 1}) {
		t.Errorf("ECHO mode missing in %v", ptys[0].Modes)
	}
	if lang, ok := srv.Env("LANG"); !ok || lang == "" {
		t.Errorf("LANG env not sent")
	}

	sh.Resize(10, 10)
	done := make(chan struct{})
	go func() {
		io.WriteString(stdinW, "x")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stdin forwarder did not consume pending input after Run returned")
	}
}

func TestShellStdinEOFEndsRun(t *testing.T) {
	d, _ := newTestDialer(t)
	srv := sshtest.Start(t, sshtest.Options{})
	sh := d.NewShell(serverFor(srv), "screen", 80, 24)
	sh.SetStdin(strings.NewReader("ls\r"))
	result := make(chan error, 1)
	go func() { result <- sh.Run() }()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after stdin EOF")
	}
	if p := srv.PTYs(); len(p) != 1 || p[0].Term != "screen" {
		t.Errorf("pty = %+v", p)
	}
}

func TestShellDialFailure(t *testing.T) {
	d, _ := newTestDialer(t)
	stranger := sshtest.NewSigner(t)
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{stranger.PublicKey()}})
	sh := d.NewShell(serverFor(srv), "", 80, 24)
	sh.SetStdin(strings.NewReader(""))
	err := sh.Run()
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("Run = %v, want ErrAuthFailed", err)
	}
}

func TestShellResizeIsSafeWithoutRun(t *testing.T) {
	d, _ := newTestDialer(t)
	sh := d.NewShell(store.Server{}, "", 80, 24)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() { sh.Resize(80+i, 24+i) })
	}
	wg.Wait()
	sh.Resize(0, 5)
	w, h := sh.size()
	if w < 80 || h < 24 {
		t.Errorf("size = %dx%d", w, h)
	}
}
