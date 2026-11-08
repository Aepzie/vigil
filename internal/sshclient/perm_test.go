package sshclient

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/CraguS/vigil/internal/fsperm"
)

func TestNewDialerRefusesExposedManagerKey(t *testing.T) {
	if !fsperm.Enforced() {
		t.Skip("permission bits are not enforced on " + runtime.GOOS)
	}
	dir := t.TempDir()
	if _, err := NewDialer(dir); err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, keyFileName), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDialer(dir); !errors.Is(err, fsperm.ErrInsecure) {
		t.Fatalf("NewDialer with a world-readable id_ed25519 = %v, want ErrInsecure", err)
	}
}
