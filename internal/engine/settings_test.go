package engine

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/K-RED90/gidm/internal/config"
)

func TestEngineSettingsApplyAndRead(t *testing.T) {
	var e Engine // bare engine: ApplySettings touches only the runtime atomics
	want := Settings{
		DownloadDir:         "/data",
		SegmentsPerDownload: 6,
		DefaultPriority:     PriorityHigh,
		MaxRate:             1000,
		PerDownloadMaxRate:  500,
	}
	e.ApplySettings(want)

	if got := e.Settings(); got != want {
		t.Fatalf("Settings = %+v, want %+v", got, want)
	}

	// Clearing the global cap drops the limiter back to the zero-lock fast path.
	e.SetGlobalRate(0)
	if e.globalLimiter.Load() != nil {
		t.Error("SetGlobalRate(0) did not clear the global limiter")
	}
	if got := e.Settings().MaxRate; got != 0 {
		t.Errorf("MaxRate after clear = %d, want 0", got)
	}
}

// Persisted settings must survive a fresh engine/manager over the same store and
// override the config-seeded defaults (the user's choices win over the file).
func TestManagerSettingsPersistAcrossReload(t *testing.T) {
	store := newMemStore()
	cfg := config.Config{
		Download: config.Download{SegmentsPerDownload: 8, BufferSize: 64 << 10},
		Paths:    config.Paths{DownloadDir: "/seed"},
	}
	m := NewManager(New(cfg, nil, store), store, 1)

	want := Settings{
		DownloadDir:         "/custom",
		SegmentsPerDownload: 3,
		DefaultPriority:     PriorityLow,
		MaxRate:             2000,
		PerDownloadMaxRate:  1000,
	}
	if err := m.SetSettings(context.Background(), want); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	if got := m.Settings(); got != want {
		t.Fatalf("Settings after set = %+v, want %+v", got, want)
	}

	// A fresh manager over the SAME store re-seeds from cfg, then LoadSettings
	// overlays the persisted values.
	m2 := NewManager(New(cfg, nil, store), store, 1)
	if got := m2.Settings().DownloadDir; got != "/seed" {
		t.Fatalf("pre-load dir = %q, want config seed /seed", got)
	}
	if err := m2.LoadSettings(context.Background()); err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got := m2.Settings(); got != want {
		t.Fatalf("Settings after reload = %+v, want %+v", got, want)
	}
}

// LoadSettings with nothing persisted leaves the config-seeded settings intact.
func TestManagerLoadSettingsEmptyKeepsConfig(t *testing.T) {
	store := newMemStore()
	cfg := config.Config{
		Download: config.Download{SegmentsPerDownload: 8, DefaultPriority: "high"},
		Paths:    config.Paths{DownloadDir: "/seed"},
	}
	m := NewManager(New(cfg, nil, store), store, 1)
	if err := m.LoadSettings(context.Background()); err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	got := m.Settings()
	if got.DownloadDir != "/seed" || got.SegmentsPerDownload != 8 || got.DefaultPriority != PriorityHigh {
		t.Fatalf("Settings = %+v, want config seed", got)
	}
}

func TestManagerSetRatePersistsAndSwapsLive(t *testing.T) {
	f := &fakeFetcher{content: makeContent(1 << 20), supportsRanges: true, sizeKnown: true}
	cfg := smallDownloadCfg()
	m, store, _ := newManager(t, f, cfg, 1) // not started: the record stays queued

	id, err := m.Submit(context.Background(), "https://example.com/d.bin", PriorityNormal, AddOptions{})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Idle download: SetRate persists the record; there is no live limiter to swap.
	if err := m.SetRate(context.Background(), id, 4096); err != nil {
		t.Fatalf("SetRate: %v", err)
	}
	d, err := store.LoadDownload(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if d.MaxRate != 4096 {
		t.Errorf("persisted MaxRate = %d, want 4096", d.MaxRate)
	}

	// Simulate an active run by registering a per-download limiter pointer, then
	// SetRate must swap it live.
	var lp atomic.Pointer[rateLimiter]
	m.engine.activeLimiters.Store(id, &lp)
	if err := m.SetRate(context.Background(), id, 8192); err != nil {
		t.Fatalf("SetRate (active): %v", err)
	}
	if got := lp.Load(); got == nil || int(got.rate) != 8192 {
		t.Fatalf("live limiter rate = %v, want 8192", got)
	}
	// 0 removes the record cap; with no engine default that means unlimited (nil).
	if err := m.SetRate(context.Background(), id, 0); err != nil {
		t.Fatalf("SetRate(0): %v", err)
	}
	if lp.Load() != nil {
		t.Error("SetRate(0) with no default did not clear the live limiter")
	}
}

// A global-rate swap must be observed by the very next flush, since progressWriter
// loads the limiter per flush — this is the "live cap" property.
func TestProgressWriterObservesLiveGlobalSwap(t *testing.T) {
	seg := []Segment{{Index: 0, Start: 0, End: 1 << 62}}
	var holder atomic.Pointer[rateLimiter]
	holder.Store(newRateLimiter(1<<50, 1<<50)) // effectively unlimited

	pw := &progressWriter{
		dst:           nopWriter{},
		ctx:           context.Background(),
		prog:          newSegProgressSized(seg, 1),
		plan:          newLivePlan(seg, 1),
		globalLimiter: &holder,
	}
	buf := make([]byte, 1000)
	if _, err := pw.Write(buf); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Swap in a slow, fake-clocked bucket; the next flushes must throttle against it.
	slow := newFakeClock()
	holder.Store(newRateLimiterClock(500, 1000, slow)) // 500 B/s, burst 1000
	for range 5 {                                      // 5000 bytes, well past the burst
		if _, err := pw.Write(buf); err != nil {
			t.Fatalf("Write after swap: %v", err)
		}
	}
	if slow.elapsed() == 0 {
		t.Error("live swap not observed: the slow bucket never throttled")
	}
}
