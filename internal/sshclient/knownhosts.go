package sshclient

import (
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const knownHostsFileName = "known_hosts"

// hostKeyDB implements trust-on-first-use host key verification backed by
// an OpenSSH known_hosts file. All file access is serialized by mu. The
// file is re-read whenever it changes on disk, so an operator can delete
// a stale line to accept a reinstalled server without restarting vigil.
type hostKeyDB struct {
	path string

	mu       sync.Mutex
	callback ssh.HostKeyCallback
	size     int64
	modTime  time.Time
}

func openHostKeyDB(path string) (*hostKeyDB, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open known_hosts: %w", err)
	}
	f.Close()
	db := &hostKeyDB{path: path}
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.reloadLocked(); err != nil {
		return nil, err
	}
	return db, nil
}

func (db *hostKeyDB) reloadLocked() error {
	st, err := os.Stat(db.path)
	if err != nil {
		return fmt.Errorf("known_hosts: %w", err)
	}
	cb, err := knownhosts.New(db.path)
	if err != nil {
		return fmt.Errorf("load known_hosts: %w", err)
	}
	db.callback, db.size, db.modTime = cb, st.Size(), st.ModTime()
	return nil
}

func (db *hostKeyDB) refreshLocked() error {
	st, err := os.Stat(db.path)
	if err != nil {
		return fmt.Errorf("known_hosts: %w", err)
	}
	if st.Size() == db.size && st.ModTime().Equal(db.modTime) {
		return nil
	}
	return db.reloadLocked()
}

// check is an ssh.HostKeyCallback. Unknown hosts are appended to the
// file and accepted; a known host presenting a different key is rejected
// with an error wrapping ErrHostKeyMismatch.
func (db *hostKeyDB) check(hostname string, remote net.Addr, key ssh.PublicKey) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.refreshLocked(); err != nil {
		return err
	}
	err := db.callback(hostname, remote, key)
	if err == nil {
		return nil
	}
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) {
		return err
	}
	if len(keyErr.Want) == 0 {
		return db.appendLocked(hostname, key)
	}
	want := keyErr.Want[0]
	return fmt.Errorf("%w: %s presented %s key %s, but %s line %d records a different key; if the server was reinstalled, delete that line",
		ErrHostKeyMismatch, knownhosts.Normalize(hostname), key.Type(), ssh.FingerprintSHA256(key), want.Filename, want.Line)
}

func (db *hostKeyDB) appendLocked(hostname string, key ssh.PublicKey) error {
	line := knownhosts.Line([]string{hostname}, key) + "\n"
	if db.size > 0 && !endsWithNewline(db.path) {
		line = "\n" + line
	}
	f, err := os.OpenFile(db.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("record host key: %w", err)
	}
	_, werr := f.WriteString(line)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return fmt.Errorf("record host key: %w", err)
	}
	return db.reloadLocked()
}

func endsWithNewline(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return true
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, st.Size()-1); err != nil {
		return true
	}
	return last[0] == '\n'
}

// preferredHostKeyAlgorithms lists the plain (non-certificate) host key
// algorithms vigil accepts, strongest first. Certificates are excluded
// because TOFU pins raw keys.
var preferredHostKeyAlgorithms = []string{
	ssh.KeyAlgoED25519,
	ssh.KeyAlgoECDSA256,
	ssh.KeyAlgoECDSA384,
	ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoRSASHA512,
	ssh.KeyAlgoRSASHA256,
}

// algorithms returns the host key algorithms to offer when connecting to
// hostname. For a known host only the algorithms of its recorded keys are
// offered, so a server that also has other key types does not trigger a
// spurious mismatch.
func (db *hostKeyDB) algorithms(hostname string, remote net.Addr) []string {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.refreshLocked() != nil {
		return slices.Clone(preferredHostKeyAlgorithms)
	}
	var keyErr *knownhosts.KeyError
	if !errors.As(db.callback(hostname, remote, probeKey{}), &keyErr) || len(keyErr.Want) == 0 {
		return slices.Clone(preferredHostKeyAlgorithms)
	}
	known := make(map[string]bool)
	for _, k := range keyErr.Want {
		for _, algo := range algorithmsForKeyType(k.Key.Type()) {
			known[algo] = true
		}
	}
	var out []string
	for _, algo := range preferredHostKeyAlgorithms {
		if known[algo] {
			out = append(out, algo)
		}
	}
	if len(out) == 0 {
		return slices.Clone(preferredHostKeyAlgorithms)
	}
	return out
}

func algorithmsForKeyType(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	}
	return []string{keyType}
}

// probeKey is a placeholder key that never matches a known_hosts entry;
// checking it reveals which keys are recorded for a host.
type probeKey struct{}

func (probeKey) Type() string                                 { return "vigil-probe" }
func (probeKey) Marshal() []byte                              { return []byte("vigil-probe") }
func (probeKey) Verify(data []byte, sig *ssh.Signature) error { return errors.New("probe key") }
