// Package socketpath resolves the gidmd control-socket path for the desktop
// client. It deliberately does NOT import internal/config (a daemon-side package
// carrying dozens of unrelated, server-only tunables): the GUI needs only this
// one path, so it mirrors that package's single default instead of coupling to
// the whole config surface.
package socketpath

import (
	"os"
	"path/filepath"
)

// EnvVar is the override the daemon and CLI also honor, so pointing all three at
// one socket needs only this variable.
const EnvVar = "GIDM_DAEMON_SOCKET_PATH"

// Resolve returns the socket path to dial, in precedence order:
//  1. setting — an explicit value (e.g. from the app's own settings), if non-empty;
//  2. the GIDM_DAEMON_SOCKET_PATH environment override;
//  3. the documented default, os.UserConfigDir()/gidm/gidmd.sock.
//
// The default MUST match internal/config.fillComputedDefaults. If the daemon's
// default ever changes, update Default below too — TestDefaultMatchesDaemon pins
// the shape so the drift is caught.
func Resolve(setting string) string {
	if setting != "" {
		return setting
	}
	if env := os.Getenv(EnvVar); env != "" {
		return env
	}
	return Default()
}

// Default is the daemon's computed default socket path: os.UserConfigDir()/gidm/
// gidmd.sock (e.g. ~/.config/gidm/gidmd.sock on Linux/macOS).
func Default() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "gidm", "gidmd.sock")
	}
	return filepath.Join("gidm", "gidmd.sock")
}
