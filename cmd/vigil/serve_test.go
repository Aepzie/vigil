package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/CraguS/vigil/internal/auth"
	"github.com/CraguS/vigil/internal/fsperm"
)

func TestIsTerminalBuffer(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Fatal("a buffer is not a terminal")
	}
}

func TestBootstrapAdminOnTerminal(t *testing.T) {
	dir := t.TempDir()
	authn, err := auth.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := bootstrapAdmin(authn, &out, true, dir, "ssh-ed25519 AAAAkey vigil@m", ":2222"); err != nil {
		t.Fatalf("bootstrapAdmin: %v", err)
	}
	password := regexp.MustCompile(`password:  (\S+)`).FindStringSubmatch(out.String())
	if password == nil || !authn.CheckPassword(bootstrapUser, password[1]) {
		t.Fatalf("printed password does not log in:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, initialPasswordFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("password file written on a terminal: %v", err)
	}
}

func TestBootstrapAdminNotOnTerminal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, initialPasswordFile)
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	authn, err := auth.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := bootstrapAdmin(authn, &out, false, dir, "ssh-ed25519 AAAAkey vigil@m", ":2222"); err != nil {
		t.Fatalf("bootstrapAdmin: %v", err)
	}
	text := out.String()
	for _, want := range []string{path, "delete", "vigil user passwd admin", "-p 2222", "ssh-ed25519 AAAAkey vigil@m"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "password:") {
		t.Errorf("output shows a password:\n%s", text)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	password := regexp.MustCompile(`password:  (\S+)`).FindSubmatch(raw)
	if password == nil || !authn.CheckPassword(bootstrapUser, string(password[1])) {
		t.Fatalf("password file does not hold a working password:\n%s", raw)
	}
	if strings.Contains(text, string(password[1])) {
		t.Error("password leaked to stdout")
	}
	if !strings.Contains(string(raw), "delete this file") {
		t.Errorf("password file lacks deletion advice:\n%s", raw)
	}
	if !fsperm.Enforced() {
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("password file mode = %04o, want 0600", fi.Mode().Perm())
	}
}

func TestSecureDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	var notice bytes.Buffer
	if err := secureDataDir(dir, &notice); err != nil {
		t.Fatalf("secureDataDir: %v", err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("data directory not created: %v", err)
	}
	if notice.Len() != 0 {
		t.Errorf("unexpected notice for a new directory: %q", notice.String())
	}
	if !fsperm.Enforced() {
		return
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := secureDataDir(dir, &notice); err != nil {
		t.Fatalf("secureDataDir: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %04o, want 0700", fi.Mode().Perm())
	}
	if !strings.Contains(notice.String(), "tightened") {
		t.Errorf("notice = %q, want a tightening notice", notice.String())
	}
}
