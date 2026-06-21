// Package config resolves gidm's configuration by layering, lowest priority
// first: built-in defaults -> TOML file -> environment (GIDM_*) -> flags.
//
// Load resolves the defaults/file/env layers. Flags are binary-specific, so each
// binary applies them last by mutating the returned Config.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
)

const defaultUserAgent = "gidm/0.1 (+https://github.com/K-RED90/gidm)"

type Config struct {
	Download Download `toml:"download"`
	Network  Network  `toml:"network"`
	Storage  Storage  `toml:"storage"`
	Daemon   Daemon   `toml:"daemon"`
	Paths    Paths    `toml:"paths"`
	Log      Log      `toml:"log"`
}

type Download struct {
	MaxConcurrent       int      `toml:"max_concurrent"`
	SegmentsPerDownload int      `toml:"segments_per_download"`
	BufferSize          int      `toml:"buffer_size"`
	Timeout             Duration `toml:"timeout"`
	MaxRetries          int      `toml:"max_retries"`
	RetryBackoff        Duration `toml:"retry_backoff"`
}

type Network struct {
	ProxyURL      string `toml:"proxy_url"`
	UserAgent     string `toml:"user_agent"`
	TLSSkipVerify bool   `toml:"tls_skip_verify"`
}

type Storage struct {
	DBPath string `toml:"db_path"`
}

type Daemon struct {
	SocketPath string `toml:"socket_path"`
}

type Paths struct {
	DownloadDir string `toml:"download_dir"`
}

type Log struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
}

func Default() *Config {
	c := &Config{
		Download: Download{
			MaxConcurrent:       4,
			SegmentsPerDownload: 8,
			BufferSize:          64 * 1024,
			Timeout:             Duration(30 * time.Second),
			MaxRetries:          5,
			RetryBackoff:        Duration(2 * time.Second),
		},
		Network: Network{UserAgent: defaultUserAgent},
		Log:     Log{Level: "info", Format: "text"},
	}
	c.fillComputedDefaults()
	return c
}

func Load() (*Config, error) {
	return load(configFilePath())
}

// LoadFrom reads the config file from an explicit path; an empty path skips the
// file layer.
func LoadFrom(path string) (*Config, error) {
	return load(path)
}

func load(path string) (*Config, error) {
	c := Default()
	if path != "" {
		if err := loadFile(c, path); err != nil {
			return nil, err
		}
	}
	if err := applyEnv(c); err != nil {
		return nil, err
	}
	c.fillComputedDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func loadFile(c *Config, path string) error {
	md, err := toml.DecodeFile(path, c)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("config: load %q: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return fmt.Errorf("config: unknown keys in %q: %v", path, undecoded)
	}
	return nil
}

func applyEnv(c *Config) error {
	var errs []error
	str := func(key string, dst *string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = v
		}
	}
	num := func(key string, dst *int) {
		if v, ok := os.LookupEnv(key); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("config: env %s: %w", key, err))
				return
			}
			*dst = n
		}
	}
	boolean := func(key string, dst *bool) {
		if v, ok := os.LookupEnv(key); ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("config: env %s: %w", key, err))
				return
			}
			*dst = b
		}
	}
	dur := func(key string, dst *Duration) {
		if v, ok := os.LookupEnv(key); ok {
			d, err := time.ParseDuration(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("config: env %s: %w", key, err))
				return
			}
			*dst = Duration(d)
		}
	}

	num("GIDM_DOWNLOAD_MAX_CONCURRENT", &c.Download.MaxConcurrent)
	num("GIDM_DOWNLOAD_SEGMENTS_PER_DOWNLOAD", &c.Download.SegmentsPerDownload)
	num("GIDM_DOWNLOAD_BUFFER_SIZE", &c.Download.BufferSize)
	dur("GIDM_DOWNLOAD_TIMEOUT", &c.Download.Timeout)
	num("GIDM_DOWNLOAD_MAX_RETRIES", &c.Download.MaxRetries)
	dur("GIDM_DOWNLOAD_RETRY_BACKOFF", &c.Download.RetryBackoff)

	str("GIDM_NETWORK_PROXY_URL", &c.Network.ProxyURL)
	str("GIDM_NETWORK_USER_AGENT", &c.Network.UserAgent)
	boolean("GIDM_NETWORK_TLS_SKIP_VERIFY", &c.Network.TLSSkipVerify)

	str("GIDM_STORAGE_DB_PATH", &c.Storage.DBPath)
	str("GIDM_DAEMON_SOCKET_PATH", &c.Daemon.SocketPath)
	str("GIDM_PATHS_DOWNLOAD_DIR", &c.Paths.DownloadDir)

	str("GIDM_LOG_LEVEL", &c.Log.Level)
	str("GIDM_LOG_FORMAT", &c.Log.Format)

	return errors.Join(errs...)
}

// fillComputedDefaults fills empty path/string fields with computed defaults, so
// an empty value always means "use the default".
func (c *Config) fillComputedDefaults() {
	dataDir := defaultDataDir()
	if c.Storage.DBPath == "" {
		c.Storage.DBPath = filepath.Join(dataDir, "gidm.db")
	}
	if c.Daemon.SocketPath == "" {
		c.Daemon.SocketPath = filepath.Join(dataDir, "gidmd.sock")
	}
	if c.Paths.DownloadDir == "" {
		c.Paths.DownloadDir = defaultDownloadDir()
	}
	if c.Network.UserAgent == "" {
		c.Network.UserAgent = defaultUserAgent
	}
}

func (c *Config) Validate() error {
	var errs []error
	if c.Download.MaxConcurrent < 1 {
		errs = append(errs, errors.New("download.max_concurrent must be >= 1"))
	}
	if c.Download.SegmentsPerDownload < 1 {
		errs = append(errs, errors.New("download.segments_per_download must be >= 1"))
	}
	if c.Download.BufferSize < 1 {
		errs = append(errs, errors.New("download.buffer_size must be >= 1"))
	}
	if c.Download.Timeout <= 0 {
		errs = append(errs, errors.New("download.timeout must be > 0"))
	}
	if c.Download.MaxRetries < 0 {
		errs = append(errs, errors.New("download.max_retries must be >= 0"))
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level %q is invalid (want debug|info|warn|error)", c.Log.Level))
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("log.format %q is invalid (want text|json)", c.Log.Format))
	}
	return errors.Join(errs...)
}

func configFilePath() string {
	if p := os.Getenv("GIDM_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "gidm", "config.toml")
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "gidm")
	}
	return "gidm"
}

func defaultDownloadDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Downloads")
	}
	return "downloads"
}
