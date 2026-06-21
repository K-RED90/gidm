package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTempConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestDefaultsAreSane(t *testing.T) {
	c := Default()
	if c.Download.MaxConcurrent < 1 {
		t.Errorf("default max_concurrent = %d, want >= 1", c.Download.MaxConcurrent)
	}
	if c.Download.SegmentsPerDownload < 1 {
		t.Errorf("default segments_per_download = %d, want >= 1", c.Download.SegmentsPerDownload)
	}
	if c.Storage.DBPath == "" || c.Daemon.SocketPath == "" || c.Paths.DownloadDir == "" {
		t.Error("default computed paths must be populated")
	}
	if err := c.Validate(); err != nil {
		t.Errorf("default config is invalid: %v", err)
	}
}

func TestFileOverridesDefaults(t *testing.T) {
	path := writeTempConfig(t, `
[download]
max_concurrent = 12
timeout = "45s"
`)
	t.Setenv("GIDM_CONFIG", path)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Download.MaxConcurrent != 12 {
		t.Errorf("max_concurrent = %d, want 12 (from file)", c.Download.MaxConcurrent)
	}
	if got := c.Download.Timeout.Duration(); got != 45*time.Second {
		t.Errorf("timeout = %s, want 45s (from file)", got)
	}
	// A key absent from the file keeps its default.
	if want := Default().Download.SegmentsPerDownload; c.Download.SegmentsPerDownload != want {
		t.Errorf("segments_per_download = %d, want default %d", c.Download.SegmentsPerDownload, want)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	path := writeTempConfig(t, "[download]\nmax_concurrent = 12\n")
	t.Setenv("GIDM_CONFIG", path)
	t.Setenv("GIDM_DOWNLOAD_MAX_CONCURRENT", "16")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Download.MaxConcurrent != 16 {
		t.Errorf("max_concurrent = %d, want 16 (env overrides file)", c.Download.MaxConcurrent)
	}
}

func TestEmptyPathFallsBackToDefault(t *testing.T) {
	path := writeTempConfig(t, "[storage]\ndb_path = \"\"\n")
	t.Setenv("GIDM_CONFIG", path)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Storage.DBPath == "" {
		t.Error("empty db_path should fall back to a computed default, got empty")
	}
}

func TestEnvInvalidValueIsAnError(t *testing.T) {
	t.Setenv("GIDM_CONFIG", filepath.Join(t.TempDir(), "absent.toml"))
	t.Setenv("GIDM_DOWNLOAD_MAX_CONCURRENT", "not-a-number")
	if _, err := Load(); err == nil {
		t.Fatal("Load: expected error for non-numeric env value, got nil")
	}
}

func TestUnknownFileKeyIsRejected(t *testing.T) {
	path := writeTempConfig(t, "[download]\nbogus_key = 1\n")
	t.Setenv("GIDM_CONFIG", path)
	if _, err := Load(); err == nil {
		t.Fatal("Load: expected error for unknown config key, got nil")
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	t.Setenv("GIDM_CONFIG", filepath.Join(t.TempDir(), "absent.toml"))
	c, err := Load()
	if err != nil {
		t.Fatalf("Load with missing file: %v", err)
	}
	if want := Default().Download.MaxConcurrent; c.Download.MaxConcurrent != want {
		t.Errorf("max_concurrent = %d, want default %d", c.Download.MaxConcurrent, want)
	}
}

func TestInvalidConfigFailsValidation(t *testing.T) {
	c := Default()
	c.Download.MaxConcurrent = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate: expected error for max_concurrent = 0, got nil")
	}
}
