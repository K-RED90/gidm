package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
)

// memStore is an in-memory engine.Store for the engine's own tests, which cannot
// import the sqlite store without an import cycle (sqlite imports engine). The
// httpfetch integration test exercises the real sqlite store end to end.
type memStore struct {
	mu        sync.Mutex
	downloads map[string]*Download
	settings  map[string]string
}

func newMemStore() *memStore {
	return &memStore{downloads: map[string]*Download{}, settings: map[string]string{}}
}

func (m *memStore) SaveDownload(_ context.Context, d *Download) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *d
	cp.Segments = append([]Segment(nil), d.Segments...)
	m.downloads[d.ID] = &cp
	return nil
}

func (m *memStore) LoadDownload(_ context.Context, id string) (*Download, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.downloads[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *d
	cp.Segments = append([]Segment(nil), d.Segments...)
	return &cp, nil
}

func (m *memStore) ListDownloads(_ context.Context) ([]*Download, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Download, 0, len(m.downloads))
	for _, d := range m.downloads {
		cp := *d
		cp.Segments = append([]Segment(nil), d.Segments...)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *memStore) DeleteDownload(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.downloads, id)
	return nil
}

func (m *memStore) UpdateSegment(_ context.Context, id string, seg Segment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.downloads[id]
	if !ok {
		return ErrNotFound
	}
	for i := range d.Segments {
		if d.Segments[i].Index == seg.Index {
			d.Segments[i] = seg
			return nil
		}
	}
	d.Segments = append(d.Segments, seg)
	return nil
}

func (m *memStore) GetSetting(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.settings[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *memStore) SetSetting(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[key] = value
	return nil
}

func (m *memStore) Close() error { return nil }

var _ Store = (*memStore)(nil)

// fakeFetcher serves content from memory, recording the ranges it is asked for so
// tests can assert that resume fetches only the missing bytes.
type fakeFetcher struct {
	content        []byte
	supportsRanges bool
	sizeKnown      bool
	etag           string
	lastModified   string
	filename       string

	mu        sync.Mutex
	rangeReqs [][2]int64
	getCalls  int

	// failAt, when > 0, makes the first body fail after failAt bytes (per body
	// instance) exactly once, simulating a mid-stream error.
	failAt   int64
	failOnce atomic.Bool
}

func (f *fakeFetcher) Probe(_ context.Context, url string, _ RequestOptions) (ProbeInfo, error) {
	size := int64(-1)
	if f.sizeKnown {
		size = int64(len(f.content))
	}
	return ProbeInfo{
		FinalURL:       url,
		Size:           size,
		SupportsRanges: f.supportsRanges,
		ETag:           f.etag,
		LastModified:   f.lastModified,
		Filename:       f.filename,
	}, nil
}

func (f *fakeFetcher) RangeGet(_ context.Context, _ string, start, end int64, _ RequestOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	f.rangeReqs = append(f.rangeReqs, [2]int64{start, end})
	f.mu.Unlock()
	if start < 0 || end >= int64(len(f.content)) || start > end {
		return nil, errors.New("range out of bounds")
	}
	chunk := make([]byte, end-start+1)
	copy(chunk, f.content[start:end+1])
	return f.wrap(chunk), nil
}

func (f *fakeFetcher) Get(_ context.Context, _ string, _ RequestOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	f.getCalls++
	f.mu.Unlock()
	chunk := make([]byte, len(f.content))
	copy(chunk, f.content)
	return f.wrap(chunk), nil
}

func (f *fakeFetcher) wrap(b []byte) io.ReadCloser {
	if f.failAt > 0 && !f.failOnce.Swap(true) {
		return &failingBody{data: b, failAt: f.failAt}
	}
	return io.NopCloser(&bytesReader{data: b})
}

// bytesReader streams data once, advancing a position and reporting io.EOF when
// drained. It exposes no WriterTo, so io.CopyBuffer uses the pooled buffer.
type bytesReader struct {
	data []byte
	pos  int
}

func (b *bytesReader) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}

// failingBody yields data up to failAt bytes, then returns an error, once.
type failingBody struct {
	data   []byte
	pos    int64
	failAt int64
}

func (b *failingBody) Read(p []byte) (int, error) {
	if b.pos >= b.failAt {
		return 0, errors.New("simulated mid-stream failure")
	}
	rem := b.failAt - b.pos
	if int64(len(p)) > rem {
		p = p[:rem]
	}
	n := copy(p, b.data[b.pos:])
	b.pos += int64(n)
	return n, nil
}

func (b *failingBody) Close() error { return nil }

// --- test scaffolding ---

func newEngine(t *testing.T, f Fetcher, dl config.Download) (*Engine, *memStore, string) {
	t.Helper()
	store := newMemStore()
	dir := t.TempDir()
	cfg := config.Config{Download: dl, Paths: config.Paths{DownloadDir: dir}}
	return New(cfg, f, store), store, dir
}

func smallDownloadCfg() config.Download {
	return config.Download{
		MaxConcurrent:       4,
		SegmentsPerDownload: 4,
		BufferSize:          4096,
		Timeout:             config.Duration(5 * time.Second),
		MaxRetries:          3,
		RetryBackoff:        config.Duration(time.Millisecond),
	}
}

func makeContent(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('A' + i%26)
	}
	return b
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// --- tests ---

func TestDownloadFullRangeReassembles(t *testing.T) {
	content := makeContent(5 << 20) // 5 MiB, splits into multiple segments
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "file.bin"}
	e, store, dir := newEngine(t, f, smallDownloadCfg())

	dl, err := e.Download(context.Background(), "https://example.com/file.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if dl.Status != StatusCompleted {
		t.Errorf("status = %q, want completed", dl.Status)
	}

	dest := filepath.Join(dir, "file.bin")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("downloaded bytes do not match source")
	}
	if _, err := os.Stat(dest + partSuffix); !os.IsNotExist(err) {
		t.Errorf(".part file still present: %v", err)
	}

	// Persisted record reflects the completion with live segment progress.
	persisted, err := store.LoadDownload(context.Background(), dl.ID)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if persisted.Status != StatusCompleted {
		t.Errorf("persisted status = %q, want completed", persisted.Status)
	}
	for i, s := range persisted.Segments {
		if !s.Done() {
			t.Errorf("persisted segment %d not Done: %+v", i, s)
		}
	}
}

func TestDownloadVerifiesChecksumAndRenames(t *testing.T) {
	content := makeContent(3 << 20)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "ok.bin"}
	e, _, dir := newEngine(t, f, smallDownloadCfg())

	// Set checksum via a pre-seeded record is awkward; instead drive Download then
	// confirm the integrity path separately. Here we assert the happy rename, and
	// checksum mismatch is covered by TestDownloadChecksumMismatch.
	if _, err := e.Download(context.Background(), "https://example.com/ok.bin"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ok.bin")); err != nil {
		t.Errorf("final file missing: %v", err)
	}
}

func TestDownloadResumeFetchesOnlyMissingBytes(t *testing.T) {
	const size = 4 << 20
	content := makeContent(size)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "resume.bin"}
	e, store, dir := newEngine(t, f, smallDownloadCfg())

	ctx := context.Background()
	dest := filepath.Join(dir, "resume.bin")

	// Pre-seed a persisted download: 4 segments, the first two already complete,
	// with a matching .part file holding those bytes.
	segs := planSegments(size, 4)
	for i := range segs[:2] {
		segs[i].Completed = segs[i].Size()
	}
	now := time.Now().UTC()
	seed := &Download{
		ID: "seed1", URL: "https://example.com/resume.bin", Destination: dest,
		TotalSize: size, Status: StatusActive, ETag: `"v1"`,
		Segments: segs, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveDownload(ctx, seed); err != nil {
		t.Fatalf("seed SaveDownload: %v", err)
	}
	// Write the .part with the completed prefix; the rest is zero-filled.
	part := make([]byte, size)
	copy(part, content[:segs[1].End+1])
	if err := os.WriteFile(dest+partSuffix, part, 0o644); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	dl, err := e.Download(ctx, "https://example.com/resume.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if dl.ID != "seed1" {
		t.Errorf("resumed ID = %q, want seed1 (a new download was created instead of resuming)", dl.ID)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("resumed file does not match source")
	}

	// Only segments 2 and 3 should have been requested.
	f.mu.Lock()
	reqs := f.rangeReqs
	f.mu.Unlock()
	for _, r := range reqs {
		if r[0] < segs[2].Start {
			t.Errorf("range request %v fetched already-complete bytes (resume re-downloaded)", r)
		}
	}
	if len(reqs) != 2 {
		t.Errorf("range requests = %d, want 2 (only missing segments)", len(reqs))
	}
}

func TestDownloadValidatorChangeRestarts(t *testing.T) {
	const size = 4 << 20
	oldContent := makeContent(size)
	newContent := makeContent(size)
	for i := range newContent {
		newContent[i] ^= 0xFF // entirely different bytes
	}
	dest := ""
	f := &fakeFetcher{content: newContent, supportsRanges: true, sizeKnown: true, etag: `"v2"`, filename: "v.bin"}
	e, store, dir := newEngine(t, f, smallDownloadCfg())
	dest = filepath.Join(dir, "v.bin")

	ctx := context.Background()
	// Seed a stale download recorded under the OLD etag with partial progress.
	segs := planSegments(size, 4)
	segs[0].Completed = segs[0].Size()
	now := time.Now().UTC()
	seed := &Download{
		ID: "stale", URL: "https://example.com/v.bin", Destination: dest,
		TotalSize: size, Status: StatusActive, ETag: `"v1"`,
		Segments: segs, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveDownload(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	old := make([]byte, size)
	copy(old, oldContent[:segs[0].End+1])
	if err := os.WriteFile(dest+partSuffix, old, 0o644); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	dl, err := e.Download(ctx, "https://example.com/v.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if dl.ID == "stale" {
		t.Error("resumed the stale download despite a changed validator")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(newContent) {
		t.Error("file does not match the new content after validator-change restart")
	}
}

func TestDownloadRetrySucceedsWithinBudget(t *testing.T) {
	content := makeContent(2 << 20)
	f := &fakeFetcher{
		content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`,
		filename: "retry.bin", failAt: 1000,
	}
	e, _, dir := newEngine(t, f, smallDownloadCfg())

	dl, err := e.Download(context.Background(), "https://example.com/retry.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if dl.Status != StatusCompleted {
		t.Errorf("status = %q, want completed", dl.Status)
	}
	got, err := os.ReadFile(filepath.Join(dir, "retry.bin"))
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("file does not match after retry")
	}
}

func TestDownloadRetryBudgetExhaustionFails(t *testing.T) {
	content := makeContent(2 << 20)
	// A fetcher whose every body fails mid-stream exhausts the budget.
	f := &alwaysFailingFetcher{content: content, failAt: 100}
	dl := smallDownloadCfg()
	dl.MaxRetries = 2
	e, _, dir := newEngine(t, f, dl)

	_, err := e.Download(context.Background(), "https://example.com/bad.bin")
	if err == nil {
		t.Fatal("Download: expected failure when retry budget is exhausted")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "bad.bin")); !os.IsNotExist(statErr) {
		t.Errorf("final file present after failure: %v", statErr)
	}
}

// alwaysFailingFetcher fails every body mid-stream so no retry ever completes.
type alwaysFailingFetcher struct {
	content []byte
	failAt  int64
}

func (f *alwaysFailingFetcher) Probe(context.Context, string, RequestOptions) (ProbeInfo, error) {
	return ProbeInfo{FinalURL: "u", Size: int64(len(f.content)), SupportsRanges: true, ETag: `"v1"`, Filename: "bad.bin"}, nil
}
func (f *alwaysFailingFetcher) RangeGet(_ context.Context, _ string, start, _ int64, _ RequestOptions) (io.ReadCloser, error) {
	return &failingBody{data: f.content[start:], failAt: f.failAt}, nil
}
func (f *alwaysFailingFetcher) Get(context.Context, string, RequestOptions) (io.ReadCloser, error) {
	return &failingBody{data: f.content, failAt: f.failAt}, nil
}

func TestDownloadChecksumMismatchLeavesNoFile(t *testing.T) {
	content := makeContent(2 << 20)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "cs.bin"}
	dlCfg := smallDownloadCfg()
	e, store, dir := newEngine(t, f, dlCfg)

	ctx := context.Background()
	dest := filepath.Join(dir, "cs.bin")
	// Seed a download carrying a WRONG checksum so verification fails post-transfer.
	segs := planSegments(int64(len(content)), 4)
	now := time.Now().UTC()
	seed := &Download{
		ID: "csbad", URL: "https://example.com/cs.bin", Destination: dest,
		TotalSize: int64(len(content)), Status: StatusActive, ETag: `"v1"`,
		Checksum: "sha256:" + sha256Hex([]byte("not the content")),
		Segments: segs, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveDownload(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := e.Download(ctx, "https://example.com/cs.bin")
	if err == nil {
		t.Fatal("Download: expected checksum mismatch error")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("final file present after checksum mismatch: %v", statErr)
	}
	if _, statErr := os.Stat(dest + partSuffix); !os.IsNotExist(statErr) {
		t.Errorf(".part should be removed on checksum mismatch: %v", statErr)
	}

	persisted, err := store.LoadDownload(ctx, "csbad")
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if persisted.Status != StatusFailed {
		t.Errorf("persisted status = %q, want failed", persisted.Status)
	}
}

func TestDownloadSingleSegmentFallbackNoRanges(t *testing.T) {
	content := makeContent(2 << 20)
	f := &fakeFetcher{content: content, supportsRanges: false, sizeKnown: true, filename: "norange.bin"}
	e, _, dir := newEngine(t, f, smallDownloadCfg())

	if _, err := e.Download(context.Background(), "https://example.com/norange.bin"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "norange.bin"))
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("fallback download bytes mismatch")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getCalls == 0 {
		t.Error("expected Get to be used for the no-range fallback")
	}
	if len(f.rangeReqs) != 0 {
		t.Errorf("RangeGet used in no-range fallback: %v", f.rangeReqs)
	}
}

func TestDownloadSingleSegmentFallbackUnknownSize(t *testing.T) {
	content := makeContent(2 << 20)
	// Ranges supported but size unknown -> still a whole-body Get.
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: false, filename: "unknown.bin"}
	e, _, dir := newEngine(t, f, smallDownloadCfg())

	if _, err := e.Download(context.Background(), "https://example.com/unknown.bin"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "unknown.bin"))
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("unknown-size fallback bytes mismatch")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getCalls == 0 {
		t.Error("expected Get for the unknown-size fallback")
	}
}

func TestDownloadPathSafety(t *testing.T) {
	content := makeContent(1024)
	f := &fakeFetcher{content: content, supportsRanges: false, sizeKnown: true, filename: "../../etc/passwd"}
	e, _, dir := newEngine(t, f, smallDownloadCfg())

	dl, err := e.Download(context.Background(), "https://example.com/safe")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if filepath.Dir(dl.Destination) != dir {
		t.Errorf("destination %q escaped download dir %q", dl.Destination, dir)
	}
	if filepath.Base(dl.Destination) != "passwd" {
		t.Errorf("destination base = %q, want passwd", filepath.Base(dl.Destination))
	}
	// The traversal target must not exist outside the dir.
	if _, statErr := os.Stat("/etc/passwd.part"); statErr == nil {
		t.Error("wrote outside the download dir")
	}
}

// TestDownloadTerminalPersistKeepsProgress is the persistence-ownership
// regression: a terminal SaveDownload (completed or failed) must carry the live
// per-segment Completed values, never zero them.
func TestDownloadTerminalPersistKeepsProgress(t *testing.T) {
	content := makeContent(5 << 20)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "p.bin"}
	e, store, _ := newEngine(t, f, smallDownloadCfg())

	dl, err := e.Download(context.Background(), "https://example.com/p.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	persisted, err := store.LoadDownload(context.Background(), dl.ID)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	var total int64
	for _, s := range persisted.Segments {
		if s.Completed != s.Size() {
			t.Errorf("segment %d Completed = %d, want %d (terminal persist zeroed progress)", s.Index, s.Completed, s.Size())
		}
		total += s.Completed
	}
	if total != int64(len(content)) {
		t.Errorf("persisted total = %d, want %d", total, len(content))
	}
}

func TestDownloadContextCancellation(t *testing.T) {
	content := makeContent(8 << 20)
	f := &blockingFetcher{content: content, release: make(chan struct{})}
	e, _, dir := newEngine(t, f, smallDownloadCfg())

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { _, err := e.Download(ctx, "https://example.com/block.bin"); done <- err }()

	// Let the workers start and block on the body, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Download returned nil error after cancellation")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Download did not abort promptly after cancellation")
	}
	close(f.release) // unblock any straggler reads

	if _, statErr := os.Stat(filepath.Join(dir, "block.bin")); !os.IsNotExist(statErr) {
		t.Errorf("final file present after cancellation: %v", statErr)
	}

	// Goroutines should settle back to roughly the baseline (allow slack for the
	// runtime's own bookkeeping).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutines did not settle: before=%d after=%d", before, runtime.NumGoroutine())
}

// blockingFetcher serves bodies that block until release is closed or ctx is
// cancelled, so a download can be caught mid-flight.
type blockingFetcher struct {
	content []byte
	release chan struct{}
}

func (f *blockingFetcher) Probe(context.Context, string, RequestOptions) (ProbeInfo, error) {
	return ProbeInfo{FinalURL: "u", Size: int64(len(f.content)), SupportsRanges: true, ETag: `"v1"`, Filename: "block.bin"}, nil
}
func (f *blockingFetcher) RangeGet(ctx context.Context, _ string, start, end int64, _ RequestOptions) (io.ReadCloser, error) {
	return &blockingBody{ctx: ctx, release: f.release, data: f.content[start : end+1]}, nil
}
func (f *blockingFetcher) Get(ctx context.Context, _ string, _ RequestOptions) (io.ReadCloser, error) {
	return &blockingBody{ctx: ctx, release: f.release, data: f.content}, nil
}

type blockingBody struct {
	ctx     context.Context
	release chan struct{}
	data    []byte
	pos     int
}

func (b *blockingBody) Read(p []byte) (int, error) {
	if b.pos == 0 {
		// First read serves a little, then subsequent reads block.
		n := copy(p, b.data[:min(len(p), 64)])
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

func (b *blockingBody) Close() error { return nil }

// --- resume reconciliation (blocker: trusting checkpoints over the .part file) ---

// TestDownloadResumeMissingPartRestartsFresh covers a checkpoint that claims every
// segment complete while the .part file is gone. O_CREATE would silently make an
// empty file; trusting the checkpoint would rename a zero-filled file. Reconcile
// must clamp all progress to what the (absent) file backs and re-fetch everything.
func TestDownloadResumeMissingPartRestartsFresh(t *testing.T) {
	const size = 4 << 20
	content := makeContent(size)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "miss.bin"}
	e, store, dir := newEngine(t, f, smallDownloadCfg())

	ctx := context.Background()
	dest := filepath.Join(dir, "miss.bin")

	// Persisted state lies: all segments fully complete, but no .part exists.
	segs := planSegments(size, 4)
	for i := range segs {
		segs[i].Completed = segs[i].Size()
	}
	now := time.Now().UTC()
	seed := &Download{
		ID: "miss1", URL: "https://example.com/miss.bin", Destination: dest,
		TotalSize: size, Status: StatusActive, ETag: `"v1"`,
		Segments: segs, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveDownload(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dl, err := e.Download(ctx, "https://example.com/miss.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if dl.Status != StatusCompleted {
		t.Fatalf("status = %q, want completed", dl.Status)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("resumed file is corrupt: missing .part was trusted as complete (zero-filled holes)")
	}

	// Every segment must have been re-fetched: nothing was actually on disk.
	f.mu.Lock()
	n := len(f.rangeReqs)
	f.mu.Unlock()
	if n != len(segs) {
		t.Errorf("range requests = %d, want %d (all segments re-fetched)", n, len(segs))
	}
}

// TestDownloadResumeTruncatedPartRefetches covers a checkpoint desynced ahead of
// the .part file (a crash persisted bytes whose fsync never landed, or the file
// was externally truncated). The over-claimed tail of the partial segment must be
// re-fetched, not skipped as zero-filled.
func TestDownloadResumeTruncatedPartRefetches(t *testing.T) {
	const size = 4 << 20
	content := makeContent(size)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "trunc.bin"}
	e, store, dir := newEngine(t, f, smallDownloadCfg())

	ctx := context.Background()
	dest := filepath.Join(dir, "trunc.bin")

	segs := planSegments(size, 4)
	// Segment 0 is checkpointed complete, but the .part only holds part of it.
	segs[0].Completed = segs[0].Size()
	const backed = 1024 // bytes actually durable on disk for segment 0
	now := time.Now().UTC()
	seed := &Download{
		ID: "trunc1", URL: "https://example.com/trunc.bin", Destination: dest,
		TotalSize: size, Status: StatusActive, ETag: `"v1"`,
		Segments: segs, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveDownload(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// .part backs only `backed` real bytes; the rest is absent (short file).
	if err := os.WriteFile(dest+partSuffix, content[:backed], 0o644); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	dl, err := e.Download(ctx, "https://example.com/trunc.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if dl.ID != "trunc1" {
		t.Errorf("resumed ID = %q, want trunc1", dl.ID)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("resumed file is corrupt: desynced checkpoint trusted past the on-disk bytes")
	}

	// Segment 0's unbacked tail must have been requested again (a request starting
	// at `backed`), proving the over-claimed checkpoint was clamped.
	f.mu.Lock()
	reqs := append([][2]int64(nil), f.rangeReqs...)
	f.mu.Unlock()
	sawRefetch := false
	for _, r := range reqs {
		if r[0] == backed {
			sawRefetch = true
		}
	}
	if !sawRefetch {
		t.Errorf("segment 0 tail not re-fetched; requests = %v", reqs)
	}
}

// --- post-transfer completeness (blocker: clean EOF short of the range) ---

// shortRangeFetcher delivers, for every RangeGet, a body that ends in a clean EOF
// after only short bytes of the requested [start,end] range. The Fetcher contract
// makes no guarantee the body fills the range, so the engine must catch this
// rather than rename a truncated file.
type shortRangeFetcher struct {
	content []byte
	short   int64
}

func (f *shortRangeFetcher) Probe(context.Context, string, RequestOptions) (ProbeInfo, error) {
	return ProbeInfo{FinalURL: "u", Size: int64(len(f.content)), SupportsRanges: true, ETag: `"v1"`, Filename: "short.bin"}, nil
}

func (f *shortRangeFetcher) RangeGet(_ context.Context, _ string, start, end int64, _ RequestOptions) (io.ReadCloser, error) {
	n := end - start + 1
	if n > f.short {
		n = f.short // truncate the body to a clean, short EOF
	}
	chunk := make([]byte, n)
	copy(chunk, f.content[start:start+n])
	return io.NopCloser(&bytesReader{data: chunk}), nil
}

func (f *shortRangeFetcher) Get(_ context.Context, _ string, _ RequestOptions) (io.ReadCloser, error) {
	chunk := make([]byte, f.short)
	copy(chunk, f.content)
	return io.NopCloser(&bytesReader{data: chunk}), nil
}

func TestDownloadShortRangeBodyFailsNoFile(t *testing.T) {
	content := makeContent(4 << 20)
	f := &shortRangeFetcher{content: content, short: 4096}
	cfg := smallDownloadCfg()
	cfg.MaxRetries = 1 // bound the doomed retries
	cfg.RetryBackoff = config.Duration(time.Millisecond)
	e, _, dir := newEngine(t, f, cfg)

	_, err := e.Download(context.Background(), "https://example.com/short.bin")
	if err == nil {
		t.Fatal("Download: expected failure on a body short of the requested range")
	}
	dest := filepath.Join(dir, "short.bin")
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("truncated file was renamed into place: %v", statErr)
	}
}

// TestDownloadShortWholeBodyKnownSizeFails covers the no-range fallback with a
// known size: a body ending in a clean EOF short of Content-Length is a truncated
// download, not completion, and must not be renamed.
func TestDownloadShortWholeBodyKnownSizeFails(t *testing.T) {
	content := makeContent(2 << 20)
	f := &shortWholeBodyFetcher{content: content, short: 1024}
	cfg := smallDownloadCfg()
	cfg.MaxRetries = 1
	e, _, dir := newEngine(t, f, cfg)

	_, err := e.Download(context.Background(), "https://example.com/sw.bin")
	if err == nil {
		t.Fatal("Download: expected failure on a whole body short of the known size")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "sw.bin")); !os.IsNotExist(statErr) {
		t.Errorf("truncated whole-body file was renamed: %v", statErr)
	}
}

// shortWholeBodyFetcher advertises a known size but no range support, then serves
// a Get body that ends short of that size.
type shortWholeBodyFetcher struct {
	content []byte
	short   int64
}

func (f *shortWholeBodyFetcher) Probe(context.Context, string, RequestOptions) (ProbeInfo, error) {
	return ProbeInfo{FinalURL: "u", Size: int64(len(f.content)), SupportsRanges: false, Filename: "sw.bin"}, nil
}
func (f *shortWholeBodyFetcher) RangeGet(context.Context, string, int64, int64, RequestOptions) (io.ReadCloser, error) {
	return nil, errors.New("ranges unsupported")
}
func (f *shortWholeBodyFetcher) Get(context.Context, string, RequestOptions) (io.ReadCloser, error) {
	chunk := make([]byte, f.short)
	copy(chunk, f.content)
	return io.NopCloser(&bytesReader{data: chunk}), nil
}
