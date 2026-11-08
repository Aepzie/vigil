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

	"github.com/Aepzie/vigil/internal/fsperm"
	"github.com/Aepzie/vigil/internal/store"
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
	return parseKey(path, raw)
}

func parseKey(path string, raw []byte) (ssh.Signer, error) {
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
	return readAllLimited(f, path, limit)
}

func readAllLimited(r io.Reader, path string, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("read %s: file larger than %d bytes", path, limit)
	}
	return raw, nil
}

// resolveKeyFile maps a per-server key name to a file inside dir. The name
// must pass store.ValidateKeyName, and after resolving symlinks the file
// must still be inside dir.
func resolveKeyFile(dir, name string) (string, error) {
	if err := store.ValidateKeyName(name); err != nil {
		return "", err
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("keys directory: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(filepath.Join(dir, name))
	if err != nil {
		return "", fmt.Errorf("private key: %w", err)
	}
	rel, err := filepath.Rel(realDir, realPath)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("private key %s resolves to %s, outside %s", name, realPath, realDir)
	}
	return realPath, nil
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

// load returns the signer for the key named name inside dir. The file
// must be a regular file and, on Unix, owned by the current user with no
// group or other permissions. Errors are detailed; callers showing them
// to dashboard users must replace them with ErrKeyNotUsable.
func (c *keyCache) load(dir, name string) (ssh.Signer, error) {
	path, err := resolveKeyFile(dir, name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("private key %s is not a regular file", path)
	}
	if err := fsperm.CheckPrivateInfo(path, st); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[path]; ok && k.size == st.Size() && k.modTime.Equal(st.ModTime()) {
		return k.signer, nil
	}
	raw, err := readAllLimited(f, path, maxKeyFileSize)
	if err != nil {
		return nil, err
	}
	signer, err := parseKey(path, raw)
	if err != nil {
		return nil, err
	}
	if c.keys == nil {
		c.keys = make(map[string]cachedKey)
	}
	c.keys[path] = cachedKey{size: st.Size(), modTime: st.ModTime(), signer: signer}
	return signer, nil
}
