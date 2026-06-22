package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestManagerRestartIdleDiscardsProgressAndRequeues restarts a settled download
// and asserts its checkpoints, validators, and .part file are discarded and the
// record is re-queued from scratch (no worker is running, so the reset is direct).
func TestManagerRestartIdleDiscardsProgressAndRequeues(t *testing.T) {
	f := &fakeFetcher{content: makeContent(1 << 20), supportsRanges: true, sizeKnown: true, etag: `"v1"`}
	m, store, dir := newManager(t, f, smallDownloadCfg(), 2)

	dest := filepath.Join(dir, "f.bin")
	seed := &Download{
		ID:          "d1",
		URL:         "https://example.com/f.bin",
		Destination: dest,
		Status:      StatusFailed,
		TotalSize:   1 << 20,
		ETag:        `"v1"`,
		Segments:    []Segment{{Index: 0, Start: 0, End: (1 << 20) - 1, Completed: 4096}},
	}
	if err := store.SaveDownload(context.Background(), seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	partPath := dest + partSuffix
	if err := os.WriteFile(partPath, []byte("partial bytes"), 0o644); err != nil {
		t.Fatalf("seed .part: %v", err)
	}

	if err := m.Restart(context.Background(), "d1"); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	got, err := store.LoadDownload(context.Background(), "d1")
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if got.Status != StatusQueued {
		t.Errorf("status = %q, want %q", got.Status, StatusQueued)
	}
	if len(got.Segments) != 0 {
		t.Errorf("segments = %d, want 0 (checkpoints discarded)", len(got.Segments))
	}
	if got.TotalSize != 0 || got.ETag != "" {
		t.Errorf("validators not cleared on restart: size=%d etag=%q", got.TotalSize, got.ETag)
	}
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Errorf(".part still present after restart (err=%v); it must be discarded", err)
	}

	// The reset record is enqueued for a worker.
	m.mu.Lock()
	var queued bool
	for _, p := range m.pending {
		if p.id == "d1" {
			queued = true
		}
	}
	m.mu.Unlock()
	if !queued {
		t.Error("restarted download was not re-enqueued")
	}
}

// TestManagerRestartUnknownIDNotFound asserts restarting a missing download surfaces
// ErrNotFound rather than silently succeeding.
func TestManagerRestartUnknownIDNotFound(t *testing.T) {
	m, _, _ := newManager(t, &fakeFetcher{}, smallDownloadCfg(), 1)
	if err := m.Restart(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Restart(unknown) = %v, want ErrNotFound", err)
	}
}

// TestManagerRestartActiveAbortsAndRedownloads restarts an in-flight download and
// asserts it aborts the running transfer, re-queues from scratch, and the re-run
// completes with the correct bytes — exercising the cancel→settle handshake that
// keeps the reset from racing the worker's own store writes.
func TestManagerRestartActiveAbortsAndRedownloads(t *testing.T) {
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

	id, err := m.Submit(context.Background(), "https://example.com/gate.bin", PriorityNormal, AddOptions{})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitStatus(t, m, id, StatusActive, 3*time.Second)
	time.Sleep(50 * time.Millisecond) // let the first chunks and the .part land

	dest := filepath.Join(dir, "gate.bin")
	if _, err := os.Stat(dest + partSuffix); err != nil {
		t.Fatalf("expected a .part mid-transfer: %v", err)
	}

	// Restart blocks until the aborted run drains through its settle path and the
	// record is re-queued; cancelling the in-flight bodies (gated on ctx) is what
	// lets it return without first closing the gate.
	start := time.Now()
	if err := m.Restart(context.Background(), id); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Restart took %s; expected prompt abort+requeue", elapsed)
	}

	// The record is re-queued (or, on a fast machine, a free worker has already
	// picked the reset record back up): either way it must not be left in a settled
	// state. The precise discard of checkpoints/.part is asserted deterministically by
	// the idle test, which shares the resetAndEnqueue path.
	reset, err := store.LoadDownload(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadDownload after restart: %v", err)
	}
	switch reset.Status {
	case StatusQueued, StatusActive:
	default:
		t.Errorf("status after restart = %q, want queued or active (re-running)", reset.Status)
	}

	// Let the re-run finish and assert it produced the whole, correct file.
	close(f.release)
	done := waitStatus(t, m, id, StatusCompleted, 5*time.Second)
	if done.TotalSize != size {
		t.Errorf("re-run TotalSize = %d, want %d", done.TotalSize, size)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("re-downloaded bytes do not match source after restart")
	}
}
