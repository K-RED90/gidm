//go:build windows

package engine

// syncDir is a no-op on Windows. Windows does not permit fsync on a directory
// handle — opening a directory and calling Sync returns ERROR_ACCESS_DENIED —
// and NTFS durably records a completed rename without an explicit directory
// flush, so skipping it is both necessary and safe.
func syncDir(string) error { return nil }
