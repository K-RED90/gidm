//go:build !windows

package engine

import "os"

// syncDir flushes a directory's entries to stable storage so a rename recorded
// in it survives a crash. This is the POSIX durability barrier after the final
// atomic rename of the .part file.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
