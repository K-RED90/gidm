package apiserver

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/K-RED90/gidm/api"
)

// probeTimeout bounds the single-instance liveness probe (dial + ping
// round-trip). It is deliberately short so startup is not delayed by a dead
// socket, and small enough that an ambiguous half-open socket fails the probe
// (treated as stale) rather than hanging. It is an internal robustness constant,
// not a user tunable: the socket path it operates on is the tunable.
const probeTimeout = 500 * time.Millisecond

// listen binds the unix socket at path after enforcing the security
// preconditions: the parent directory must not be world-writable, any
// pre-existing socket must not belong to a live daemon (single-instance), and a
// stale socket is unlinked before binding. After binding it enforces 0600 perms
// (POSIX; a no-op with a debug log on Windows). It uses "unix" exclusively and
// never net.Listen("tcp", ...).
func listen(path string, logger *slog.Logger) (net.Listener, error) {
	if err := checkParentDir(path); err != nil {
		return nil, err
	}
	if err := guardSingleInstance(path); err != nil {
		return nil, err
	}

	addr := &net.UnixAddr{Name: path, Net: "unix"}
	l, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("apiserver: listen on %q: %w", path, err)
	}

	if err := secureSocket(path, logger); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// checkParentDir refuses to create the socket under a world-writable or
// world-accessible directory, where another user could swap or read the socket.
// The check is skipped on Windows, where unix-socket files do not carry POSIX
// permission bits.
func checkParentDir(path string) error {
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("apiserver: stat socket dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("apiserver: socket parent %q is not a directory", dir)
	}
	if worldAccessible(info.Mode().Perm()) {
		return fmt.Errorf("apiserver: refusing to use world-accessible socket dir %q (mode %o)", dir, info.Mode().Perm())
	}
	return nil
}

// guardSingleInstance handles a pre-existing socket file. It dials and sends an
// api ping: if a live daemon answers, it returns ErrAlreadyRunning so the caller
// refuses to start. If the dial fails or the peer does not answer a valid ping
// (a stale or non-daemon socket), it unlinks the file so listen can bind.
func guardSingleInstance(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil // nothing in the way
		}
		return fmt.Errorf("apiserver: stat socket %q: %w", path, err)
	}

	if probeLiveDaemon(path) {
		return ErrAlreadyRunning
	}

	// Stale or non-daemon socket: remove it so we can bind.
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("apiserver: remove stale socket %q: %w", path, err)
	}
	return nil
}

// probeLiveDaemon reports whether a live gidmd answers a ping on path. A dial
// failure (ECONNREFUSED on a stale socket), a write/read error, or a response
// that is not a well-formed ping all mean "not a live daemon" -> false, so the
// socket is treated as stale.
func probeLiveDaemon(path string) bool {
	conn, err := net.DialTimeout("unix", path, probeTimeout)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(probeTimeout)
	_ = conn.SetDeadline(deadline)

	if err := api.WriteMessage(conn, api.NewPingRequest()); err != nil {
		return false
	}
	var resp api.Response
	if err := api.NewDecoder(conn).Decode(&resp); err != nil {
		return false
	}
	return resp.OK && resp.Ping != nil
}

// removeSocket deletes the socket file on shutdown, best-effort. A missing file
// is not an error (it may already be gone); any other failure is logged.
func removeSocket(path string, logger *slog.Logger) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		logger.Warn("apiserver: remove socket on shutdown", "path", path, "err", err)
	}
}
