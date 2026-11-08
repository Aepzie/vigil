// Package sshclient dials managed servers with the manager's key, verifies
// host keys trust-on-first-use against <data>/known_hosts, and provides an
// interactive remote shell usable with tea.Exec.
package sshclient

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/CraguS/vigil/internal/store"
)

// DefaultDialTimeout bounds TCP connect plus SSH handshake when the
// context passed to Dial has no deadline.
const DefaultDialTimeout = 10 * time.Second

// Dialer is safe for concurrent use.
type Dialer struct {
	dataDir string
	signer  ssh.Signer
	comment string
	hosts   *hostKeyDB
	keys    keyCache
}

// NewDialer loads <dataDir>/id_ed25519 (generating it with 0600
// permissions on first start) and <dataDir>/known_hosts.
func NewDialer(dataDir string) (*Dialer, error) {
	comment := keyComment()
	signer, err := loadOrCreateKey(dataDir, comment)
	if err != nil {
		return nil, err
	}
	hosts, err := openHostKeyDB(filepath.Join(dataDir, knownHostsFileName))
	if err != nil {
		return nil, err
	}
	return &Dialer{dataDir: dataDir, signer: signer, comment: comment, hosts: hosts}, nil
}

// PublicKey returns the manager public key as an authorized_keys line
// (without a trailing newline), commented "vigil@<hostname>".
func (d *Dialer) PublicKey() string {
	return authorizedKeyLine(d.signer.PublicKey(), d.comment)
}

// Dial connects and authenticates to srv. Unknown host keys are recorded
// (trust on first use); a changed host key is rejected with
// ErrHostKeyMismatch. Without a deadline on ctx, DefaultDialTimeout
// applies. Errors are *DialError values classified by the Err* variables.
func (d *Dialer) Dial(ctx context.Context, srv store.Server) (*ssh.Client, error) {
	addr := serverAddr(srv)
	fail := func(err error) error {
		return &DialError{User: srv.User, Addr: addr, Kind: classify(err), Err: err}
	}
	if srv.Host == "" {
		return nil, fail(errors.New("no host configured"))
	}
	if srv.User == "" {
		return nil, fail(errors.New("no user configured"))
	}
	signer, err := d.signerFor(srv)
	if err != nil {
		return nil, fail(err)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultDialTimeout)
		defer cancel()
	}

	var nd net.Dialer
	conn, err := nd.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fail(err)
	}
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, fail(err)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })

	cfg := &ssh.ClientConfig{
		User:              srv.User,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback:   d.hosts.check,
		HostKeyAlgorithms: d.hosts.algorithms(addr, conn.RemoteAddr()),
		ClientVersion:     "SSH-2.0-vigil",
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if !stop() {
		if err == nil {
			c.Close()
		}
		return nil, fail(errors.Join(ctx.Err(), err))
	}
	if err != nil {
		conn.Close()
		return nil, fail(err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		c.Close()
		return nil, fail(err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func (d *Dialer) signerFor(srv store.Server) (ssh.Signer, error) {
	if srv.KeyPath == "" {
		return d.signer, nil
	}
	return d.keys.load(srv.KeyPath)
}

// serverAddr returns host:port (IPv6 safe), defaulting to port 22.
func serverAddr(srv store.Server) string {
	port := srv.Port
	if port <= 0 {
		port = 22
	}
	return net.JoinHostPort(srv.Host, strconv.Itoa(port))
}
