package socketpath_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/K-RED90/gidm/desktop/internal/socketpath"
)

func TestResolvePrecedence(t *testing.T) {
	t.Setenv(socketpath.EnvVar, "/env/path.sock")

	if got := socketpath.Resolve("/explicit.sock"); got != "/explicit.sock" {
		t.Errorf("explicit setting should win: got %q", got)
	}
	if got := socketpath.Resolve(""); got != "/env/path.sock" {
		t.Errorf("env should win when no setting: got %q", got)
	}
}

func TestResolveFallsBackToDefault(t *testing.T) {
	t.Setenv(socketpath.EnvVar, "")
	if got := socketpath.Resolve(""); got != socketpath.Default() {
		t.Errorf("Resolve(\"\") = %q, want default %q", got, socketpath.Default())
	}
}

// The default must mirror the daemon's internal/config default shape
// (<UserConfigDir>/gidm/gidmd.sock); this pins the drift contract.
func TestDefaultMatchesDaemonShape(t *testing.T) {
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no user config dir on this platform")
	}
	want := filepath.Join(dir, "gidm", "gidmd.sock")
	if got := socketpath.Default(); got != want {
		t.Errorf("Default() = %q, want %q", got, want)
	}
}
