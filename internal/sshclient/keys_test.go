package sshclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestNewDialerGeneratesAndReloadsKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	d1, err := NewDialer(dir)
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	pub := d1.PublicKey()
	fields := strings.Fields(pub)
	if len(fields) != 3 || fields[0] != ssh.KeyAlgoED25519 || !strings.HasPrefix(fields[2], "vigil@") {
		t.Fatalf("PublicKey() = %q, want \"ssh-ed25519 <base64> vigil@<host>\"", pub)
	}
	if strings.ContainsAny(pub, "\r\n") {
		t.Errorf("PublicKey() contains a newline: %q", pub)
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pub)); err != nil {
		t.Errorf("PublicKey() is not a valid authorized_keys line: %v", err)
	}

	pubFile, err := os.ReadFile(filepath.Join(dir, "id_ed25519.pub"))
	if err != nil {
		t.Fatalf("read .pub: %v", err)
	}
	if strings.TrimSpace(string(pubFile)) != pub {
		t.Errorf(".pub = %q, want %q", pubFile, pub)
	}
	if _, err := os.Stat(filepath.Join(dir, "known_hosts")); err != nil {
		t.Errorf("known_hosts not created: %v", err)
	}

	if runtime.GOOS != "windows" {
		for name, want := range map[string]os.FileMode{"id_ed25519": 0o600, "known_hosts": 0o600, ".": 0o700, "keys": 0o700} {
			st, err := os.Stat(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if got := st.Mode().Perm(); got != want {
				t.Errorf("%s mode = %o, want %o", name, got, want)
			}
		}
	}

	d2, err := NewDialer(dir)
	if err != nil {
		t.Fatalf("second NewDialer: %v", err)
	}
	if d2.PublicKey() != pub {
		t.Errorf("reloaded key differs:\n got %s\nwant %s", d2.PublicKey(), pub)
	}
}

func TestLoadKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ssh.MarshalPrivateKey(priv, "plain")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "enc", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name string, content []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"unencrypted", write("plain", pem.EncodeToMemory(plain)), ""},
		{"passphrase", write("enc", pem.EncodeToMemory(encrypted)), "passphrase"},
		{"garbage", write("garbage", []byte("not a key")), "parse private key"},
		{"too large", write("large", make([]byte, maxKeyFileSize+1)), "larger than"},
		{"missing", filepath.Join(dir, "nope"), "no such file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signer, err := loadKey(tt.path)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("loadKey: %v", err)
				}
				if signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
					t.Errorf("type = %s", signer.PublicKey().Type())
				}
				return
			}
			if err == nil {
				t.Fatalf("loadKey succeeded, want error containing %q", tt.wantErr)
			}
			if tt.name == "missing" {
				if !os.IsNotExist(err) {
					t.Errorf("err = %v, want not-exist", err)
				}
				return
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestKeyCacheReloadsChangedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	writeKey := func() ssh.PublicKey {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		block, err := ssh.MarshalPrivateKey(priv, "a much longer comment to change the size "+rand.Text())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatal(err)
		}
		s, _ := ssh.NewSignerFromKey(priv)
		return s.PublicKey()
	}
	var c keyCache
	first := writeKey()
	s1, err := c.load(dir, "key")
	if err != nil {
		t.Fatal(err)
	}
	if !keysEqual(s1.PublicKey(), first) {
		t.Fatal("first load returned wrong key")
	}
	if err := os.WriteFile(path, append(mustRead(t, path), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	s2, err := c.load(dir, "key")
	if err != nil {
		t.Fatal(err)
	}
	if !keysEqual(s2.PublicKey(), first) {
		t.Fatal("reload of same key returned a different key")
	}
	second := writeKey()
	s3, err := c.load(dir, "key")
	if err != nil {
		t.Fatal(err)
	}
	if !keysEqual(s3.PublicKey(), second) {
		t.Fatal("cache did not pick up the replaced key")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func keysEqual(a, b ssh.PublicKey) bool {
	return string(a.Marshal()) == string(b.Marshal())
}
