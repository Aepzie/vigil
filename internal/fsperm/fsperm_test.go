package fsperm

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeWithMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestEnforced(t *testing.T) {
	if got, want := Enforced(), runtime.GOOS != "windows"; got != want {
		t.Errorf("Enforced() = %v on %s, want %v", got, runtime.GOOS, want)
	}
}

func TestMissingFileKeepsStatError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	for name, check := range map[string]func(string) error{
		"CheckPrivateFile":         CheckPrivateFile,
		"CheckNotWritableByOthers": CheckNotWritableByOthers,
	} {
		if err := check(missing); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s(missing) = %v, want fs.ErrNotExist", name, err)
		}
	}
}

func TestFileModes(t *testing.T) {
	tests := []struct {
		mode        os.FileMode
		private     bool
		notWritable bool
	}{
		{0o600, true, true},
		{0o700, true, true},
		{0o640, false, true},
		{0o604, false, true},
		{0o644, false, true},
		{0o620, false, false},
		{0o602, false, false},
		{0o664, false, false},
		{0o666, false, false},
	}
	dir := t.TempDir()
	for _, tt := range tests {
		path := filepath.Join(dir, "f"+tt.mode.String())
		writeWithMode(t, path, tt.mode)
		wantPrivate, wantNotWritable := tt.private, tt.notWritable
		if !Enforced() {
			wantPrivate, wantNotWritable = true, true
		}
		err := CheckPrivateFile(path)
		if (err == nil) != wantPrivate {
			t.Errorf("CheckPrivateFile(%04o) = %v, want ok=%v", tt.mode, err, wantPrivate)
		}
		if err != nil && (!errors.Is(err, ErrInsecure) || !strings.Contains(err.Error(), "chmod 600 "+path)) {
			t.Errorf("CheckPrivateFile(%04o) error %q lacks ErrInsecure or the fix", tt.mode, err)
		}
		err = CheckNotWritableByOthers(path)
		if (err == nil) != wantNotWritable {
			t.Errorf("CheckNotWritableByOthers(%04o) = %v, want ok=%v", tt.mode, err, wantNotWritable)
		}
		if err != nil && (!errors.Is(err, ErrInsecure) || !strings.Contains(err.Error(), "chmod go-w "+path)) {
			t.Errorf("CheckNotWritableByOthers(%04o) error %q lacks ErrInsecure or the fix", tt.mode, err)
		}
	}
}

func TestEnsurePrivateDirCreates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "data")
	tightened, err := EnsurePrivateDir(dir)
	if err != nil || tightened {
		t.Fatalf("EnsurePrivateDir = %v, %v; want false, nil", tightened, err)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}
	if Enforced() && fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %04o, want 0700", fi.Mode().Perm())
	}
}

func TestEnsurePrivateDirTightens(t *testing.T) {
	if !Enforced() {
		t.Skip("permission bits are not enforced on " + runtime.GOOS)
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o775); err != nil {
		t.Fatal(err)
	}
	tightened, err := EnsurePrivateDir(dir)
	if err != nil || !tightened {
		t.Fatalf("EnsurePrivateDir = %v, %v; want true, nil", tightened, err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %04o, want 0700", fi.Mode().Perm())
	}
	if tightened, err := EnsurePrivateDir(dir); err != nil || tightened {
		t.Errorf("second EnsurePrivateDir = %v, %v; want false, nil", tightened, err)
	}
}

func TestEnsurePrivateDirRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	writeWithMode(t, path, 0o600)
	if _, err := EnsurePrivateDir(path); err == nil {
		t.Fatal("EnsurePrivateDir accepted a regular file")
	}
}
