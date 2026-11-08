package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Aepzie/vigil/internal/fsperm"
)

func TestInsecureFilesAreRefused(t *testing.T) {
	if !fsperm.Enforced() {
		t.Skip("permission bits are not enforced on " + runtime.GOOS)
	}
	key := newKey(t)
	tests := []struct {
		name   string
		file   string
		mode   os.FileMode
		wantOK bool
	}{
		{"users.json 0600", UsersFile, 0o600, true},
		{"users.json group readable", UsersFile, 0o640, false},
		{"users.json world readable", UsersFile, 0o644, false},
		{"authorized_keys 0644", AuthorizedKeysFile, 0o644, true},
		{"authorized_keys group writable", AuthorizedKeysFile, 0o664, false},
		{"authorized_keys world writable", AuthorizedKeysFile, 0o646, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			content := `{"version":1,"users":[]}`
			if tt.file == AuthorizedKeysFile {
				content = authorizedLine(key, "k")
			}
			path := filepath.Join(dir, tt.file)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tt.mode); err != nil {
				t.Fatal(err)
			}
			_, err := New(dir)
			if tt.wantOK {
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				return
			}
			if !errors.Is(err, fsperm.ErrInsecure) || !strings.Contains(err.Error(), tt.file) {
				t.Fatalf("New = %v, want an insecure-permissions error naming %s", err, tt.file)
			}
		})
	}
}

func TestChmodAtRuntimeFailsClosed(t *testing.T) {
	if !fsperm.Enforced() {
		t.Skip("permission bits are not enforced on " + runtime.GOOS)
	}
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetPassword("admin", "password1"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, UsersFile)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if a.CheckPassword("admin", "password1") {
		t.Fatal("login accepted from a world-readable users.json")
	}
	if err := a.Err(); !errors.Is(err, fsperm.ErrInsecure) {
		t.Fatalf("Err() = %v, want ErrInsecure", err)
	}
	if err := a.SetPassword("admin", "password2"); !errors.Is(err, fsperm.ErrInsecure) {
		t.Fatalf("SetPassword with a world-readable users.json = %v, want ErrInsecure", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.CheckPassword("admin", "password1") {
		t.Fatal("login rejected after permissions were fixed")
	}
}
