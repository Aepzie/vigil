package sshclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/CraguS/vigil/internal/fsperm"
	"github.com/CraguS/vigil/internal/store"
)

func newPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block)
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
}

func TestKeyNameResolution(t *testing.T) {
	root := t.TempDir()
	keysDir := filepath.Join(root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pemKey := newPrivateKeyPEM(t)
	outside := filepath.Join(root, "outside_ed25519")
	for _, path := range []string{filepath.Join(keysDir, "good"), outside} {
		if err := os.WriteFile(path, pemKey, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(keysDir, "adir"), 0o700); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		key    string
		wantOK bool
	}{
		{"plain name", "good", true},
		{"missing", "nope", false},
		{"directory", "adir", false},
		{"absolute path", outside, false},
		{"traversal", "../outside_ed25519", false},
		{"home", "~/outside_ed25519", false},
		{"dot dot", "..", false},
		{"empty", "", false},
	}
	var c keyCache
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.load(keysDir, tt.key)
			if (err == nil) != tt.wantOK {
				t.Fatalf("load(%q) = %v, want ok=%v", tt.key, err, tt.wantOK)
			}
		})
	}
}

func TestKeySymlinks(t *testing.T) {
	root := t.TempDir()
	keysDir := filepath.Join(root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pemKey := newPrivateKeyPEM(t)
	outside := filepath.Join(root, "secret")
	inside := filepath.Join(keysDir, "real")
	for _, path := range []string{outside, inside} {
		if err := os.WriteFile(path, pemKey, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	symlinkOrSkip(t, outside, filepath.Join(keysDir, "escape"))
	symlinkOrSkip(t, "../secret", filepath.Join(keysDir, "relescape"))
	symlinkOrSkip(t, "real", filepath.Join(keysDir, "alias"))
	symlinkOrSkip(t, ".", filepath.Join(keysDir, "self"))

	var c keyCache
	for _, name := range []string{"escape", "relescape", "self"} {
		if _, err := c.load(keysDir, name); err == nil {
			t.Errorf("load(%q) followed a symlink out of the keys directory", name)
		}
	}
	if _, err := c.load(keysDir, "alias"); err != nil {
		t.Errorf("load(alias) of a symlink inside the keys directory: %v", err)
	}
}

func TestKeyPermissionsEnforced(t *testing.T) {
	if !fsperm.Enforced() {
		t.Skip("permission bits are not enforced here")
	}
	keysDir := t.TempDir()
	path := filepath.Join(keysDir, "shared")
	if err := os.WriteFile(path, newPrivateKeyPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	var c keyCache
	if _, err := c.load(keysDir, "shared"); err != nil {
		t.Fatalf("load 0600 key: %v", err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := c.load(keysDir, "shared"); !errors.Is(err, fsperm.ErrInsecure) {
		t.Fatalf("load of a group-readable key = %v, want ErrInsecure (also when cached)", err)
	}
}

func TestDialKeyErrorsAreGeneric(t *testing.T) {
	d, dir := newTestDialer(t)
	if err := os.WriteFile(filepath.Join(dir, store.KeysDir, "garbage"), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	var messages []string
	for _, name := range []string{"missing", "garbage", "/etc/passwd", "../id_ed25519"} {
		_, err := d.Dial(dialCtx(t, time.Second), store.Server{Host: "127.0.0.1", Port: 22, User: "u", KeyPath: name})
		if !errors.Is(err, ErrKeyNotUsable) {
			t.Fatalf("Dial with key %q = %v, want ErrKeyNotUsable", name, err)
		}
		if got := Reason(err); got != "key not usable" {
			t.Errorf("Reason = %q, want %q", got, "key not usable")
		}
		messages = append(messages, err.Error())
	}
	for _, msg := range messages[1:] {
		if msg != messages[0] {
			t.Errorf("key errors differ, revealing the cause:\n%s\n%s", messages[0], msg)
		}
	}
}
