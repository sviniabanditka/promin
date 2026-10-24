// Package fsutil: the one filesystem helper two packages need.
package fsutil

import (
	"os"
	"syscall"
)

// FreeBytes is the space left on the filesystem holding dir (created if
// missing). Used as a gate before a recording or a whole-file torrent
// prefetch: the SQLite database shares the volume, and a full disk turns every
// write into an error.
func FreeBytes(dir string) (uint64, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bsize) * st.Bavail, nil
}
