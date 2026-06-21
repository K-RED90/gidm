package engine

import (
	"sync"

	"github.com/K-RED90/gidm/internal/config"
)

// Engine turns URLs into bytes on disk: it probes a URL, plans byte-range
// segments, transfers them concurrently, resumes from persisted progress,
// retries, and verifies integrity. It reaches the network only through Fetcher
// and persistence only through Store, so it imports no adapter.
type Engine struct {
	cfg         config.Download
	downloadDir string
	fetcher     Fetcher
	store       Store

	// observer is an optional live-progress hook a Manager registers once at
	// construction (before any transfer runs), so it is read-only thereafter and
	// needs no lock. nil for the standalone Download path.
	observer progressObserver

	// bufPool hands out reusable transfer buffers sized cfg.BufferSize. Pooling
	// keeps the per-chunk copy loop allocation-free (CLAUDE.md principle 2).
	bufPool sync.Pool

	// globalLimiter caps total bandwidth across every segment of every download.
	// nil when no global cap is configured, in which case the flush boundary takes
	// no lock and allocates nothing. Engine-scoped (like store), so it is read here
	// rather than threaded through the run; the per-download cap is threaded instead.
	globalLimiter *rateLimiter
}

// New builds an Engine from the resolved config, an HTTP fetcher, and a store.
// It reads cfg.Download for tunables and cfg.Paths.DownloadDir for the
// destination directory.
func New(cfg config.Config, fetcher Fetcher, store Store) *Engine {
	bufSize := cfg.Download.BufferSize
	if bufSize < 1 {
		bufSize = defaultBufferSize
	}
	e := &Engine{
		cfg:         cfg.Download,
		downloadDir: cfg.Paths.DownloadDir,
		fetcher:     fetcher,
		store:       store,
	}
	e.bufPool.New = func() any {
		b := make([]byte, bufSize)
		return &b
	}
	if rate := int64(cfg.Download.MaxRate); rate > 0 {
		e.globalLimiter = newRateLimiter(rate, rateBurst(rate, bufSize, int64(cfg.Download.RateBurst)))
	}
	return e
}

// bufSize reports the effective transfer-buffer size, matching New's flooring. It
// is the upper bound on a single flush, so the rate limiter's burst is floored to a
// multiple of it (see rateBurst).
func (e *Engine) bufSize() int {
	if e.cfg.BufferSize < 1 {
		return defaultBufferSize
	}
	return e.cfg.BufferSize
}

// Ready reports whether the engine is wired enough to run. Per-download fan-out
// is bounded by cfg.SegmentsPerDownload; MaxConcurrent (cross-download
// concurrency) is M2, checked here only so a misconfigured engine is rejected
// early and for parity with the daemon's readiness gate.
func (e *Engine) Ready() bool {
	return e.store != nil && e.fetcher != nil && e.cfg.MaxConcurrent > 0
}
