package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CraguS/vigil/internal/fsperm"
)

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
