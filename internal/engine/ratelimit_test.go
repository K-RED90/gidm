package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// fakeClock is a deterministic clock: now only moves when sleep advances it, so a
// throttle test asserts exact wait durations with no real waiting. It is safe for
// the concurrent limiter (sleep/now are mutex-guarded).
type fakeClock struct {
	mu      sync.Mutex
	t       time.Time
	slept   time.Duration
	origin  time.Time
	advance bool // when false, sleep does not move time (forces a re-check loop)
}

func newFakeClock() *fakeClock {
	base := time.Unix(0, 0)
	return &fakeClock{t: base, origin: base, advance: true}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.advance {
		c.t = c.t.Add(d)
	}
	c.slept += d
	c.mu.Unlock()
	return nil
}

func (c *fakeClock) elapsed() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t.Sub(c.origin)
}

func TestRateLimiterImmediateWithinBurst(t *testing.T) {
	clk := newFakeClock()
	l := newRateLimiterClock(1000, 1000, clk) // 1000 B/s, burst 1000, starts full

	if err := l.WaitN(context.Background(), 1000); err != nil {
		t.Fatalf("WaitN within burst: %v", err)
	}
	if clk.elapsed() != 0 {
		t.Errorf("clock advanced %v admitting a full burst, want 0", clk.elapsed())
	}
}

func TestRateLimiterBlocksExactDuration(t *testing.T) {
	clk := newFakeClock()
	l := newRateLimiterClock(1000, 1000, clk) // 1000 B/s

	if err := l.WaitN(context.Background(), 1000); err != nil { // drain the bucket
		t.Fatalf("drain: %v", err)
	}
	// 500 more bytes at 1000 B/s must take exactly 0.5s of virtual time.
	if err := l.WaitN(context.Background(), 500); err != nil {
		t.Fatalf("WaitN past burst: %v", err)
	}
	if got, want := clk.elapsed(), 500*time.Millisecond; got != want {
		t.Errorf("elapsed = %v, want %v", got, want)
	}
}

func TestRateLimiterNeverOverGrants(t *testing.T) {
	clk := newFakeClock()
	const rate, burst = int64(1000), int64(1000)
	l := newRateLimiterClock(rate, burst, clk)

	granted := int64(0)
	for range 20 {
		if err := l.WaitN(context.Background(), 300); err != nil {
			t.Fatalf("WaitN: %v", err)
		}
		granted += 300
	}
	// Tokens handed out can never exceed the initial burst plus what the rate accrued
	// over the elapsed (virtual) time.
	maxGrant := float64(burst) + float64(rate)*clk.elapsed().Seconds()
	if float64(granted) > maxGrant+1e-6 {
		t.Errorf("granted %d bytes, but cap allows at most %.0f over %v", granted, maxGrant, clk.elapsed())
	}
}

func TestRateLimiterCtxCancelMidWait(t *testing.T) {
	clk := newFakeClock()
	l := newRateLimiterClock(1000, 1000, clk)
	if err := l.WaitN(context.Background(), 1000); err != nil { // drain
		t.Fatalf("drain: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.WaitN(ctx, 500); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitN on cancelled ctx = %v, want context.Canceled", err)
	}
	// No tokens were consumed by the cancelled wait: a fresh full refill still admits
	// a whole burst at once.
	clk.mu.Lock()
	clk.t = clk.t.Add(time.Second) // accrue a full bucket
	clk.mu.Unlock()
	if err := l.WaitN(context.Background(), 1000); err != nil {
		t.Errorf("WaitN after cancel did not find tokens intact: %v", err)
	}
}

func TestRateLimiterWaitNNonPositive(t *testing.T) {
	clk := newFakeClock()
	l := newRateLimiterClock(1000, 1000, clk)
	if err := l.WaitN(context.Background(), 0); err != nil {
		t.Errorf("WaitN(0): %v", err)
	}
	if err := l.WaitN(context.Background(), -5); err != nil {
		t.Errorf("WaitN(-5): %v", err)
	}
	if clk.elapsed() != 0 {
		t.Errorf("WaitN of non-positive n advanced the clock by %v", clk.elapsed())
	}
}

func TestRateLimiterConcurrent(t *testing.T) {
	// Real clock, high rate: this only proves the shared bucket is race-free and never
	// deadlocks under many concurrent callers (run with -race).
	l := newRateLimiter(64<<20, 64<<20)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				if err := l.WaitN(context.Background(), 4096); err != nil {
					t.Errorf("WaitN: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestRateBurstFloorAndOverride(t *testing.T) {
	const buf = 64 * 1024
	floor := int64(2 * buf)

	// Tiny rate: rate/10 is below the floor, so the floor (deadlock invariant) wins.
	if got := rateBurst(100, buf, 0); got != floor {
		t.Errorf("rateBurst(tiny) = %d, want floor %d", got, floor)
	}
	// Large rate: rate/10 (~100ms of bandwidth) exceeds the floor and is used.
	if got, want := rateBurst(64<<20, buf, 0), int64(64<<20)/10; got != want {
		t.Errorf("rateBurst(large) = %d, want %d", got, want)
	}
	// Explicit override above the floor is honored verbatim.
	if got := rateBurst(100, buf, 500_000); got != 500_000 {
		t.Errorf("rateBurst(override) = %d, want 500000", got)
	}
	// Explicit override below the floor is still floored (never below one buffer).
	if got := rateBurst(100, buf, 1000); got != floor {
		t.Errorf("rateBurst(small override) = %d, want floor %d", got, floor)
	}
}

// TestProgressWriterWriteZeroAllocsLimited proves the limited flush boundary stays
// allocation-free on the fast path (rate high enough that WaitN never sleeps),
// mirroring TestProgressWriterWriteZeroAllocsStealing for the unlimited path.
func TestProgressWriterWriteZeroAllocsLimited(t *testing.T) {
	seg := []Segment{{Index: 0, Start: 0, End: 1 << 62}}
	huge := newRateLimiter(1<<50, 1<<50) // never throttles within the run

	cases := map[string]*progressWriter{
		"global only": {
			dst:           nopWriter{},
			prog:          newSegProgressSized(seg, 1),
			plan:          newLivePlan(seg, 1),
			globalLimiter: huge,
			nextFlush:     time.Now().Add(time.Hour),
		},
		"both buckets": {
			dst:           nopWriter{},
			prog:          newSegProgressSized(seg, 1),
			plan:          newLivePlan(seg, 1),
			limiter:       newRateLimiter(1<<50, 1<<50),
			globalLimiter: huge,
			nextFlush:     time.Now().Add(time.Hour),
		},
	}
	buf := make([]byte, 64*1024)
	for name, pw := range cases {
		allocs := testing.AllocsPerRun(200, func() {
			if _, err := pw.Write(buf); err != nil {
				t.Fatalf("%s: Write: %v", name, err)
			}
		})
		if allocs != 0 {
			t.Errorf("%s: progressWriter.Write allocations = %v per run, want 0", name, allocs)
		}
	}
}

// TestProgressWriterThrottlesBothBuckets proves a flush is gated by BOTH the
// per-download and global buckets: each has its own fake clock, and both advance.
// BenchmarkProgressWriterWriteLimited is the -benchmem proof that the limited flush
// boundary allocates nothing on the fast path: the rate/burst are large enough that
// WaitN consumes from the bucket without ever sleeping. Mirrors
// BenchmarkProgressWriterWriteStealing (the unlimited path) with both buckets set.
func BenchmarkProgressWriterWriteLimited(b *testing.B) {
	seg := []Segment{{Index: 0, Start: 0, End: 1 << 62}}
	pw := &progressWriter{
		dst:           nopWriter{},
		ctx:           context.Background(),
		prog:          newSegProgressSized(seg, 1),
		plan:          newLivePlan(seg, 1),
		limiter:       newRateLimiter(1<<50, 1<<50),
		globalLimiter: newRateLimiter(1<<50, 1<<50),
		nextFlush:     time.Now().Add(time.Hour),
	}
	buf := make([]byte, 64*1024)
	b.SetBytes(int64(len(buf)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := pw.Write(buf); err != nil {
			b.Fatalf("Write: %v", err)
		}
	}
}

func TestProgressWriterThrottlesBothBuckets(t *testing.T) {
	seg := []Segment{{Index: 0, Start: 0, End: 1 << 62}}
	dlClk, gClk := newFakeClock(), newFakeClock()
	pw := &progressWriter{
		dst:           nopWriter{},
		ctx:           context.Background(),
		prog:          newSegProgressSized(seg, 1),
		plan:          newLivePlan(seg, 1),
		limiter:       newRateLimiterClock(1000, 2000, dlClk), // per-download: 1000 B/s
		globalLimiter: newRateLimiterClock(500, 2000, gClk),   // global: 500 B/s
		nextFlush:     time.Now().Add(time.Hour),
	}
	buf := make([]byte, 1000)
	for range 10 { // 10_000 bytes, well past both 2000-byte bursts
		if _, err := pw.Write(buf); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if dlClk.elapsed() == 0 {
		t.Error("per-download bucket never throttled")
	}
	if gClk.elapsed() == 0 {
		t.Error("global bucket never throttled")
	}
	// The slower (global) bucket gates the aggregate, so it must wait at least as long
	// as the faster per-download one.
	if gClk.elapsed() < dlClk.elapsed() {
		t.Errorf("global elapsed %v < per-download %v; the slower cap should dominate", gClk.elapsed(), dlClk.elapsed())
	}
}

func TestCappedDownloadCompletes(t *testing.T) {
	content := makeContent(1 << 20) // 1 MiB across several segments
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, filename: "capped.bin"}
	cfg := smallDownloadCfg()
	cfg.MaxRate = 8 << 20            // 8 MiB/s global
	cfg.PerDownloadMaxRate = 4 << 20 // 4 MiB/s per download
	e, _, dir := newEngine(t, f, cfg)

	dl, err := e.Download(context.Background(), "https://example.com/capped.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if dl.Status != StatusCompleted {
		t.Errorf("status = %q, want completed", dl.Status)
	}
	got, err := os.ReadFile(filepath.Join(dir, "capped.bin"))
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("capped download bytes do not match source")
	}
}

func TestCappedDownloadTracksRate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping wall-clock rate check in -short mode")
	}
	const size = 512 << 10 // 512 KiB
	const rate = 1 << 20   // 1 MiB/s per download
	content := makeContent(size)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, filename: "rate.bin"}
	cfg := smallDownloadCfg()
	cfg.PerDownloadMaxRate = rate
	e, _, _ := newEngine(t, f, cfg)

	start := time.Now()
	if _, err := e.Download(context.Background(), "https://example.com/rate.bin"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	elapsed := time.Since(start)

	// Burst lets ~one bucket of bytes through instantly; the rest is paced at rate.
	burst := rateBurst(rate, cfg.BufferSize, int64(cfg.RateBurst))
	paced := float64(size-burst) / float64(rate)
	lower := time.Duration(paced * 0.5 * float64(time.Second)) // generous margin against flakiness
	if elapsed < lower {
		t.Errorf("download took %v, want >= %v for a %d B/s cap", elapsed, lower, rate)
	}
	t.Logf("capped %d bytes at %d B/s in %v (~%.0f KiB/s effective)", size, rate, elapsed, float64(size)/elapsed.Seconds()/1024)
}

func TestCappedConcurrentDownloadsShareGlobalBucket(t *testing.T) {
	// Two downloads run concurrently on one engine: the global bucket is shared
	// (engine-scoped), each download owns a separate per-download bucket. Both must
	// complete with correct bytes and without deadlocking on the shared bucket.
	content := makeContent(512 << 10)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true} // empty name => dest from URL
	cfg := smallDownloadCfg()
	cfg.MaxRate = 16 << 20           // shared global cap
	cfg.PerDownloadMaxRate = 8 << 20 // independent per-download caps
	e, _, dir := newEngine(t, f, cfg)

	urls := []string{"https://example.com/a.bin", "https://example.com/b.bin"}
	errs := make([]error, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			_, errs[i] = e.Download(context.Background(), u)
		}(i, u)
	}
	wg.Wait()

	for i, u := range urls {
		if errs[i] != nil {
			t.Fatalf("Download %s: %v", u, errs[i])
		}
	}
	for _, name := range []string{"a.bin", "b.bin"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if sha256Hex(got) != sha256Hex(content) {
			t.Errorf("%s bytes do not match source", name)
		}
	}
}

func TestCappedDownloadAbortsOnCancel(t *testing.T) {
	content := makeContent(8 << 20)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, filename: "slow.bin"}
	cfg := smallDownloadCfg()
	cfg.MaxRate = 16 << 10 // 16 KiB/s: workers park in WaitN almost immediately
	e, _, dir := newEngine(t, f, cfg)

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := e.Download(ctx, "https://example.com/slow.bin"); done <- err }()

	time.Sleep(50 * time.Millisecond) // let workers drain the burst and block on tokens
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("throttled download did not abort promptly after cancellation")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "slow.bin")); !os.IsNotExist(statErr) {
		t.Errorf("final file present after cancellation: %v", statErr)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutines did not settle: before=%d after=%d", before, runtime.NumGoroutine())
}
