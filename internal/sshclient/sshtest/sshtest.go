// Package sshtest provides an in-process SSH server for tests. It answers
// exec requests through a callback, runs a tiny echoing line shell for
// pty-req/shell sessions, records PTY, window-change and env requests, and
// supports public key, password and keyboard-interactive authentication.
package sshtest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// ExecFunc handles an exec request. stdin yields the client's input until
// it closes its side; the returned value is sent as exit-status.
type ExecFunc func(command string, stdin io.Reader, stdout io.Writer) uint32

// Options configure a Server.
type Options struct {
	// HostKey is the server's host key; a fresh ed25519 key when nil.
	HostKey ssh.Signer
	// AuthorizedKeys restricts public key authentication. When neither it
	// nor AuthorizedKeysFile is set, any public key is accepted.
	AuthorizedKeys []ssh.PublicKey
	// AuthorizedKeysFile, when set, also accepts the keys listed in this
	// OpenSSH authorized_keys file, which is re-read on every attempt (a
	// missing file lists no keys).
	AuthorizedKeysFile string
	// Password, when non-empty, enables password authentication with
	// this password.
	Password string
	// KeyboardInteractive, when non-empty, enables keyboard-interactive
	// authentication: the server asks an echoed question, whose answer
	// is ignored, and a non-echoed one that must be answered with this
	// password.
	KeyboardInteractive string
	// Exec handles exec requests; when nil they exit with status 127.
	Exec ExecFunc
}

// PTY is a recorded pty-req.
type PTY struct {
	Term          string
	Width, Height int
	Modes         []byte
}

// Size is a recorded window-change.
type Size struct{ Width, Height int }

// Server is a running test server. It is stopped by tb.Cleanup.
type Server struct {
	ln   net.Listener
	opts Options

	mu      sync.Mutex
	hostKey ssh.Signer
	conns   map[net.Conn]struct{}
	ptys    []PTY
	resizes []Size
	env     map[string]string
	execs   []string
	logins  int
	closed  bool

	wg sync.WaitGroup
}

// Start listens on 127.0.0.1:0 and serves until the test ends.
func Start(tb testing.TB, opts Options) *Server {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("sshtest: listen: %v", err)
	}
	hostKey := opts.HostKey
	if hostKey == nil {
		hostKey = NewSigner(tb)
	}
	s := &Server{
		ln:      ln,
		opts:    opts,
		hostKey: hostKey,
		conns:   make(map[net.Conn]struct{}),
		env:     make(map[string]string),
	}
	s.wg.Go(s.serve)
	tb.Cleanup(s.Close)
	return s
}

// NewSigner returns a fresh ed25519 signer.
func NewSigner(tb testing.TB) ssh.Signer {
	tb.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatalf("sshtest: generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		tb.Fatalf("sshtest: signer: %v", err)
	}
	return signer
}

// Addr returns the listen address.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Host returns the listen IP.
func (s *Server) Host() string { return s.ln.Addr().(*net.TCPAddr).IP.String() }

// Port returns the listen port.
func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// SetHostKey replaces the host key for subsequent connections.
func (s *Server) SetHostKey(k ssh.Signer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hostKey = k
}

// HostKey returns the current host key.
func (s *Server) HostKey() ssh.Signer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostKey
}

// PTYs returns the recorded pty requests.
func (s *Server) PTYs() []PTY {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PTY(nil), s.ptys...)
}

// Resizes returns the recorded window changes.
func (s *Server) Resizes() []Size {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Size(nil), s.resizes...)
}

// Env returns the value of a recorded env request.
func (s *Server) Env(name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.env[name]
	return v, ok
}

// Execs returns the commands of all exec requests so far.
func (s *Server) Execs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.execs...)
}

// PasswordAttempts returns how many password and keyboard-interactive
// logins were attempted.
func (s *Server) PasswordAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

// Close stops the listener, closes all connections and waits for the
// handlers to return.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.ln.Close()
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			conn.Close()
			return
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		s.wg.Go(func() {
			defer func() {
				s.mu.Lock()
				delete(s.conns, conn)
				s.mu.Unlock()
				conn.Close()
			}()
			s.handleConn(conn)
		})
	}
}

func (s *Server) config() *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if s.authorized(key) {
				return &ssh.Permissions{}, nil
			}
			return nil, errRejected
		},
	}
	if s.opts.Password != "" {
		cfg.PasswordCallback = func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			s.countLogin()
			if subtle.ConstantTimeCompare(password, []byte(s.opts.Password)) == 1 {
				return &ssh.Permissions{}, nil
			}
			return nil, errRejected
		}
	}
	if s.opts.KeyboardInteractive != "" {
		cfg.KeyboardInteractiveCallback = func(_ ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			s.countLogin()
			answers, err := challenge("vigil-test", "", []string{"Note (ignored): ", "Password: "}, []bool{true, false})
			if err != nil {
				return nil, err
			}
			if len(answers) == 2 && subtle.ConstantTimeCompare([]byte(answers[1]), []byte(s.opts.KeyboardInteractive)) == 1 {
				return &ssh.Permissions{}, nil
			}
			return nil, errRejected
		}
	}
	cfg.AddHostKey(s.HostKey())
	return cfg
}

var errRejected = errors.New("sshtest: rejected")

func (s *Server) countLogin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logins++
}

// authorized reports whether key may log in.
func (s *Server) authorized(key ssh.PublicKey) bool {
	if len(s.opts.AuthorizedKeys) == 0 && s.opts.AuthorizedKeysFile == "" {
		return true
	}
	for _, k := range s.opts.AuthorizedKeys {
		if bytes.Equal(k.Marshal(), key.Marshal()) {
			return true
		}
	}
	if s.opts.AuthorizedKeysFile == "" {
		return false
	}
	rest, err := os.ReadFile(s.opts.AuthorizedKeysFile)
	if err != nil {
		return false
	}
	for len(rest) > 0 {
		var listed ssh.PublicKey
		listed, _, _, rest, err = ssh.ParseAuthorizedKey(rest)
		if err != nil {
			return false
		}
		if bytes.Equal(listed.Marshal(), key.Marshal()) {
			return true
		}
	}
	return false
}

func (s *Server) handleConn(conn net.Conn) {
	sconn, chans, reqs, err := ssh.NewServerConn(conn, s.config())
	if err != nil {
		return
	}
	defer sconn.Close()
	s.wg.Go(func() {
		for req := range reqs {
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	})
	var chWG sync.WaitGroup
	defer chWG.Wait()
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}
		chWG.Go(func() { s.handleSession(ch, chReqs) })
	}
}

func (s *Server) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	var started sync.WaitGroup
	defer started.Wait()
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			var p struct {
				Term                string
				Columns, Rows, W, H uint32
				Modes               string
			}
			ok := ssh.Unmarshal(req.Payload, &p) == nil
			if ok {
				s.mu.Lock()
				s.ptys = append(s.ptys, PTY{Term: p.Term, Width: int(p.Columns), Height: int(p.Rows), Modes: []byte(p.Modes)})
				s.mu.Unlock()
			}
			req.Reply(ok, nil)
		case "window-change":
			var w struct{ Columns, Rows, W, H uint32 }
			if ssh.Unmarshal(req.Payload, &w) == nil {
				s.mu.Lock()
				s.resizes = append(s.resizes, Size{Width: int(w.Columns), Height: int(w.Rows)})
				s.mu.Unlock()
			}
		case "env":
			var e struct{ Name, Value string }
			ok := ssh.Unmarshal(req.Payload, &e) == nil
			if ok {
				s.mu.Lock()
				s.env[e.Name] = e.Value
				s.mu.Unlock()
			}
			req.Reply(ok, nil)
		case "exec":
			var e struct{ Command string }
			if ssh.Unmarshal(req.Payload, &e) != nil {
				req.Reply(false, nil)
				continue
			}
			s.mu.Lock()
			s.execs = append(s.execs, e.Command)
			s.mu.Unlock()
			req.Reply(true, nil)
			started.Go(func() {
				status := uint32(127)
				if s.opts.Exec != nil {
					status = s.opts.Exec(e.Command, ch, ch)
				}
				sendExit(ch, status)
			})
		case "shell":
			req.Reply(true, nil)
			started.Go(func() { sendExit(ch, lineShell(ch)) })
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

func sendExit(ch ssh.Channel, status uint32) {
	ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
	ch.Close()
}

// lineShell echoes input like a terminal with ECHO set. A line "exit N"
// ends the shell with status N; EOF ends it with status 0.
func lineShell(ch ssh.Channel) uint32 {
	io.WriteString(ch, "$ ")
	var line []byte
	buf := make([]byte, 1024)
	for {
		n, err := ch.Read(buf)
		for _, b := range buf[:n] {
			if b != '\r' && b != '\n' {
				line = append(line, b)
				ch.Write([]byte{b})
				continue
			}
			io.WriteString(ch, "\r\n")
			if rest, ok := strings.CutPrefix(string(line), "exit"); ok {
				status, _ := strconv.Atoi(strings.TrimSpace(rest))
				return uint32(status)
			}
			line = line[:0]
			io.WriteString(ch, "$ ")
		}
		if err != nil {
			return 0
		}
	}
}
