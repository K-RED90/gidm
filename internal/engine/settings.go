package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"
)

// Settings is the runtime-tunable subset of configuration the daemon exposes for
// live changes. The values are the current effective in-memory settings; MaxRate
// and PerDownloadMaxRate are bytes/sec (0 = unlimited). The non-tunable engine
// internals (buffer size, retries, timeouts, work-stealing, max-concurrent) are
// deliberately not represented here — they stay config-file-only.
type Settings struct {
	DownloadDir         string
	SegmentsPerDownload int
	DefaultPriority     Priority
	MaxRate             int // engine-wide cap; 0 = unlimited
	PerDownloadMaxRate  int // default per-download cap; 0 = unlimited
}

// Setting keys persisted in the store's settings table. They mirror the config
// field paths so the persisted layer reads the same as the TOML it overrides.
const (
	settingDownloadDir         = "paths.download_dir"
	settingSegmentsPerDownload = "download.segments_per_download"
	settingDefaultPriority     = "download.default_priority"
	settingMaxRate             = "download.max_rate"
	settingPerDownloadMaxRate  = "download.per_download_max_rate"
)

// --- Engine runtime getters/setters (atomic; safe during active transfers) ---

// dlDir reports the effective download directory.
func (e *Engine) dlDir() string {
	if p := e.downloadDir.Load(); p != nil {
		return *p
	}
	return ""
}

// defaultSegmentCount reports the effective default segment count for a download
// that does not override it.
func (e *Engine) defaultSegmentCount() int { return int(e.defaultSegments.Load()) }

// DefaultPriority reports the priority applied to an add that omits one.
func (e *Engine) DefaultPriority() Priority { return Priority(e.defaultPriority.Load()) }

// effectiveDownloadRate resolves a download's record-level MaxRate against the
// engine default: a positive record cap wins, else the per-download default
// applies (which may itself be 0 = unlimited).
func (e *Engine) effectiveDownloadRate(recordMaxRate int) int {
	if recordMaxRate > 0 {
		return recordMaxRate
	}
	return int(e.perDownloadMaxRate.Load())
}

// Settings returns the current effective runtime-tunable settings.
func (e *Engine) Settings() Settings {
	s := Settings{
		DownloadDir:         e.dlDir(),
		SegmentsPerDownload: int(e.defaultSegments.Load()),
		DefaultPriority:     Priority(e.defaultPriority.Load()),
		PerDownloadMaxRate:  int(e.perDownloadMaxRate.Load()),
	}
	// rate is immutable for a limiter instance (SetGlobalRate swaps the whole
	// pointer), so reading it after the atomic load is race-free.
	if l := e.globalLimiter.Load(); l != nil {
		s.MaxRate = int(l.rate)
	}
	return s
}

// SetDownloadDir overrides the destination directory for new downloads. An empty
// dir is ignored so a partial update never blanks it.
func (e *Engine) SetDownloadDir(dir string) {
	if dir != "" {
		e.downloadDir.Store(&dir)
	}
}

// SetDefaultSegments overrides the default segment count for new downloads. A
// non-positive value is ignored.
func (e *Engine) SetDefaultSegments(n int) {
	if n > 0 {
		e.defaultSegments.Store(int64(n))
	}
}

// SetDefaultPriority overrides the priority applied to adds that omit one. An
// invalid priority is ignored.
func (e *Engine) SetDefaultPriority(p Priority) {
	if p.Valid() {
		e.defaultPriority.Store(int64(p))
	}
}

// SetPerDownloadRate sets the default per-download bandwidth cap (bytes/sec;
// <= 0 = unlimited) for downloads without a record-level override. It takes
// effect on a download's next run, not mid-flight.
func (e *Engine) SetPerDownloadRate(bps int) {
	if bps < 0 {
		bps = 0
	}
	e.perDownloadMaxRate.Store(int64(bps))
}

// SetGlobalRate sets (or clears) the engine-wide bandwidth cap at runtime. bps
// <= 0 removes the cap: the per-flush atomic load then sees nil and takes the
// zero-lock fast path. Safe to call concurrently with active transfers, which
// pick up the new bucket on their next flush.
func (e *Engine) SetGlobalRate(bps int) {
	if bps <= 0 {
		e.globalLimiter.Store(nil)
		return
	}
	rate := int64(bps)
	e.globalLimiter.Store(newRateLimiter(rate, rateBurst(rate, e.bufSize(), int64(e.cfg.RateBurst))))
}

// SetDownloadRate swaps the per-download cap of a currently running download
// live, resolving recordMaxRate through effectiveDownloadRate (0 falls back to
// the engine default, which may be unlimited). A download that is not active right
// now is a no-op here — its persisted Download.MaxRate is applied when its run
// next starts (see transfer in download_run.go).
func (e *Engine) SetDownloadRate(id string, recordMaxRate int) {
	v, ok := e.activeLimiters.Load(id)
	if !ok {
		return
	}
	lp := v.(*atomic.Pointer[rateLimiter])
	rate := int64(e.effectiveDownloadRate(recordMaxRate))
	if rate <= 0 {
		lp.Store(nil)
		return
	}
	lp.Store(newRateLimiter(rate, rateBurst(rate, e.bufSize(), int64(e.cfg.RateBurst))))
}

// ApplySettings applies the full runtime-tunable set live, in one call. Used by
// the daemon's set-config path and the startup overlay.
func (e *Engine) ApplySettings(s Settings) {
	e.SetDownloadDir(s.DownloadDir)
	e.SetDefaultSegments(s.SegmentsPerDownload)
	e.SetDefaultPriority(s.DefaultPriority)
	e.SetGlobalRate(s.MaxRate)
	e.SetPerDownloadRate(s.PerDownloadMaxRate)
}

// --- Manager settings surface (live apply + persistence) ---

// Settings returns the daemon's current effective runtime settings.
func (m *Manager) Settings() Settings { return m.engine.Settings() }

// SetSettings applies s live and persists every field so the change survives a
// restart. The live apply happens first and cannot fail; a persist error is
// returned so the caller learns the change will not outlive the process, even
// though it is already in effect.
func (m *Manager) SetSettings(ctx context.Context, s Settings) error {
	m.engine.ApplySettings(s)
	kv := []struct{ key, value string }{
		{settingDownloadDir, s.DownloadDir},
		{settingSegmentsPerDownload, strconv.Itoa(s.SegmentsPerDownload)},
		{settingDefaultPriority, s.DefaultPriority.String()},
		{settingMaxRate, strconv.Itoa(s.MaxRate)},
		{settingPerDownloadMaxRate, strconv.Itoa(s.PerDownloadMaxRate)},
	}
	for _, kv := range kv {
		if err := m.store.SetSetting(ctx, kv.key, kv.value); err != nil {
			return fmt.Errorf("engine: manager set settings: persist %q: %w", kv.key, err)
		}
	}
	return nil
}

// LoadSettings overlays persisted settings onto the config-seeded runtime state,
// so a value the user changed via the API wins over the config file on the next
// boot. A key never set falls back to config (ErrNotFound → skip); a malformed
// persisted value is logged and skipped rather than failing the boot. Call it
// once at startup, before Start.
func (m *Manager) LoadSettings(ctx context.Context) error {
	cur := m.engine.Settings()

	if v, ok := m.loadSetting(ctx, settingDownloadDir); ok && v != "" {
		cur.DownloadDir = v
	}
	if v, ok := m.loadSetting(ctx, settingSegmentsPerDownload); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cur.SegmentsPerDownload = n
		} else {
			m.logger.Warn("engine: manager: ignoring bad persisted setting", "key", settingSegmentsPerDownload, "value", v)
		}
	}
	if v, ok := m.loadSetting(ctx, settingDefaultPriority); ok {
		if p, err := ParsePriority(v); err == nil {
			cur.DefaultPriority = p
		} else {
			m.logger.Warn("engine: manager: ignoring bad persisted setting", "key", settingDefaultPriority, "value", v)
		}
	}
	if v, ok := m.loadSetting(ctx, settingMaxRate); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cur.MaxRate = n
		} else {
			m.logger.Warn("engine: manager: ignoring bad persisted setting", "key", settingMaxRate, "value", v)
		}
	}
	if v, ok := m.loadSetting(ctx, settingPerDownloadMaxRate); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cur.PerDownloadMaxRate = n
		} else {
			m.logger.Warn("engine: manager: ignoring bad persisted setting", "key", settingPerDownloadMaxRate, "value", v)
		}
	}

	m.engine.ApplySettings(cur)
	return nil
}

// loadSetting reads one persisted setting, distinguishing "unset" (ErrNotFound,
// ok=false, silent) from a real store error (logged, ok=false).
func (m *Manager) loadSetting(ctx context.Context, key string) (string, bool) {
	v, err := m.store.GetSetting(ctx, key)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			m.logger.Error("engine: manager: read persisted setting", "key", key, "err", err)
		}
		return "", false
	}
	return v, true
}

// SetRate sets a download's per-download bandwidth cap (bytes/sec; 0 removes the
// record-level cap, falling back to the engine default), persisting it on the
// record and applying it live if the download is currently running. Like
// SetPriority, the persisted record is authoritative: a cap set on a running
// download takes effect at once via its registered limiter, and is re-applied
// from the record whenever the download next (re)starts.
func (m *Manager) SetRate(ctx context.Context, id string, bps int) error {
	if bps < 0 {
		bps = 0
	}
	d, err := m.store.LoadDownload(ctx, id)
	if err != nil {
		return fmt.Errorf("engine: manager set-rate %q: %w", id, err)
	}
	d.MaxRate = bps
	d.UpdatedAt = time.Now().UTC()
	if err := m.store.SaveDownload(ctx, d); err != nil {
		return fmt.Errorf("engine: manager set-rate persist %q: %w", id, err)
	}
	m.engine.SetDownloadRate(id, bps)
	return nil
}
