package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// nopSyncer is a no-op durability seam: Sync always succeeds. Used where a test
// exercises the persist path, not fsync behavior.
type nopSyncer struct{}

func (nopSyncer) Sync() error { return nil }

// errSyncer always fails its fsync, modelling a disk whose flush never reaches
// stable storage.
type errSyncer struct{}

func (errSyncer) Sync() error { return errors.New("sync failed") }

// durabilityTracker models fsync durability against the live counters. On Sync it
// records, per segment, the bytes written so far as durable — then advances the
// counters to simulate a worker landing MORE bytes in the page cache immediately
// after the sync point. A checkpointer that reads counters BEFORE syncing (the
// correct order) only ever persists the recorded-durable values; one that read
// them AFTER syncing would persist the post-sync bytes, which are not yet durable.
type durabilityTracker struct {
	prog *segProgress
	n    int
	bump int64

	mu      sync.Mutex
	durable []int64
}

func (d *durabilityTracker) Sync() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.n {
		d.durable[i] = d.prog.load(i)
	}
	for i := range d.n {
		d.prog.store(i, d.prog.load(i)+d.bump) // post-sync writes, not yet durable
	}
	return nil
}

func (d *durabilityTracker) durableAt(i int) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.durable[i]
}

// recordingStore wraps memStore and flags any persisted segment whose Completed
// exceeds the bytes the durabilityTracker has made durable for it — a violation of
// the resume invariant (a checkpoint counter that outran the fsync).
type recordingStore struct {
	*memStore
	track *durabilityTracker
	t     *testing.T

	mu       sync.Mutex
	persists int
}

func (s *recordingStore) UpdateSegment(ctx context.Context, id string, seg Segment) error {
	durable := s.track.durableAt(seg.Index)
	s.mu.Lock()
	s.persists++
	s.mu.Unlock()
	if seg.Completed > durable {
		s.t.Errorf("persisted segment %d Completed=%d exceeds durable=%d: checkpoint ran ahead of fsync", seg.Index, seg.Completed, durable)
	}
	return s.memStore.UpdateSegment(ctx, id, seg)
}

// TestCheckpointNeverOutrunsDurableBytes is the crash-durability invariant: a
// persisted segment counter must never exceed the bytes durably on disk for that
// segment. The tracker advances the counters at each fsync to model a worker
// writing on past the sync point; the barrier must persist only the pre-sync
// snapshot. Reading counters after the fsync (the bug this guards) would persist
// the post-sync bytes and fail here.
func TestCheckpointNeverOutrunsDurableBytes(t *testing.T) {
	segs := []Segment{
		{Index: 0, Start: 0, End: 999, Completed: 0},
		{Index: 1, Start: 1000, End: 1999, Completed: 0},
	}
	prog := newSegProgressSized(segs, len(segs))
	prog.store(0, 100)
	prog.store(1, 200)

	track := &durabilityTracker{prog: prog, n: len(segs), bump: 64 << 10, durable: make([]int64, len(segs))}
	store := &recordingStore{memStore: newMemStore(), track: track, t: t}
	if err := store.SaveDownload(context.Background(), &Download{ID: "dl", Segments: segs}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cp := &checkpointer{
		file:       track,
		store:      store,
		downloadID: "dl",
		ctx:        context.Background(),
		segView:    func(i int) Segment { return prog.segmentAt(segs, i) },
		activeN:    func() int { return len(segs) },
	}

	for range 3 {
		cp.tick()
	}
	cp.flushFinal(0)

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.persists == 0 {
		t.Fatal("no segments were persisted; the test would pass vacuously")
	}
}

// countingStore wraps memStore and counts UpdateSegment calls.
type countingStore struct {
	*memStore
	mu sync.Mutex
	n  int
}

func (s *countingStore) UpdateSegment(ctx context.Context, id string, seg Segment) error {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	return s.memStore.UpdateSegment(ctx, id, seg)
}

func (s *countingStore) updates() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// TestCheckpointSuppressedOnSyncError proves a failed fsync suppresses the round's
// persists: if the page cache never reached disk, advancing a persisted counter
// would let it claim bytes a crash could drop.
func TestCheckpointSuppressedOnSyncError(t *testing.T) {
	segs := []Segment{{Index: 0, Start: 0, End: 999, Completed: 0}}
	prog := newSegProgressSized(segs, len(segs))
	prog.store(0, 500)

	store := &countingStore{memStore: newMemStore()}
	if err := store.SaveDownload(context.Background(), &Download{ID: "dl", Segments: segs}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cp := &checkpointer{
		file:       errSyncer{},
		store:      store,
		downloadID: "dl",
		ctx:        context.Background(),
		segView:    func(i int) Segment { return prog.segmentAt(segs, i) },
		activeN:    func() int { return len(segs) },
	}
	cp.tick()
	cp.flushFinal(0)

	if n := store.updates(); n != 0 {
		t.Errorf("UpdateSegment called %d times after fsync failure, want 0", n)
	}
}
