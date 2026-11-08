// Package fsperm implements StrictModes-style checks for the files vigil
// keeps secrets in. On Unix a checked file or directory must be owned by
// the user running vigil, and its permission bits must not grant group or
// others more than allowed. On other platforms (Windows) Unix ownership and
// permission bits do not apply and every check passes.
package fsperm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// ErrInsecure is wrapped by every ownership or permission complaint; test
// with errors.Is.
var ErrInsecure = errors.New("insecure ownership or permissions")

const (
	// privateMask are the bits a private file or directory must not have.
	privateMask fs.FileMode = 0o077
	// othersWriteMask are the bits a file that only must not be writable
	// by others must not have.
	othersWriteMask fs.FileMode = 0o022
)

// Enforced reports whether checks are performed on this platform.
func Enforced() bool { return enforced }

// CheckPrivateFile verifies that path is owned by the current user and
// grants no permissions to group or others (mode 0600 or stricter), as
// required for private keys and password hashes. Errors from os.Stat are
// returned unwrapped, so errors.Is(err, fs.ErrNotExist) works.
func CheckPrivateFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return CheckPrivateInfo(path, fi)
}

// CheckPrivateInfo is CheckPrivateFile for an already obtained FileInfo
// (for example from (*os.File).Stat of an open file).
func CheckPrivateInfo(path string, fi fs.FileInfo) error {
	return check(path, fi, privateMask, "access", "chmod 600 "+path)
}

// CheckNotWritableByOthers verifies that path is owned by the current
// user and is not writable by group or others, as required for files that
// grant access (authorized_keys). Errors from os.Stat are returned
// unwrapped.
func CheckNotWritableByOthers(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return check(path, fi, othersWriteMask, "write access", "chmod go-w "+path)
}

// EnsurePrivateDir creates dir with mode 0700 if it does not exist and
// verifies that it is a directory owned by the current user. When the
// current user owns it but its permissions are looser than 0700, they are
// tightened to 0700 and tightened is true. A directory owned by someone
// else is refused.
func EnsurePrivateDir(dir string) (tightened bool, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return false, err
	}
	if !fi.IsDir() {
		return false, fmt.Errorf("%s: not a directory", dir)
	}
	if !enforced {
		return false, nil
	}
	if err := checkOwner(dir, fi); err != nil {
		return false, err
	}
	if fi.Mode().Perm()&privateMask == 0 {
		return false, nil
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return false, fmt.Errorf("%s: tighten permissions to 0700: %w", dir, err)
	}
	return true, nil
}

func check(path string, fi fs.FileInfo, mask fs.FileMode, what, fix string) error {
	if !enforced {
		return nil
	}
	if err := checkOwner(path, fi); err != nil {
		return err
	}
	if perm := fi.Mode().Perm(); perm&mask != 0 {
		return fmt.Errorf("%s: %w: mode %04o grants %s to group or others; fix with: %s", path, ErrInsecure, perm, what, fix)
	}
	return nil
}

func checkOwner(path string, fi fs.FileInfo) error {
	uid, ok := fileOwner(fi)
	if !ok {
		return fmt.Errorf("%s: %w: cannot determine the owner", path, ErrInsecure)
	}
	if want := currentUID(); uid != want {
		return fmt.Errorf("%s: %w: owned by uid %d, but vigil runs as uid %d; fix with: chown %d %s", path, ErrInsecure, uid, want, want, path)
	}
	return nil
}
