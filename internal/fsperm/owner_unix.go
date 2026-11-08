//go:build unix

package fsperm

import (
	"io/fs"
	"os"
	"syscall"
)

const enforced = true

// currentUID returns the effective user ID; tests replace it.
var currentUID = os.Geteuid

func fileOwner(fi fs.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
