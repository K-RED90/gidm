//go:build !windows

package apiserver

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
)

// worldAccessible reports whether a directory mode grants write access to group
// or other. A group/other-writable parent would let another user replace or
// unlink the socket node, so we refuse to bind under it. Read/execute bits on
// the parent (e.g. a 0755 system temp dir) are not a threat to the socket
// itself, which is created 0600, so they are allowed.
func worldAccessible(perm fs.FileMode) bool {
	return perm&0o022 != 0
}

// secureSocket enforces 0600 on the bound socket file and verifies it took
// effect, so only the owner can connect. Binding may have created the file under
// a looser umask; the explicit chmod closes that window.
func secureSocket(path string, _ *slog.Logger) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("apiserver: chmod socket %q to 0600: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("apiserver: stat socket %q after chmod: %w", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		return fmt.Errorf("apiserver: socket %q has mode %o, want 0600", path, perm)
	}
	return nil
}
