package engine

import (
	"sync"
	"sync/atomic"

	"github.com/K-RED90/gidm/internal/config"
)

// Engine turns URLs into bytes on disk: it probes a URL, plans byte-range
// segments, transfers them concurrently, resumes from persisted progress,
// retries, and verifies integrity. It reaches the network only through Fetcher
// and persistence only through Store, so it imports no adapter.
type Engine struct {
	cfg     config.Download
	fetcher Fetcher
	store   Store

	// observer is an optional live-progress hook a Manager registers once at
	// construction (before any transfer runs), so it is read-only thereafter and
	// needs no lock. nil for the standalone Download path.
	observer progressObserver

	// bufPool hands out reusable transfer buffers sized cfg.BufferSize. Pooling
	// keeps the per-chunk copy loop allocation-free (CLAUDE.md principle 2).
	bufPool sync.Pool

	// Runtime-tunable settings. The daemon mutates these live (via the settings
	// API) while transfers run, so each is an atomic: a concurrent Set never races
	// a Submit or an in-flight flush. The non-tunable knobs stay in cfg (read-only
	// after New). See settings.go for the get/apply surface.
	//
	// globalLimiter caps total bandwidth across every segment of every download; a
	// nil pointer (the zero value) means "unlimited", so the flush boundary takes no
	// lock and allocates nothing. It is loaded per flush, so SetGlobalRate throttles
	// in-flight transfers at once.
	globalLimiter      atomic.Pointer[rateLimiter]
	downloadDir        atomic.Pointer[string]
	defaultSegments    atomic.Int64
	perDownloadMaxRate atomic.Int64
	defaultPriority    atomic.Int64

	// activeLimiters maps a running download's ID to its per-download limiter
	// pointer, so Manager.SetRate can swap the cap of a live transfer. Populated for
	// the run's duration in transfer(); a cold path (one insert/delete per run, one
	// lookup per set-rate), never the per-flush loop.
	activeLimiters sync.Map // string -> *atomic.Pointer[rateLimiter]
}

// New builds an Engine from the resolved config, an HTTP fetcher, and a store.
// It reads cfg.Download for tunables and cfg.Paths.DownloadDir for the
// destination directory. The runtime-tunable subset is seeded here and may later
// be overridden live (the daemon overlays persisted settings after construction).
func New(cfg config.Config, fetcher Fetcher, store Store) *Engine {
	bufSize := cfg.Download.BufferSize
	if bufSize < 1 {
		bufSize = defaultBufferSize
	}
	e := &Engine{
		cfg:     cfg.Download,
		fetcher: fetcher,
		store:   store,
	}
	e.bufPool.New = func() any {
		b := make([]byte, bufSize)
		return &b
	}

	dir := cfg.Paths.DownloadDir
	e.downloadDir.Store(&dir)
	e.defaultSegments.Store(int64(cfg.Download.SegmentsPerDownload))
	e.perDownloadMaxRate.Store(int64(cfg.Download.PerDownloadMaxRate))
	p, _ := ParsePriority(cfg.Download.DefaultPriority) // invalid → PriorityNormal (0)
	e.defaultPriority.Store(int64(p))
	if rate := int64(cfg.Download.MaxRate); rate > 0 {
		e.globalLimiter.Store(newRateLimiter(rate, rateBurst(rate, bufSize, int64(cfg.Download.RateBurst))))
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
