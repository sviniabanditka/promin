package dvr

import (
	"os"
	"syscall"
)

// freeBytes is the space left on the filesystem holding dir. Used as a gate
// before starting a recording: the SQLite database shares the volume, and a
// full disk turns every write into an error.
func freeBytes(dir string) (uint64, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bsize) * st.Bavail, nil
}
