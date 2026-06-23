package engine

import (
	"context"
	"testing"
)

// ctxAwareStore wraps memStore so UpdateSegment honors context cancellation,
// mirroring database/sql: a cancelled context aborts the write. It lets the
// checkpoint-detach test prove the write lands despite a cancelled parent.
type ctxAwareStore struct {
	*memStore
}

func (c ctxAwareStore) UpdateSegment(ctx context.Context, id string, seg Segment) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.memStore.UpdateSegment(ctx, id, seg)
}

// TestCheckpointSurvivesParentCancel is the root-cause regression: a checkpoint
// fired with an already-cancelled run context must still persist. Before the
// fix the write inherited the cancelled context (UpdateSegment errored and, with
// the real sqlite store, could wedge a pooled connection); now the checkpointer's
// persist runs on a context detached from cancellation.
func TestCheckpointSurvivesParentCancel(t *testing.T) {
	t.Parallel()

	store := ctxAwareStore{newMemStore()}
	if err := store.SaveDownload(context.Background(), &Download{
		ID:       "dl",
		Segments: []Segment{{Index: 0, Start: 0, End: 999, Completed: 0}},
	}); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	segs := []Segment{{Index: 0, Start: 0, End: 999, Completed: 0}}
	prog := newSegProgressSized(segs, 1)
	prog.store(0, 500) // 500 bytes fetched since the seeded checkpoint

	// Control: the store really does reject a cancelled context, so a passing
	// checkpoint below means the write was genuinely detached, not merely tolerated.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.UpdateSegment(cancelled, "dl", segs[0]); err == nil {
		t.Fatal("control: UpdateSegment with cancelled ctx = nil, want error")
	}

	cp := &checkpointer{
		file:       nopSyncer{}, // fsync is irrelevant here; exercise the persist detach
		store:      store,
		downloadID: "dl",
		ctx:        cancelled, // the run context is already cancelled
		segView:    func(i int) Segment { return prog.segmentAt(segs, i) },
		activeN:    func() int { return len(segs) },
	}
	cp.flushFinal(0)

	got, err := store.LoadDownload(context.Background(), "dl")
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if got.Segments[0].Completed != 500 {
		t.Errorf("persisted Completed = %d, want 500 (checkpoint dropped under a cancelled parent)", got.Segments[0].Completed)
	}
}
