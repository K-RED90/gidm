package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/K-RED90/gidm/internal/config"
)

// TestEnsureRuntimeDirsCreatesMissing covers the fresh-install case: the user
// config dir has no gidm subdirectory, so the database and socket parents must
// be created before the store open / socket bind reach them.
func TestEnsureRuntimeDirsCreatesMissing(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}
	cfg.Storage.DBPath = filepath.Join(root, "data", "gidm.db")
	cfg.Daemon.SocketPath = filepath.Join(root, "run", "gidmd.sock")

	if err := ensureRuntimeDirs(cfg); err != nil {
		t.Fatalf("ensureRuntimeDirs: %v", err)
	}

	for _, dir := range []string{filepath.Dir(cfg.Storage.DBPath), filepath.Dir(cfg.Daemon.SocketPath)} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Fatalf("dir %q not created: stat err=%v", dir, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Errorf("dir %q mode = %o, want 700 (private to the user)", dir, info.Mode().Perm())
		}
	}

	// Idempotent: a second call against existing dirs must succeed.
	if err := ensureRuntimeDirs(cfg); err != nil {
		t.Fatalf("ensureRuntimeDirs (second call): %v", err)
	}
}

// TestEnsureRuntimeDirsError surfaces a real failure: a path component that is a
// regular file cannot be turned into a directory.
func TestEnsureRuntimeDirsError(t *testing.T) {
	root := t.TempDir()
	notADir := filepath.Join(root, "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := &config.Config{}
	cfg.Storage.DBPath = filepath.Join(notADir, "nested", "gidm.db")
	cfg.Daemon.SocketPath = filepath.Join(root, "run", "gidmd.sock")

	if err := ensureRuntimeDirs(cfg); err == nil {
		t.Fatal("ensureRuntimeDirs = nil, want error when a path component is a file")
	}
}
