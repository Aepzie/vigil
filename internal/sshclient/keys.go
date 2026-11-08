package sshclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/CraguS/vigil/internal/fsperm"
)

const (
	keyFileName = "id_ed25519"
	// maxKeyFileSize bounds how much of a private key file is read; real
	// OpenSSH keys are a few KiB at most.
	maxKeyFileSize = 64 << 10
)

// keyComment returns the comment used for the manager key,
// "vigil@<hostname>".
func keyComment() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "manager"
	}
	return "vigil@" + host
}

// loadOrCreateKey loads <dir>/id_ed25519 or generates it on first start.
// It also (re)writes <dir>/id_ed25519.pub when it is missing. On Unix an
// existing key must be owned by the current user with no group or other
// permissions, or it is refused.
func loadOrCreateKey(dir, comment string) (ssh.Signer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dir, keyFileName)
	signer, err := loadManagerKey(path, comment)
	if err != nil {
		return nil, err
	}
	if err := writePublicKeyIfMissing(path+".pub", signer.PublicKey(), comment); err != nil {
		return nil, err
	}
	return signer, nil
}

func loadManagerKey(path, comment string) (ssh.Signer, error) {
	err := fsperm.CheckPrivateFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return createKey(path, comment)
	}
	if err != nil {
		return nil, fmt.Errorf("refusing to use the manager key: %w", err)
	}
	return loadKey(path)
}

// createKey generates a new ed25519 key and stores it in OpenSSH format
// with 0600 permissions. If another process created the file first, that
// key is loaded instead.
func createKey(path, comment string) (ssh.Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, fmt.Errorf("encode private key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return loadKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("create private key: %w", err)
	}
	if _, err := f.Write(pem.EncodeToMemory(block)); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("write private key: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("write private key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("create signer: %w", err)
	}
	return signer, nil
}

func writePublicKeyIfMissing(path string, pub ssh.PublicKey, comment string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	line := authorizedKeyLine(pub, comment) + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		return fmt.Errorf("write public key: %w", err)
	}
	return nil
}

// authorizedKeyLine formats pub as a single authorized_keys line without
// a trailing newline.
func authorizedKeyLine(pub ssh.PublicKey, comment string) string {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment == "" {
		return line
	}
	return line + " " + comment
}

// loadKey reads an unencrypted private key in any format understood by
// ssh.ParsePrivateKey (OpenSSH, PKCS#1, PKCS#8, SEC1).
func loadKey(path string) (ssh.Signer, error) {
	raw, err := readLimited(path, maxKeyFileSize)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(raw)
	var missing *ssh.PassphraseMissingError
	switch {
	case errors.As(err, &missing):
		return nil, fmt.Errorf("private key %s is protected by a passphrase; vigil needs an unencrypted key (remove the passphrase with: ssh-keygen -p -N '' -f %s)", path, path)
	case err != nil:
		return nil, fmt.Errorf("parse private key %s: %w", path, err)
	}
	return signer, nil
}

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("read %s: file larger than %d bytes", path, limit)
	}
	return raw, nil
}

// expandHome replaces a leading "~/" (or a lone "~") with the current
// user's home directory.
func expandHome(path string) (string, error) {
	rest, ok := strings.CutPrefix(path, "~")
	if !ok || (rest != "" && rest[0] != '/' && rest[0] != filepath.Separator) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand %q: %w", path, err)
	}
	return filepath.Join(home, filepath.FromSlash(strings.TrimLeft(rest, `/\`))), nil
}

// keyCache memoizes per-server private keys, reloading a file when its
// size or modification time changes.
type keyCache struct {
	mu   sync.Mutex
	keys map[string]cachedKey
}

type cachedKey struct {
	size    int64
	modTime time.Time
	signer  ssh.Signer
}

func (c *keyCache) load(path string) (ssh.Signer, error) {
	expanded, err := expandHome(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(expanded)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[expanded]; ok && k.size == st.Size() && k.modTime.Equal(st.ModTime()) {
		return k.signer, nil
	}
	signer, err := loadKey(expanded)
	if err != nil {
		return nil, err
	}
	if c.keys == nil {
		c.keys = make(map[string]cachedKey)
	}
	c.keys[expanded] = cachedKey{size: st.Size(), modTime: st.ModTime(), signer: signer}
	return signer, nil
}
