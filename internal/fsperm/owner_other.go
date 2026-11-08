//go:build !unix

package fsperm

import "io/fs"

// enforced is false where Unix ownership and permission bits do not
// apply (Windows).
const enforced = false

func currentUID() int { return -1 }

func fileOwner(fs.FileInfo) (int, bool) { return 0, false }
