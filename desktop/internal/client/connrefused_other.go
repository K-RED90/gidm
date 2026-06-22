//go:build !windows

package client

import (
	"errors"
	"syscall"
)

// isConnRefused reports whether err is a "connection refused" — the daemon's
// socket exists but nothing is listening. On POSIX systems this is ECONNREFUSED.
func isConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
