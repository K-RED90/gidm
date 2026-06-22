//go:build linux

package engine

import (
	"errors"
	"os"
	"syscall"
)

// preallocate reserves size bytes of real disk blocks for f and sets its length,
// so every segment's WriteAt lands in allocated space, an out-of-space condition
// fails here (up front) instead of mid-download, and the finished file is less
// fragmented. A filesystem that cannot fallocate falls back to a plain truncate,
// which sets the length sparsely (blocks allocated lazily on write).
func preallocate(f *os.File, size int64) error {
	if size <= 0 {
		return nil
	}
	// mode 0: allocate [0, size) and grow the file length to match.
	switch err := syscall.Fallocate(int(f.Fd()), 0, 0, size); {
	case err == nil:
		return nil
	case errors.Is(err, syscall.EOPNOTSUPP), errors.Is(err, syscall.ENOSYS):
		return f.Truncate(size) // FS without fallocate support: sparse fallback
	default:
		return err // ENOSPC and the like propagate so the caller fails early
	}
}
