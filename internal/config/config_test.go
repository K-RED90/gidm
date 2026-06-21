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

func TestDaemonTunableDefaults(t *testing.T) {
	c := Default()
	if c.Daemon.ReadTimeout.Duration() != 30*time.Second {
		t.Errorf("read_timeout = %s, want 30s", c.Daemon.ReadTimeout.Duration())
	}
	if c.Daemon.WriteTimeout.Duration() != 10*time.Second {
		t.Errorf("write_timeout = %s, want 10s", c.Daemon.WriteTimeout.Duration())
	}
	if c.Daemon.ShutdownTimeout.Duration() != 10*time.Second {
		t.Errorf("shutdown_timeout = %s, want 10s", c.Daemon.ShutdownTimeout.Duration())
	}
	if c.Daemon.MaxRequestBytes != 1<<20 {
		t.Errorf("max_request_bytes = %d, want %d", c.Daemon.MaxRequestBytes, 1<<20)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("default daemon config is invalid: %v", err)
	}
}

func TestDaemonTunablesLayering(t *testing.T) {
	path := writeTempConfig(t, "[daemon]\nread_timeout = \"15s\"\nmax_request_bytes = 2048\n")
	t.Setenv("GIDM_CONFIG", path)
	t.Setenv("GIDM_DAEMON_READ_TIMEOUT", "5s") // env overrides file
	t.Setenv("GIDM_DAEMON_WRITE_TIMEOUT", "7s")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Daemon.ReadTimeout.Duration() != 5*time.Second {
		t.Errorf("read_timeout = %s, want 5s (env over file)", c.Daemon.ReadTimeout.Duration())
	}
	if c.Daemon.WriteTimeout.Duration() != 7*time.Second {
		t.Errorf("write_timeout = %s, want 7s (env)", c.Daemon.WriteTimeout.Duration())
	}
	if c.Daemon.MaxRequestBytes != 2048 {
		t.Errorf("max_request_bytes = %d, want 2048 (file)", c.Daemon.MaxRequestBytes)
	}
}

func TestWorkStealingDefaults(t *testing.T) {
	c := Default()
	if !c.Download.WorkStealing {
		t.Error("default work_stealing = false, want true")
	}
	if c.Download.MaxSegments != 64 {
		t.Errorf("default max_segments = %d, want 64", c.Download.MaxSegments)
	}
	if c.Download.MinStealSize != 1<<20 {
		t.Errorf("default min_steal_size = %d, want %d", c.Download.MinStealSize, 1<<20)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("default config is invalid: %v", err)
	}
}

// TestWorkStealingDefaultsTrueWhenFileOmitsKey guards the default-true toggle: the
// TOML decoder must leave work_stealing at the Default() value when the key is
// absent from the file, not reset it to the bool zero value.
func TestWorkStealingDefaultsTrueWhenFileOmitsKey(t *testing.T) {
	path := writeTempConfig(t, "[download]\nmax_concurrent = 6\n")
	t.Setenv("GIDM_CONFIG", path)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.Download.WorkStealing {
		t.Error("work_stealing = false after a file that omits the key, want true (default preserved)")
	}
}

func TestWorkStealingLayering(t *testing.T) {
	path := writeTempConfig(t, "[download]\nwork_stealing = true\nmax_segments = 16\n")
	t.Setenv("GIDM_CONFIG", path)
	t.Setenv("GIDM_DOWNLOAD_WORK_STEALING", "false") // env overrides file
	t.Setenv("GIDM_DOWNLOAD_MIN_STEAL_SIZE", "2097152")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Download.WorkStealing {
		t.Error("work_stealing = true, want false (env overrides file)")
	}
	if c.Download.MaxSegments != 16 {
		t.Errorf("max_segments = %d, want 16 (file)", c.Download.MaxSegments)
	}
	if c.Download.MinStealSize != 2097152 {
		t.Errorf("min_steal_size = %d, want 2097152 (env)", c.Download.MinStealSize)
	}
}

func TestWorkStealingValidation(t *testing.T) {
	t.Run("max_segments < 1 when enabled", func(t *testing.T) {
		c := Default()
		c.Download.MaxSegments = 0
		if err := c.Validate(); err == nil {
			t.Fatal("Validate: expected error for max_segments = 0, got nil")
		}
	})
	t.Run("min_steal_size < 1 when enabled", func(t *testing.T) {
		c := Default()
		c.Download.MinStealSize = 0
		if err := c.Validate(); err == nil {
			t.Fatal("Validate: expected error for min_steal_size = 0, got nil")
		}
	})
	t.Run("unchecked when disabled", func(t *testing.T) {
		c := Default()
		c.Download.WorkStealing = false
		c.Download.MaxSegments = 0
		c.Download.MinStealSize = 0
		if err := c.Validate(); err != nil {
			t.Errorf("Validate: work-stealing knobs should be unchecked when disabled, got %v", err)
		}
	})
}

func TestDaemonTunablesValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"read_timeout <= 0", func(c *Config) { c.Daemon.ReadTimeout = 0 }},
		{"write_timeout <= 0", func(c *Config) { c.Daemon.WriteTimeout = 0 }},
		{"shutdown_timeout <= 0", func(c *Config) { c.Daemon.ShutdownTimeout = 0 }},
		{"max_request_bytes < 1", func(c *Config) { c.Daemon.MaxRequestBytes = 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mutate(c)
			if err := c.Validate(); err == nil {
				t.Fatalf("Validate: expected error for %s, got nil", tc.name)
			}
		})
	}
}
