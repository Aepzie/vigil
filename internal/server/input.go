package server

import (
	"io"
	"sync"
)

// maxPendingInput bounds how much client input is buffered before the
// pump stops reading from the session (backpressure).
const maxPendingInput = 64 << 10

// inputMux shares a session's input between the Bubble Tea program and a
// remote shell started with tea.Exec.
//
// Both Bubble Tea's input reader and a shell's stdin copier keep a Read
// call pending on their input. Reading the session directly, whichever
// stale reader wakes up first swallows the next keystroke after every
// hand-over. inputMux instead lets exactly one consumer own the input: a
// pending read of a consumer that lost ownership returns io.EOF without
// consuming anything, so no input is lost when control moves between the
// dashboard and a shell.
type inputMux struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	// err is the sticky error of the source (io.EOF once closed).
	err error
	// shell is the generation of the attached shell reader; 0 means the
	// Bubble Tea program owns the input.
	shell uint64
	gen   uint64
}

func newInputMux() *inputMux {
	m := &inputMux{}
	m.cond = sync.NewCond(&m.mu)
	return m
}

// pump copies r into the buffer until r fails or the mux is closed.
func (m *inputMux) pump(r io.Reader) {
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		m.mu.Lock()
		for len(m.buf) >= maxPendingInput && m.err == nil {
			m.cond.Wait()
		}
		if m.err != nil {
			m.mu.Unlock()
			return
		}
		m.buf = append(m.buf, chunk[:n]...)
		if err != nil {
			m.err = err
		}
		m.cond.Broadcast()
		m.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// close makes every pending and future read return io.EOF once the
// buffered input is drained, and stops the pump.
func (m *inputMux) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err == nil {
		m.err = io.EOF
	}
	m.cond.Broadcast()
}

// read blocks until data, an error or loss of ownership. owns is
// evaluated with m.mu held.
func (m *inputMux) read(p []byte, owns func() bool) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		switch {
		case !owns():
			return 0, io.EOF
		case len(m.buf) > 0:
			n := copy(p, m.buf)
			m.buf = m.buf[n:]
			if len(m.buf) == 0 {
				m.buf = nil
			}
			m.cond.Broadcast()
			return n, nil
		case m.err != nil:
			return 0, m.err
		}
		m.cond.Wait()
	}
}

// program returns the reader for the Bubble Tea program. It yields input
// only while no shell is attached.
func (m *inputMux) program() io.Reader {
	return readerFunc(func(p []byte) (int, error) {
		return m.read(p, func() bool { return m.shell == 0 })
	})
}

// attachShell transfers ownership to a new shell reader.
func (m *inputMux) attachShell() *shellInput {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gen++
	m.shell = m.gen
	m.cond.Broadcast()
	return &shellInput{m: m, gen: m.gen}
}

// detachShell returns ownership to the Bubble Tea program if in is still
// the attached shell reader.
func (m *inputMux) detachShell(in *shellInput) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shell == in.gen {
		m.shell = 0
	}
	m.cond.Broadcast()
}

// shellInput is the stdin of one remote shell.
type shellInput struct {
	m   *inputMux
	gen uint64
}

// Read implements io.Reader.
func (s *shellInput) Read(p []byte) (int, error) {
	return s.m.read(p, func() bool { return s.m.shell == s.gen })
}

type readerFunc func(p []byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }
