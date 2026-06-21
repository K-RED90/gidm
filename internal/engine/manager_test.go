package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
)

// newManager wires a Manager over a fresh engine, memStore, and temp download
// dir, returning the manager, store, and dir. It does not call Start.
func newManager(t *testing.T, f Fetcher, dl config.Download, maxConcurrent int) (*Manager, *memStore, string) {
	t.Helper()
	store := newMemStore()
	dir := t.TempDir()
	cfg := config.Config{Download: dl, Paths: config.Paths{DownloadDir: dir}}
	e := New(cfg, f, store)
	return NewManager(e, store, maxConcurrent), store, dir
}

// waitStatus polls Get until the download reaches want or the deadline passes.
func waitStatus(t *testing.T, m *Manager, id string, want Status, within time.Duration) *Download {
	t.Helper()
	deadline := time.Now().Add(within)
	var last Status
	for time.Now().Before(deadline) {
		d, err := m.Get(context.Background(), id)
		if err == nil {
			last = d.Status
			if d.Status == want {
				return d
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("download %q status = %q, want %q within %s", id, last, want, within)
	return nil
}

func TestManagerSubmitRunsInBackground(t *testing.T) {
	content := makeContent(3 << 20)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "bg.bin"}
	m, store, dir := newManager(t, f, smallDownloadCfg(), 2)

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	start := time.Now()
	id, err := m.Submit(context.Background(), "https://example.com/bg.bin")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("Submit blocked for %s; it must return without waiting on the transfer", elapsed)
	}

	// Queued immediately after submit (it may already be active on a fast machine,
	// which is also fine — the point is the call returned without completing).
	if got, err := m.Get(context.Background(), id); err != nil {
		t.Fatalf("Get: %v", err)
	} else if got.Status == StatusCompleted {
		t.Error("download completed synchronously during Submit")
	}

	done := waitStatus(t, m, id, StatusCompleted, 5*time.Second)
	if done.ID != id {
		t.Errorf("Get returned ID %q, want %q", done.ID, id)
	}

	got, err := os.ReadFile(filepath.Join(dir, "bg.bin"))
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("downloaded bytes do not match source")
	}

	persisted, err := store.LoadDownload(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if persisted.Status != StatusCompleted {
		t.Errorf("persisted status = %q, want completed", persisted.Status)
	}
}

func TestManagerListReflectsLifecycle(t *testing.T) {
	content := makeContent(2 << 20)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "lc.bin"}
	m, _, _ := newManager(t, f, smallDownloadCfg(), 2)

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	id, err := m.Submit(context.Background(), "https://example.com/lc.bin")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitStatus(t, m, id, StatusCompleted, 5*time.Second)

	list, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != id || list[0].Status != StatusCompleted {
		t.Errorf("List = %+v, want one completed download %q", list, id)
	}
}

// gatedFetcher holds every body open until release is closed (or ctx is
// cancelled), recording the peak number of bodies open at once so a test can
// assert the MaxConcurrent cap on active downloads.
type gatedFetcher struct {
	content []byte
	release chan struct{}

	inFlight atomic.Int64
	peak     atomic.Int64

	mu        sync.Mutex
	rangeReqs [][2]int64
}

func (f *gatedFetcher) Probe(_ context.Context, url string) (ProbeInfo, error) {
	// Derive a distinct filename from the URL path so concurrent downloads do not
	// collide on one destination; the suite submits unique URLs per download.
	return ProbeInfo{FinalURL: url, Size: int64(len(f.content)), SupportsRanges: true, ETag: `"v1"`, Filename: baseFromURL(url)}, nil
}

func (f *gatedFetcher) RangeGet(ctx context.Context, _ string, start, end int64) (io.ReadCloser, error) {
	f.mu.Lock()
	f.rangeReqs = append(f.rangeReqs, [2]int64{start, end})
	f.mu.Unlock()
	return f.body(ctx, f.content[start:end+1]), nil
}

func (f *gatedFetcher) Get(ctx context.Context, _ string) (io.ReadCloser, error) {
	return f.body(ctx, f.content), nil
}

func (f *gatedFetcher) body(ctx context.Context, data []byte) io.ReadCloser {
	n := f.inFlight.Add(1)
	for {
		peak := f.peak.Load()
		if n <= peak || f.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	return &gatedBody{f: f, ctx: ctx, release: f.release, data: data}
}

type gatedBody struct {
	f       *gatedFetcher
	ctx     context.Context
	release chan struct{}
	data    []byte
	pos     int
	done    bool
}

func (b *gatedBody) Read(p []byte) (int, error) {
	if b.pos == 0 {
		n := copy(p, b.data[:min(len(p), 32)])
		b.pos += n
		return n, nil
	}
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.release:
		if b.pos >= len(b.data) {
			return 0, io.EOF
		}
		n := copy(p, b.data[b.pos:])
		b.pos += n
		return n, nil
	}
}

func (b *gatedBody) Close() error {
	if !b.done {
		b.done = true
		b.f.inFlight.Add(-1)
	}
	return nil
}

// TestManagerHonorsMaxConcurrent asserts that with N=2 and four submits, at most
// two downloads are active at once. A single-segment plan (SegmentsPerDownload=1)
// keeps one body per download so the peak body count equals the active-download
// count.
func TestManagerHonorsMaxConcurrent(t *testing.T) {
	content := makeContent(64 << 10)
	f := &gatedFetcher{content: content, release: make(chan struct{})}
	cfg := smallDownloadCfg()
	cfg.SegmentsPerDownload = 1 // one body per download -> peak bodies == active downloads
	m, _, _ := newManager(t, f, cfg, 2)

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ids := make([]string, 4)
	for i := range ids {
		url := fmt.Sprintf("https://example.com/gate-%d.bin", i)
		id, err := m.Submit(context.Background(), url)
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
		ids[i] = id
	}

	// Let the workers pick up jobs and block on the gate, then check the peak.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.inFlight.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // give any over-eager 3rd worker a chance to violate the cap

	if peak := f.peak.Load(); peak > 2 {
		t.Errorf("peak concurrent downloads = %d, want <= 2 (MaxConcurrent)", peak)
	}
	if cur := f.inFlight.Load(); cur > 2 {
		t.Errorf("currently active downloads = %d, want <= 2", cur)
	}

	close(f.release) // let everything finish
	for _, id := range ids {
		waitStatus(t, m, id, StatusCompleted, 5*time.Second)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// TestManagerPauseKeepsProgressResumeFinishes pauses a running download mid-flight
// (asserting checkpointed bytes survive and the .part remains), then resumes and
// confirms only the remainder is fetched and the final file is byte-correct.
func TestManagerPauseKeepsProgressResumeFinishes(t *testing.T) {
	const size = 4 << 20
	content := makeContent(size)
	f := &gatedFetcher{content: content, release: make(chan struct{})}
	cfg := smallDownloadCfg()
	cfg.SegmentsPerDownload = 4
	m, store, dir := newManager(t, f, cfg, 2)

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	id, err := m.Submit(context.Background(), "https://example.com/gate.bin")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitStatus(t, m, id, StatusActive, 3*time.Second)

	// Let each segment deliver its first 32-byte chunk (the gate holds the rest).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.inFlight.Load() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)

	if err := m.Pause(context.Background(), id); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	paused := waitStatus(t, m, id, StatusPaused, 3*time.Second)

	var pausedBytes int64
	for _, s := range paused.Segments {
		pausedBytes += s.Completed
	}
	if pausedBytes == 0 {
		t.Error("paused download checkpointed zero bytes; pause should keep progress")
	}
	dest := filepath.Join(dir, "gate.bin")
	if _, err := os.Stat(dest + partSuffix); err != nil {
		t.Errorf(".part missing after pause: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("final file present after pause: %v", err)
	}

	// Snapshot the ranges requested before resume so we can prove the resume
	// re-requests only the unfinished tail (offsets at Start+Completed), never the
	// already-checkpointed prefix.
	f.mu.Lock()
	f.rangeReqs = f.rangeReqs[:0]
	f.mu.Unlock()

	// Unblock all bodies so the resumed run can complete immediately.
	close(f.release)

	if err := m.Resume(context.Background(), id); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	done := waitStatus(t, m, id, StatusCompleted, 5*time.Second)

	// Every resume request must begin at its segment's checkpointed resume offset
	// (planned Start + checkpointed Completed), never at the raw Start, so the
	// already-downloaded prefix is not re-fetched.
	resumeSegs := planSegments(size, cfg.SegmentsPerDownload)
	resumeOffset := make(map[int64]bool, len(resumeSegs))
	for i := range resumeSegs {
		resumeOffset[resumeSegs[i].Start+paused.Segments[i].Completed] = true
	}
	f.mu.Lock()
	resumeReqs := append([][2]int64(nil), f.rangeReqs...)
	f.mu.Unlock()
	for _, r := range resumeReqs {
		if !resumeOffset[r[0]] {
			t.Errorf("resume request %v does not start at any segment's checkpointed offset; the prefix was re-downloaded", r)
		}
	}

	var doneBytes int64
	for _, s := range done.Segments {
		doneBytes += s.Completed
	}
	if doneBytes != size {
		t.Errorf("completed bytes = %d, want %d", doneBytes, size)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("resumed file does not match source")
	}

	persisted, err := store.LoadDownload(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if persisted.Status != StatusCompleted {
		t.Errorf("persisted status = %q, want completed", persisted.Status)
	}
}

// TestManagerCancelStopsAndKeepsPart cancels a running download and asserts it
// transitions to canceled promptly while the .part is left for a later resume.
func TestManagerCancelStopsAndKeepsPart(t *testing.T) {
	const size = 4 << 20
	content := makeContent(size)
	f := &gatedFetcher{content: content, release: make(chan struct{})}
	cfg := smallDownloadCfg()
	cfg.SegmentsPerDownload = 4
	m, store, dir := newManager(t, f, cfg, 2)

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	id, err := m.Submit(context.Background(), "https://example.com/gate.bin")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitStatus(t, m, id, StatusActive, 3*time.Second)
	time.Sleep(50 * time.Millisecond) // let the first chunks land

	start := time.Now()
	if err := m.Cancel(context.Background(), id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitStatus(t, m, id, StatusCanceled, 3*time.Second)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Cancel took %s; expected prompt cancellation", elapsed)
	}

	dest := filepath.Join(dir, "gate.bin")
	if _, err := os.Stat(dest + partSuffix); err != nil {
		t.Errorf(".part removed on cancel; it should be left for a later resume: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("final file present after cancel: %v", err)
	}

	persisted, err := store.LoadDownload(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if persisted.Status != StatusCanceled {
		t.Errorf("persisted status = %q, want canceled", persisted.Status)
	}

	close(f.release)
}

// TestManagerRecoversActiveDownloadOnStart pre-seeds the store with an active,
// partially-complete download plus a matching .part, then Starts the manager and
// asserts it resumes to completion without re-fetching the done segments.
func TestManagerRecoversActiveDownloadOnStart(t *testing.T) {
	const size = 4 << 20
	content := makeContent(size)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "rec.bin"}
	m, store, dir := newManager(t, f, smallDownloadCfg(), 2)

	ctx := context.Background()
	dest := filepath.Join(dir, "rec.bin")

	segs := planSegments(size, 4)
	for i := range segs[:2] {
		segs[i].Completed = segs[i].Size()
	}
	now := time.Now().UTC()
	seed := &Download{
		ID: "recover1", URL: "https://example.com/rec.bin", Destination: dest,
		TotalSize: size, Status: StatusActive, ETag: `"v1"`,
		Segments: segs, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveDownload(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	part := make([]byte, size)
	copy(part, content[:segs[1].End+1])
	if err := os.WriteFile(dest+partSuffix, part, 0o644); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	waitStatus(t, m, "recover1", StatusCompleted, 5*time.Second)

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("recovered file does not match source")
	}

	f.mu.Lock()
	reqs := append([][2]int64(nil), f.rangeReqs...)
	f.mu.Unlock()
	if len(reqs) != 2 {
		t.Errorf("range requests = %d, want 2 (only the missing segments)", len(reqs))
	}
	for _, r := range reqs {
		if r[0] < segs[2].Start {
			t.Errorf("range %v re-fetched an already-complete segment", r)
		}
	}
}

// TestManagerShutdownCancelsInFlightNoLeak starts a download, holds it open, then
// Shuts down — asserting the in-flight transfer is cancelled, persisted state is
// left consistent (active, for a later Start), and goroutines settle (no leak).
func TestManagerShutdownCancelsInFlightNoLeak(t *testing.T) {
	const size = 8 << 20
	content := makeContent(size)
	f := &gatedFetcher{content: content, release: make(chan struct{})}
	cfg := smallDownloadCfg()
	cfg.SegmentsPerDownload = 4
	m, store, _ := newManager(t, f, cfg, 3)

	before := runtime.NumGoroutine()

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	id, err := m.Submit(context.Background(), "https://example.com/gate.bin")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitStatus(t, m, id, StatusActive, 3*time.Second)
	time.Sleep(50 * time.Millisecond)

	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.Shutdown(shutCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	close(f.release) // unblock any straggler reads

	// API is closed after shutdown.
	if _, err := m.Submit(context.Background(), "https://example.com/x"); err == nil {
		t.Error("Submit after Shutdown should fail with ErrManagerClosed")
	}

	// Persisted state must be consistent: the interrupted download stays active or
	// queued (resumable on a later Start), never torn.
	persisted, err := store.LoadDownload(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	switch persisted.Status {
	case StatusActive, StatusQueued, StatusCompleted:
	default:
		t.Errorf("persisted status after shutdown = %q, want active/queued/completed", persisted.Status)
	}

	// Goroutines settle back near the baseline (slack for runtime bookkeeping).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutines did not settle after shutdown: before=%d after=%d", before, runtime.NumGoroutine())
}

func TestManagerShutdownIdempotentAndDoubleStart(t *testing.T) {
	f := &fakeFetcher{content: makeContent(1024), supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "i.bin"}
	m, _, _ := newManager(t, f, smallDownloadCfg(), 2)

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Start(context.Background()); err == nil {
		t.Error("second Start should fail")
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown should be a no-op, got %v", err)
	}
	if err := m.Start(context.Background()); err == nil {
		t.Error("Start after Shutdown should fail")
	}
}

// raceConcurrentControls stresses Submit/Pause/Resume/Cancel/List/Get under -race
// to catch data races on the manager's shared maps and state.
func TestManagerConcurrentControlsRace(t *testing.T) {
	content := makeContent(1 << 20)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "r.bin"}
	m, _, _ := newManager(t, f, smallDownloadCfg(), 4)

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	ctx := context.Background()
	var ids sync.Map
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := m.Submit(ctx, fmt.Sprintf("https://example.com/r-%d.bin", i))
			if err != nil {
				return
			}
			ids.Store(id, struct{}{})
			_, _ = m.Get(ctx, id)
			_ = m.Pause(ctx, id)
			_, _ = m.List(ctx)
			_ = m.Resume(ctx, id)
			_ = m.Cancel(ctx, id)
		}(i)
	}
	wg.Wait()
}

// TestManagerPauseQueuedJobNeverRuns pauses a job that is still queued (every
// worker is saturated on a blocked download) and asserts the worker honors the
// pause when it later dequeues the job: the transfer never starts and the status
// stays paused. This guards the fix for the data race where a stop on a not-yet-
// running job could be overwritten by the worker's StatusActive persist.
func TestManagerPauseQueuedJobNeverRuns(t *testing.T) {
	content := makeContent(64 << 10)
	f := &gatedFetcher{content: content, release: make(chan struct{})}
	cfg := smallDownloadCfg()
	cfg.SegmentsPerDownload = 1
	// One worker so we can saturate it and keep the second submit strictly queued.
	m, store, dir := newManager(t, f, cfg, 1)

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	// Saturate the single worker with a download that blocks on the gate.
	busy, err := m.Submit(context.Background(), "https://example.com/busy.bin")
	if err != nil {
		t.Fatalf("Submit busy: %v", err)
	}
	waitStatus(t, m, busy, StatusActive, 3*time.Second)

	// This one cannot be picked up yet (the worker is busy): it stays queued.
	queued, err := m.Submit(context.Background(), "https://example.com/queued.bin")
	if err != nil {
		t.Fatalf("Submit queued: %v", err)
	}

	// Pause the still-queued job, then release the worker so it drains the queue.
	if err := m.Pause(context.Background(), queued); err != nil {
		t.Fatalf("Pause queued: %v", err)
	}
	waitStatus(t, m, queued, StatusPaused, 3*time.Second)
	close(f.release)

	// Let the worker finish the busy job and dequeue the paused one.
	waitStatus(t, m, busy, StatusCompleted, 5*time.Second)
	time.Sleep(200 * time.Millisecond) // give the worker time to (mistakenly) run it

	persisted, err := store.LoadDownload(context.Background(), queued)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if persisted.Status != StatusPaused {
		t.Errorf("paused queued job status = %q, want paused (the worker ran it after pause)", persisted.Status)
	}
	// The transfer must never have produced a destination file for the paused job.
	if _, err := os.Stat(filepath.Join(dir, "queued.bin")); !os.IsNotExist(err) {
		t.Errorf("paused queued job produced output; the transfer ran after pause: %v", err)
	}
}

// flakySaveStore wraps a Store and fails every SaveDownload once armed, so a test
// can drive the status-override persist down its failure path while loads still
// succeed (mirroring a store that accepts reads but rejects writes).
type flakySaveStore struct {
	Store
	failSaves atomic.Bool
}

func (s *flakySaveStore) SaveDownload(ctx context.Context, d *Download) error {
	if s.failSaves.Load() {
		return errors.New("simulated store write failure")
	}
	return s.Store.SaveDownload(ctx, d)
}

// TestManagerPersistOverrideLogsOnFailure proves the previously-silent error at the
// override persist is now surfaced: when the store cannot accept the override write,
// persistOverride retries the bounded number of times and logs an error rather than
// dropping the failure, so a shutdown-interrupted record left at failed is at least
// observable (and remains Resume-able). This is the regression guard for the fix to
// classify(), which used to ignore the SaveDownload error entirely.
func TestManagerPersistOverrideLogsOnFailure(t *testing.T) {
	store := &flakySaveStore{Store: newMemStore()}
	cfg := config.Config{Download: smallDownloadCfg(), Paths: config.Paths{DownloadDir: t.TempDir()}}
	f := &fakeFetcher{content: makeContent(1024), supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "o.bin"}
	m := NewManager(New(cfg, f, store), store, 1)

	var logs bytes.Buffer
	m.SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})))

	ctx := context.Background()
	now := time.Now().UTC()
	seed := &Download{ID: "ov1", URL: "https://example.com/o.bin", Status: StatusFailed, CreatedAt: now, UpdatedAt: now}
	if err := store.Store.SaveDownload(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Arm the write failure, then drive the shutdown-recovery override (failed ->
	// queued). Every save fails, so the override cannot land.
	store.failSaves.Store(true)
	m.persistOverride("ov1", StatusQueued)

	if !strings.Contains(logs.String(), "could not persist status override") {
		t.Errorf("override persist failure was not logged; got logs: %q", logs.String())
	}
	if !strings.Contains(logs.String(), "ov1") {
		t.Errorf("log did not identify the download id; got: %q", logs.String())
	}

	// With writes still failing the record keeps its engine-persisted status, but it
	// is recoverable: it is not silently lost, and Resume (allowed from failed) is the
	// documented manual path back.
	persisted, err := store.LoadDownload(ctx, "ov1")
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if persisted.Status != StatusFailed {
		t.Errorf("status after failed override = %q, want failed (unchanged)", persisted.Status)
	}

	// Once the store recovers, the override succeeds on the first attempt and lands.
	store.failSaves.Store(false)
	m.persistOverride("ov1", StatusQueued)
	persisted, err = store.LoadDownload(ctx, "ov1")
	if err != nil {
		t.Fatalf("LoadDownload after recovery: %v", err)
	}
	if persisted.Status != StatusQueued {
		t.Errorf("status after recovered override = %q, want queued", persisted.Status)
	}
}

// TestManagerPersistOverrideRetriesTransientFailure asserts the bounded retry loop
// absorbs a transient store failure: a single failing save followed by a success
// still lands the override without logging an error.
func TestManagerPersistOverrideRetriesTransientFailure(t *testing.T) {
	base := newMemStore()
	store := &countingSaveStore{Store: base, failFirst: 1}
	cfg := config.Config{Download: smallDownloadCfg(), Paths: config.Paths{DownloadDir: t.TempDir()}}
	f := &fakeFetcher{content: makeContent(1024), supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "t.bin"}
	m := NewManager(New(cfg, f, store), store, 1)

	var logs bytes.Buffer
	m.SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})))

	ctx := context.Background()
	now := time.Now().UTC()
	seed := &Download{ID: "t1", URL: "https://example.com/t.bin", Status: StatusFailed, CreatedAt: now, UpdatedAt: now}
	if err := base.SaveDownload(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	m.persistOverride("t1", StatusQueued)

	if strings.Contains(logs.String(), "could not persist status override") {
		t.Errorf("transient failure should have been absorbed by retry, but an error was logged: %q", logs.String())
	}
	persisted, err := store.LoadDownload(ctx, "t1")
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if persisted.Status != StatusQueued {
		t.Errorf("status after transient-then-success override = %q, want queued", persisted.Status)
	}
}

// countingSaveStore fails the first failFirst SaveDownload calls, then succeeds, to
// exercise persistOverride's retry loop deterministically.
type countingSaveStore struct {
	Store
	mu        sync.Mutex
	calls     int
	failFirst int
}

func (s *countingSaveStore) SaveDownload(ctx context.Context, d *Download) error {
	s.mu.Lock()
	s.calls++
	fail := s.calls <= s.failFirst
	s.mu.Unlock()
	if fail {
		return errors.New("simulated transient store failure")
	}
	return s.Store.SaveDownload(ctx, d)
}

func TestManagerResumeRejectsNonResumable(t *testing.T) {
	content := makeContent(2 << 20)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "nr.bin"}
	m, store, _ := newManager(t, f, smallDownloadCfg(), 2)

	ctx := context.Background()
	// A queued (not yet running) record is not resumable.
	now := time.Now().UTC()
	seed := &Download{ID: "q1", URL: "https://example.com/nr.bin", Status: StatusQueued, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveDownload(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := m.Resume(ctx, "q1"); !errors.Is(err, ErrNotResumable) {
		t.Errorf("Resume of a queued download = %v, want ErrNotResumable", err)
	}

	// An unknown ID surfaces a not-found error, never a panic.
	if err := m.Resume(ctx, "missing"); err == nil {
		t.Error("Resume of an unknown ID should error")
	}
}
