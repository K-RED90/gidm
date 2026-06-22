//go:build !linux

package engine

import "os"

// preallocate sets f's length to size. On platforms without a portable block-
// reservation syscall wired up here (darwin, windows, ...), this is a plain
// truncate: WriteAt offsets stay valid but the file is sparse, so disk blocks
// are not reserved up front. Real reservation (fcntl F_PREALLOCATE on darwin,
// SetFileValidData on windows) can be added per platform later.
func preallocate(f *os.File, size int64) error {
	if size <= 0 {
		return nil
	}
	return f.Truncate(size)
}
