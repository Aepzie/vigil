//go:build unix

package fsperm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pretendOtherUser(t *testing.T) {
	t.Helper()
	actual := currentUID
	currentUID = func() int { return actual() + 1 }
	t.Cleanup(func() { currentUID = actual })
}

func TestForeignOwnerIsRefused(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "users.json")
	writeWithMode(t, file, 0o600)
	dir := filepath.Join(root, "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pretendOtherUser(t)

	for name, check := range map[string]func(string) error{
		"CheckPrivateFile":         CheckPrivateFile,
		"CheckNotWritableByOthers": CheckNotWritableByOthers,
	} {
		err := check(file)
		if !errors.Is(err, ErrInsecure) || !strings.Contains(err.Error(), "owned by uid") {
			t.Errorf("%s = %v, want an ownership error", name, err)
		}
	}
	tightened, err := EnsurePrivateDir(dir)
	if !errors.Is(err, ErrInsecure) || tightened {
		t.Errorf("EnsurePrivateDir = %v, %v; want ownership error", tightened, err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() == 0o700 {
		t.Error("a directory owned by someone else must not be modified")
	}
}
