package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
)

// stealCfg enables work-stealing with a small buffer and floor so steals fire
// readily in tests, and 8 workers so a file that plans into fewer segments leaves
// idle workers that steal immediately.
func stealCfg() config.Download {
	return config.Download{
		MaxConcurrent:       4,
		SegmentsPerDownload: 8,
		BufferSize:          4096,
		Timeout:             config.Duration(5 * time.Second),
		MaxRetries:          3,
		RetryBackoff:        config.Duration(time.Millisecond),
		WorkStealing:        true,
		MaxSegments:         64,
		MinStealSize:        4096,
	}
}

// assertTiling checks that segs cover [0,total) exactly: sorted by Start they are
// contiguous, non-empty, with no gap and no overlap. This is the byte-accounting
// proof that steals never double-write or leave a hole.
func assertTiling(t *testing.T, segs []Segment, total int64) {
	t.Helper()
	cp := append([]Segment(nil), segs...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Start < cp[j].Start })
	var want int64
	for i, s := range cp {
		if s.Start != want {
			t.Errorf("segment %d Start = %d, want %d (gap or overlap)", i, s.Start, want)
		}
		if s.End < s.Start {
			t.Errorf("segment %d empty or inverted: [%d,%d]", i, s.Start, s.End)
		}
		want = s.End + 1
	}
	if want != total {
		t.Errorf("segments cover %d bytes, want %d", want, total)
	}
}

func TestWorkStealingReassemblesWithManySteals(t *testing.T) {
	const size = 4 << 20 // plans into 4 x 1 MiB segments; 8 workers ⇒ 4 idle stealers
	content := makeContent(size)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "ws.bin"}
	e, store, dir := newEngine(t, f, stealCfg())

	dl, err := e.Download(context.Background(), "https://example.com/ws.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if dl.Status != StatusCompleted {
		t.Errorf("status = %q, want completed", dl.Status)
	}
	if len(dl.Segments) <= 4 {
		t.Errorf("segments = %d, want > 4 (idle workers should have stolen and split)", len(dl.Segments))
	}
	assertTiling(t, dl.Segments, size)

	dest := filepath.Join(dir, "ws.bin")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("downloaded bytes do not match source")
	}
	if _, err := os.Stat(dest + partSuffix); !os.IsNotExist(err) {
		t.Errorf(".part still present: %v", err)
	}

	persisted, err := store.LoadDownload(context.Background(), dl.ID)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	assertTiling(t, persisted.Segments, size)
	for i, s := range persisted.Segments {
		if !s.Done() {
			t.Errorf("persisted segment %d not Done: %+v", i, s)
		}
	}
}

// stragglerFetcher serves every range from memory immediately, except the first
// body whose range starts at or beyond slowFrom: that one delivers a small prefix
// (via blockingBody) then blocks until release closes, modelling a single slow
// connection. The stolen tails (later fetches of that region) take the fast path,
// so a fast worker that steals the straggler's tail completes it on a new
// connection while the original slow body is still blocked.
type stragglerFetcher struct {
	content  []byte
	slowFrom int64
	release  chan struct{}
	blocked  atomic.Bool

	mu        sync.Mutex
	rangeReqs [][2]int64
}

func (f *stragglerFetcher) Probe(_ context.Context, url string, _ RequestOptions) (ProbeInfo, error) {
	return ProbeInfo{FinalURL: url, Size: int64(len(f.content)), SupportsRanges: true, ETag: `"v1"`, Filename: "straggler.bin"}, nil
}

func (f *stragglerFetcher) RangeGet(ctx context.Context, _ string, start, end int64, _ RequestOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	f.rangeReqs = append(f.rangeReqs, [2]int64{start, end})
	f.mu.Unlock()
	chunk := make([]byte, end-start+1)
	copy(chunk, f.content[start:end+1])
	if start >= f.slowFrom && !f.blocked.Swap(true) {
		return &blockingBody{ctx: ctx, release: f.release, data: chunk}, nil
	}
	return io.NopCloser(&bytesReader{data: chunk}), nil
}

func (f *stragglerFetcher) Get(_ context.Context, _ string, _ RequestOptions) (io.ReadCloser, error) {
	return io.NopCloser(&bytesReader{data: append([]byte(nil), f.content...)}), nil
}

func (f *stragglerFetcher) stoleTail() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rangeReqs {
		if r[0] > f.slowFrom { // a sub-range starting past the straggler's own start
			return true
		}
	}
	return false
}

func TestWorkStealingStragglerTailIsStolen(t *testing.T) {
	const size = 8 << 20
	content := makeContent(size)
	cfg := stealCfg()
	// The straggler is the last planned segment; gate its first body.
	slowFrom := planSegments(size, cfg.SegmentsPerDownload)
	f := &stragglerFetcher{content: content, slowFrom: slowFrom[len(slowFrom)-1].Start, release: make(chan struct{})}
	e, _, dir := newEngine(t, f, cfg)

	var (
		dl   *Download
		derr error
		done = make(chan struct{})
	)
	go func() {
		dl, derr = e.Download(context.Background(), "https://example.com/straggler.bin")
		close(done)
	}()

	// A fast worker must steal the straggler's tail before the straggler can finish
	// (it is blocked), so wait for structural evidence, not a timer.
	deadline := time.Now().Add(3 * time.Second)
	for !f.stoleTail() {
		if time.Now().After(deadline) {
			close(f.release)
			<-done
			t.Fatal("no fast worker stole the straggler's tail before the deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(f.release) // let the straggler finish its now-smaller head
	<-done

	if derr != nil {
		t.Fatalf("Download: %v", derr)
	}
	if len(dl.Segments) <= len(slowFrom) {
		t.Errorf("segments = %d, want > %d (the straggler should have been split)", len(dl.Segments), len(slowFrom))
	}
	assertTiling(t, dl.Segments, size)

	got, err := os.ReadFile(filepath.Join(dir, "straggler.bin"))
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("downloaded bytes do not match source after straggler steal")
	}
}

func TestWorkStealingWholeBodyUnaffected(t *testing.T) {
	content := makeContent(3 << 20)
	// No range support ⇒ the single open-ended segment path, which stealing must
	// never touch.
	f := &fakeFetcher{content: content, supportsRanges: false, sizeKnown: true, etag: `"v1"`, filename: "whole.bin"}
	e, _, dir := newEngine(t, f, stealCfg())

	if _, err := e.Download(context.Background(), "https://example.com/whole.bin"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	f.mu.Lock()
	getCalls, rangeReqs := f.getCalls, len(f.rangeReqs)
	f.mu.Unlock()
	if getCalls == 0 {
		t.Error("whole-body path not taken (expected a Get call)")
	}
	if rangeReqs != 0 {
		t.Errorf("range requests = %d, want 0 on the whole-body path", rangeReqs)
	}
	got, err := os.ReadFile(filepath.Join(dir, "whole.bin"))
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("whole-body download bytes do not match source")
	}
}

func TestWorkStealingTinyFileStaysOneSegment(t *testing.T) {
	// Below 2*minSteal with the default 1 MiB floor: not worth splitting.
	const size = 1500 * 1024 // ~1.46 MiB
	content := makeContent(size)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "tiny.bin"}
	cfg := stealCfg()
	cfg.BufferSize = 64 * 1024
	cfg.MinStealSize = 1 << 20 // effMinSteal = 1 MiB ⇒ split needs >= 2 MiB remaining
	e, _, dir := newEngine(t, f, cfg)

	dl, err := e.Download(context.Background(), "https://example.com/tiny.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(dl.Segments) != 1 {
		t.Errorf("segments = %d, want 1 (file below the steal floor)", len(dl.Segments))
	}
	got, err := os.ReadFile(filepath.Join(dir, "tiny.bin"))
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("tiny-file bytes do not match source")
	}
}

func TestWorkStealingResumeFromPostStealLayout(t *testing.T) {
	const size = 4 << 20
	content := makeContent(size)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "wsresume.bin"}
	e, store, dir := newEngine(t, f, stealCfg())
	ctx := context.Background()
	dest := filepath.Join(dir, "wsresume.bin")

	// A coherent post-steal layout: segment 0 was split into a complete donor
	// [0,mid] and a not-yet-fetched tail (idx 4); segment 1 is complete; 2 and 3 are
	// untouched. Dense indices, exactly tiling [0,size).
	base := planSegments(size, 4)
	mid := base[0].Start + base[0].Size()/2 - 1
	segs := []Segment{
		{Index: 0, Start: base[0].Start, End: mid, Completed: mid - base[0].Start + 1},
		{Index: 1, Start: base[1].Start, End: base[1].End, Completed: base[1].Size()},
		{Index: 2, Start: base[2].Start, End: base[2].End, Completed: 0},
		{Index: 3, Start: base[3].Start, End: base[3].End, Completed: 0},
		{Index: 4, Start: mid + 1, End: base[0].End, Completed: 0},
	}
	now := time.Now().UTC()
	seed := &Download{
		ID: "wsseed", URL: "https://example.com/wsresume.bin", Destination: dest,
		TotalSize: size, Status: StatusActive, ETag: `"v1"`,
		Segments: segs, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveDownload(ctx, seed); err != nil {
		t.Fatalf("seed SaveDownload: %v", err)
	}
	// .part holds the completed regions: [0,mid] and [base1.Start, base1.End].
	part := make([]byte, size)
	copy(part[base[0].Start:mid+1], content[base[0].Start:mid+1])
	copy(part[base[1].Start:base[1].End+1], content[base[1].Start:base[1].End+1])
	if err := os.WriteFile(dest+partSuffix, part, 0o644); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	dl, err := e.Download(ctx, "https://example.com/wsresume.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if dl.ID != "wsseed" {
		t.Errorf("resumed ID = %q, want wsseed (a fresh download was created)", dl.ID)
	}
	assertTiling(t, dl.Segments, size)
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("resumed file does not match source")
	}
	// The complete donor head [0,mid] must not be re-fetched from the start.
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rangeReqs {
		if r[0] == base[0].Start {
			t.Errorf("range request %v re-fetched the already-complete donor head", r)
		}
	}
}

func TestWorkStealingResumeRepairsTornLayout(t *testing.T) {
	const size = 4 << 20
	content := makeContent(size)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "torn.bin"}
	e, store, dir := newEngine(t, f, stealCfg())
	ctx := context.Background()
	dest := filepath.Join(dir, "torn.bin")

	// A TORN layout a crash between a steal's two checkpoints could leave: the donor
	// still claims its pre-shrink End [0,end0] while the stolen tail [mid+1,end0]
	// also exists, so the two overlap. repairLayout must trim the donor and finish
	// byte-exact rather than wedge on the overlap.
	base := planSegments(size, 4)
	mid := base[0].Start + base[0].Size()/2 - 1
	segs := []Segment{
		{Index: 0, Start: base[0].Start, End: base[0].End, Completed: 0}, // stale: not shrunk
		{Index: 1, Start: base[1].Start, End: base[1].End, Completed: 0},
		{Index: 2, Start: base[2].Start, End: base[2].End, Completed: 0},
		{Index: 3, Start: base[3].Start, End: base[3].End, Completed: 0},
		{Index: 4, Start: mid + 1, End: base[0].End, Completed: 0}, // tail overlapping the donor
	}
	now := time.Now().UTC()
	seed := &Download{
		ID: "tornseed", URL: "https://example.com/torn.bin", Destination: dest,
		TotalSize: size, Status: StatusActive, ETag: `"v1"`,
		Segments: segs, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveDownload(ctx, seed); err != nil {
		t.Fatalf("seed SaveDownload: %v", err)
	}

	dl, err := e.Download(ctx, "https://example.com/torn.bin")
	if err != nil {
		t.Fatalf("Download (torn resume): %v", err)
	}
	assertTiling(t, dl.Segments, size)
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("repaired resume does not match source")
	}
}

// pollObserver continuously folds the live counters while a stealing download runs,
// to prove the observer's reads stay race-clean as steals append slots and workers
// update counters. The fold target spans the full slot ceiling.
type pollObserver struct {
	target []Segment
	stop   chan struct{}
	done   chan struct{}
}

func (o *pollObserver) trackProgress(_ string, prog *segProgress) {
	go func() {
		defer close(o.done)
		for {
			select {
			case <-o.stop:
				return
			default:
				prog.snapshotInto(o.target)
			}
		}
	}()
}

func (o *pollObserver) untrackProgress(_ string) { close(o.stop) }

func TestWorkStealingObserverReadsRaceClean(t *testing.T) {
	const size = 4 << 20
	content := makeContent(size)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "obs.bin"}
	e, _, dir := newEngine(t, f, stealCfg())

	obs := &pollObserver{
		target: make([]Segment, e.cfg.MaxSegments),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	e.observer = obs

	dl, err := e.Download(context.Background(), "https://example.com/obs.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	<-obs.done // the poller saw untrackProgress and stopped

	assertTiling(t, dl.Segments, size)
	got, err := os.ReadFile(filepath.Join(dir, "obs.bin"))
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("downloaded bytes do not match source")
	}
}

// nopWriter is a no-op io.Writer for allocation benchmarks of progressWriter.
type nopWriter struct{}

func (nopWriter) Write(b []byte) (int, error) { return len(b), nil }

// TestProgressWriterWriteZeroAllocsStealing proves the steady-state per-flush write
// stays allocation-free on the work-stealing hot path: the live-End re-read and clip
// math are atomic loads and integer arithmetic, never an allocation.
func TestProgressWriterWriteZeroAllocsStealing(t *testing.T) {
	seg := []Segment{{Index: 0, Start: 0, End: 1 << 62}}
	pw := &progressWriter{
		dst:  nopWriter{},
		prog: newSegProgressSized(seg, 1),
		plan: newLivePlan(seg, 1),
	}
	buf := make([]byte, 64*1024)
	allocs := testing.AllocsPerRun(200, func() {
		if _, err := pw.Write(buf); err != nil {
			t.Fatalf("Write: %v", err)
		}
	})
	if allocs != 0 {
		t.Errorf("progressWriter.Write allocations = %v per run, want 0 (stealing hot path)", allocs)
	}
}

func BenchmarkProgressWriterWriteStealing(b *testing.B) {
	seg := []Segment{{Index: 0, Start: 0, End: 1 << 62}}
	pw := &progressWriter{
		dst:  nopWriter{},
		prog: newSegProgressSized(seg, 1),
		plan: newLivePlan(seg, 1),
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

// slowSegFetcher models a single slow connection: the first body whose range
// starts at or beyond slowFrom delivers each chunk after delay; every other fetch
// (including the stolen tails of that region) is instant. So with work-stealing a
// fast worker reassigns the straggler's tail onto fast connections, and only the
// small head still drains slowly.
type slowSegFetcher struct {
	content  []byte
	slowFrom int64
	delay    time.Duration
	slowed   atomic.Bool
}

func (f *slowSegFetcher) Probe(_ context.Context, url string, _ RequestOptions) (ProbeInfo, error) {
	return ProbeInfo{FinalURL: url, Size: int64(len(f.content)), SupportsRanges: true, ETag: `"v1"`, Filename: "slow.bin"}, nil
}

func (f *slowSegFetcher) RangeGet(_ context.Context, _ string, start, end int64, _ RequestOptions) (io.ReadCloser, error) {
	chunk := make([]byte, end-start+1)
	copy(chunk, f.content[start:end+1])
	if start >= f.slowFrom && !f.slowed.Swap(true) {
		return io.NopCloser(&delayReader{data: chunk, delay: f.delay}), nil
	}
	return io.NopCloser(&bytesReader{data: chunk}), nil
}

func (f *slowSegFetcher) Get(_ context.Context, _ string, _ RequestOptions) (io.ReadCloser, error) {
	return io.NopCloser(&bytesReader{data: append([]byte(nil), f.content...)}), nil
}

// delayReader sleeps delay before each read after the first, throttling a body.
type delayReader struct {
	data  []byte
	pos   int
	delay time.Duration
}

func (r *delayReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	if r.pos > 0 {
		time.Sleep(r.delay)
	}
	n := copy(p, r.data[r.pos:min(r.pos+16<<10, len(r.data))])
	r.pos += n
	return n, nil
}

// BenchmarkStraggler reports the wall-clock win of work-stealing when one segment
// is served by a slow connection: stealing reassigns the straggler's tail to fast
// workers, so the run tracks the fast links rather than the slow one. Run with
//
//	go test -run='^$' -bench=BenchmarkStraggler ./internal/engine/
func BenchmarkStraggler(b *testing.B) {
	const size = 8 << 20
	content := makeContent(size)
	cfg := stealCfg()
	cfg.BufferSize = 64 << 10
	slowFrom := planSegments(size, cfg.SegmentsPerDownload)
	slowStart := slowFrom[len(slowFrom)-1].Start

	run := func(b *testing.B, stealing bool) {
		c := cfg
		c.WorkStealing = stealing
		for range b.N {
			b.StopTimer()
			f := &slowSegFetcher{content: content, slowFrom: slowStart, delay: 200 * time.Microsecond}
			e := New(config.Config{Download: c, Paths: config.Paths{DownloadDir: b.TempDir()}}, f, newMemStore())
			b.StartTimer()
			if _, err := e.Download(context.Background(), "https://example.com/slow.bin"); err != nil {
				b.Fatalf("Download: %v", err)
			}
		}
	}
	b.Run("with_stealing", func(b *testing.B) { run(b, true) })
	b.Run("without_stealing", func(b *testing.B) { run(b, false) })
}
