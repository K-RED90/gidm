package engine

import (
	"context"
	"sync"
	"time"
)

// syncer is the durability seam the checkpointer fsyncs through: the .part file
// in production, a fake in tests that models which bytes have reached disk.
type syncer interface{ Sync() error }

// checkpointer is the run-level durability barrier for a multi-segment ranged
// transfer. Every checkpointInterval it does ONE whole-file fsync, then persists
// each active segment's live counter. The order is load-bearing: fsync first, so
// every counter it then persists describes bytes already durable on disk.
//
// This upholds the resume invariant — a persisted segment counter never exceeds
// the bytes durably on disk for that segment — which reconcileProgress can no
// longer enforce on its own: the .part is preallocated to full size, so its
// on-disk size never reveals which bytes are actually durable. Without the
// barrier, a checkpoint committed after a periodic write (SQLite WAL +
// synchronous=NORMAL) could outlive page-cache bytes a hard crash dropped, and
// resume would skip re-fetching them, leaving a silent hole.
//
// One fsync per interval (not per segment) is deliberate: os.File.Sync flushes
// every segment's dirty pages at once, so a single call does the same physical
// work as N per-segment fsyncs for identical durability. It also keeps fsync off
// the per-chunk hot path entirely — progressWriter.Write now only advances a
// lock-free counter, and this one goroutine reads those counters for the snapshot.
type checkpointer struct {
	file       syncer
	store      Store
	downloadID string
	ctx        context.Context // run context; persist writes detach from it

	// segView returns the durable shape (Start/End/Index) of slot i with its live
	// Completed folded in; activeN reports how many slots are live. Together they
	// hide the static (immutable dl.Segments) vs work-stealing (live plan, growing
	// slot count) difference from the barrier.
	segView func(i int) Segment
	activeN func() int
}

// startCheckpointer launches c's barrier goroutine and returns an idempotent stop
// function that signals it and waits for it to exit. Callers defer the stop so the
// barrier is fully quiesced before the .part is synced/closed at finalization — no
// fsync ever races the terminal one.
func startCheckpointer(c *checkpointer) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.run(stop)
	}()
	return sync.OnceFunc(func() {
		close(stop)
		<-done
	})
}

// run ticks the barrier every checkpointInterval until stop is signalled.
func (c *checkpointer) run(stop <-chan struct{}) {
	t := time.NewTicker(checkpointInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			c.tick()
		}
	}
}

// tick snapshots the live counters, fsyncs once, then persists that snapshot. The
// snapshot is taken BEFORE the fsync on purpose: fsync makes durable every byte
// written before it returned, which includes every byte the snapshot counts (those
// were written before the snapshot, hence before the fsync), so each persisted
// counter is backed by durable bytes. Reading the counters AFTER the fsync would
// instead risk persisting a counter a worker advanced in the window between the
// fsync and the read — bytes still only in the page cache, the exact crash hole
// this closes. A failed fsync means nothing reached disk, so the round's persists
// are suppressed rather than written ahead of durable bytes.
func (c *checkpointer) tick() {
	n := c.activeN()
	segs := make([]Segment, n) // cold path (once per interval): the alloc is immaterial
	for i := range n {
		segs[i] = c.segView(i)
	}
	if err := c.file.Sync(); err != nil {
		return
	}
	for i := range segs {
		c.persist(segs[i])
	}
}

// flushFinal fsync-orders a single segment's terminal persist when its stream ends
// cleanly between ticks — a segment can finish and its worker exit before the next
// barrier, so the final counter still needs its own durable-ordered write. The
// counter is read before the fsync, matching tick's ordering.
func (c *checkpointer) flushFinal(idx int) {
	seg := c.segView(idx)
	if err := c.file.Sync(); err != nil {
		return
	}
	c.persist(seg)
}

// persist writes one segment row. It detaches from the run context so a parent
// cancel (pause, shutdown, a sibling's error) cannot abort the write mid-query and
// wedge the store connection; the cap bounds a genuinely stuck store. Errors are
// swallowed: a lost checkpoint only costs re-downloaded bytes on resume, never
// correctness (CLAUDE.md: persistence off the hot path).
func (c *checkpointer) persist(seg Segment) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.ctx), checkpointWriteTimeout)
	defer cancel()
	_ = c.store.UpdateSegment(ctx, c.downloadID, seg)
}
