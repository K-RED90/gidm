//go:build windows

package client

import (
	"errors"
	"syscall"
)

// wsaeconnrefused is the Winsock equivalent of ECONNREFUSED. A refused connect on
// Windows surfaces this (10061), not the POSIX syscall.ECONNREFUSED (a distinct
// value), so errors.Is against ECONNREFUSED alone never matches here. Defined as
// a literal to avoid pulling in golang.org/x/sys/windows.
const wsaeconnrefused = syscall.Errno(10061)

// isConnRefused reports whether err is a "connection refused" — the daemon's
// socket exists but nothing is listening. Both the POSIX and Winsock spellings
// are checked so callers get identical behavior across platforms.
func isConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, wsaeconnrefused)
}
