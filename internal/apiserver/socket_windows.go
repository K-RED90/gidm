//go:build windows

package apiserver

import (
	"io/fs"
	"log/slog"
)

// worldAccessible always reports false on Windows: unix-socket files there do
// not carry POSIX permission bits, so the world-writable parent-dir check does
// not apply. Access control degrades to the filesystem ACLs of the data dir.
func worldAccessible(_ fs.FileMode) bool { return false }

// secureSocket is a no-op on Windows: the 0600 chmod semantics POSIX relies on
// do not exist for AF_UNIX socket files there. It degrades cleanly with a debug
// log rather than failing the build or the bind.
func secureSocket(path string, logger *slog.Logger) error {
	logger.Debug("apiserver: 0600 socket permissions are not enforced on Windows", "path", path)
	return nil
}
