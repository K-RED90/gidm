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

	// WorkStealing rebalances a download's byte ranges mid-flight: a worker that
	// finishes its segment steals the unfetched tail of the segment with the most
	// bytes left, so fast connections absorb a straggler's remainder. MaxSegments
	// caps how many segments a download may fragment into; MinStealSize is the
	// floor below which splitting a remainder is not worth the request overhead.
	WorkStealing bool `toml:"work_stealing"`
	MaxSegments  int  `toml:"max_segments"`
	MinStealSize int  `toml:"min_steal_size"`

	// MaxRate caps total download bandwidth across all downloads (bytes/sec); 0 =
	// unlimited. PerDownloadMaxRate caps each individual download the same way.
	// RateBurst overrides the token-bucket capacity (bytes); 0 derives it from the
	// rate. A configured burst is floored so it is never below BufferSize — a smaller
	// burst could never admit one buffer flush and would stall a worker.
	MaxRate            int `toml:"max_rate"`
	PerDownloadMaxRate int `toml:"per_download_max_rate"`
	RateBurst          int `toml:"rate_burst"`
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

	// ReadTimeout/WriteTimeout bound a single request's read and response write
	// so a slow or stuck client never pins a handler goroutine. ShutdownTimeout
	// bounds the drain of in-flight connection goroutines on a graceful stop.
	// MaxRequestBytes caps one request frame so an oversized payload cannot
	// exhaust memory.
	ReadTimeout     Duration `toml:"read_timeout"`
	WriteTimeout    Duration `toml:"write_timeout"`
	ShutdownTimeout Duration `toml:"shutdown_timeout"`
	MaxRequestBytes int      `toml:"max_request_bytes"`

	// DialTimeout bounds the CLI's client-side dial plus one request/response
	// round-trip over the socket. It is distinct from the server-side
	// ReadTimeout/WriteTimeout above (which bound handler I/O): this caps how
	// long `gidm` waits for `gidmd` before giving up.
	DialTimeout Duration `toml:"dial_timeout"`
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
			WorkStealing:        true,
			MaxSegments:         64,
			MinStealSize:        1 << 20, // 1 MiB
		},
		Network: Network{UserAgent: defaultUserAgent},
		Daemon: Daemon{
			ReadTimeout:     Duration(30 * time.Second),
			WriteTimeout:    Duration(10 * time.Second),
			ShutdownTimeout: Duration(10 * time.Second),
			MaxRequestBytes: 1 << 20, // 1 MiB
			DialTimeout:     Duration(10 * time.Second),
		},
		Log: Log{Level: "info", Format: "text"},
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
	boolean("GIDM_DOWNLOAD_WORK_STEALING", &c.Download.WorkStealing)
	num("GIDM_DOWNLOAD_MAX_SEGMENTS", &c.Download.MaxSegments)
	num("GIDM_DOWNLOAD_MIN_STEAL_SIZE", &c.Download.MinStealSize)
	num("GIDM_DOWNLOAD_MAX_RATE", &c.Download.MaxRate)
	num("GIDM_DOWNLOAD_PER_DOWNLOAD_MAX_RATE", &c.Download.PerDownloadMaxRate)
	num("GIDM_DOWNLOAD_RATE_BURST", &c.Download.RateBurst)

	str("GIDM_NETWORK_PROXY_URL", &c.Network.ProxyURL)
	str("GIDM_NETWORK_USER_AGENT", &c.Network.UserAgent)
	boolean("GIDM_NETWORK_TLS_SKIP_VERIFY", &c.Network.TLSSkipVerify)

	str("GIDM_STORAGE_DB_PATH", &c.Storage.DBPath)
	str("GIDM_DAEMON_SOCKET_PATH", &c.Daemon.SocketPath)
	dur("GIDM_DAEMON_READ_TIMEOUT", &c.Daemon.ReadTimeout)
	dur("GIDM_DAEMON_WRITE_TIMEOUT", &c.Daemon.WriteTimeout)
	dur("GIDM_DAEMON_SHUTDOWN_TIMEOUT", &c.Daemon.ShutdownTimeout)
	num("GIDM_DAEMON_MAX_REQUEST_BYTES", &c.Daemon.MaxRequestBytes)
	dur("GIDM_DAEMON_DIAL_TIMEOUT", &c.Daemon.DialTimeout)
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
	if c.Download.WorkStealing {
		if c.Download.MaxSegments < 1 {
			errs = append(errs, errors.New("download.max_segments must be >= 1 when work_stealing is enabled"))
		}
		if c.Download.MinStealSize < 1 {
			errs = append(errs, errors.New("download.min_steal_size must be >= 1 when work_stealing is enabled"))
		}
	}
	if c.Download.MaxRate < 0 {
		errs = append(errs, errors.New("download.max_rate must be >= 0"))
	}
	if c.Download.PerDownloadMaxRate < 0 {
		errs = append(errs, errors.New("download.per_download_max_rate must be >= 0"))
	}
	if c.Download.RateBurst < 0 {
		errs = append(errs, errors.New("download.rate_burst must be >= 0"))
	}
	if c.Download.RateBurst > 0 && c.Download.RateBurst < c.Download.BufferSize {
		errs = append(errs, errors.New("download.rate_burst must be >= download.buffer_size (a smaller burst can never admit one buffer)"))
	}
	if c.Daemon.ReadTimeout <= 0 {
		errs = append(errs, errors.New("daemon.read_timeout must be > 0"))
	}
	if c.Daemon.WriteTimeout <= 0 {
		errs = append(errs, errors.New("daemon.write_timeout must be > 0"))
	}
	if c.Daemon.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("daemon.shutdown_timeout must be > 0"))
	}
	if c.Daemon.MaxRequestBytes < 1 {
		errs = append(errs, errors.New("daemon.max_request_bytes must be >= 1"))
	}
	if c.Daemon.DialTimeout <= 0 {
		errs = append(errs, errors.New("daemon.dial_timeout must be > 0"))
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
