package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"
)

// checkpointInterval bounds how often a worker persists a segment's progress.
// Persistence stays off the per-chunk hot path (CLAUDE.md): the worker checks
// elapsed time between buffer flushes and writes at most once per interval, plus
// a final write when the stream ends.
const checkpointInterval = time.Second

// checkpointWriteTimeout bounds a single checkpoint store write. The write runs
// on a context detached from the run context (see checkpoint) so a per-run
// cancel never aborts an in-flight db query and wedges the connection; this cap
// keeps a genuinely stuck store from outliving the transfer.
const checkpointWriteTimeout = 5 * time.Second

// runSegment transfers one ranged segment into part, retrying mid-stream body
// errors up to MaxRetries and resuming from the last checkpointed offset. Its live
// Completed lives in prog (a lock-free atomic counter), so the per-chunk loop never
// contends on a process-wide mutex. Already-Done segments return immediately. After
// a clean stream it asserts the segment actually reached its end, so a short body (a
// clean EOF before the full range arrives) is retried rather than silently accepted
// (no truncated rename).
//
// coord carries the live segmentation for a work-stealing run: Start is immutable
// for the slot, but End is re-read each attempt because a thief may shrink it (the
// donor then stops at the new boundary, signalled by errSegmentStolen). Reaching a
// clip boundary is confirmed complete under the coordinator lock (coord.settled),
// which rules out a steal that was tentatively shrunk then undone; an undone steal
// restores the End and the loop re-fetches the remainder. coord is nil for the
// static and whole-body paths, where the segment's bounds are the immutable record.
func (e *Engine) runSegment(ctx context.Context, dl *Download, idx int, part *os.File, prog *segProgress, coord *stealCoord, limiter *atomic.Pointer[rateLimiter]) error {
	var plan *livePlan
	if coord != nil {
		plan = coord.plan
	}
	start := segStart(dl, plan, idx) // immutable for the run; safe without a lock.
	if isDone(Segment{Start: start, End: segEnd(dl, plan, idx)}, prog.load(idx)) {
		return nil
	}

	bufp := e.bufPool.Get().(*[]byte)
	defer e.bufPool.Put(bufp)
	buf := *bufp

	var lastErr error
	for attempt := 0; attempt <= e.cfg.MaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt > 0 {
			if err := sleepCtx(ctx, e.cfg.RetryBackoff.Duration()); err != nil {
				return err
			}
		}

		end := segEnd(dl, plan, idx) // re-read: a thief may have shrunk the tail.
		from := start + prog.load(idx)
		if from > end {
			break // fully fetched between attempts (or the tail was stolen away)
		}

		body, err := e.fetcher.RangeGet(ctx, dl.URL, from, end)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			continue
		}

		err = e.streamInto(ctx, body, part, from, dl, idx, prog, plan, start, buf, limiter)
		_ = body.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errSegmentStolen) {
			// Filled up to the live End. Confirm under the coordinator lock: a real
			// finish (or a committed steal) is done; a steal that was undone restored
			// a larger End, so re-fetch the remainder without spending the retry budget.
			if coord.settled(idx, prog.load(idx)) {
				return nil
			}
			attempt--
			continue
		}
		if err != nil {
			lastErr = err
			continue
		}
		// A clean copy is not proof of completeness: the Fetcher contract does not
		// guarantee a body fills the requested range, so a short read that ends in
		// io.EOF leaves the segment incomplete. Retry until the range is fully
		// backed; only then is success real.
		if isDone(Segment{Start: start, End: segEnd(dl, plan, idx)}, prog.load(idx)) {
			return nil
		}
		lastErr = io.ErrUnexpectedEOF
	}
	if lastErr == nil {
		lastErr = errors.New("segment incomplete")
	}
	return fmt.Errorf("engine: segment %d: %w", idx, lastErr)
}

// segStart and segEnd report segment idx's bounds. With a live plan (work-stealing)
// Start is immutable but End is read atomically because a thief may shrink it;
// without one the bounds are the durable record's immutable values.
func segStart(dl *Download, plan *livePlan, idx int) int64 {
	if plan != nil {
		return plan.start(idx)
	}
	return dl.Segments[idx].Start
}

func segEnd(dl *Download, plan *livePlan, idx int) int64 {
	if plan != nil {
		return plan.endLoad(idx)
	}
	return dl.Segments[idx].End
}

// isDone reports whether a segment with the given live completed count covers its
// whole inclusive range. End < 0 marks an open-ended (whole-body) segment, which
// has no length to satisfy here.
func isDone(seg Segment, completed int64) bool {
	if seg.End < 0 {
		return false
	}
	return completed >= seg.Size()
}

// runWholeBody handles the single-segment fallback for an unknown size or a
// server without range support: it streams the full body from offset 0 until EOF.
// Without an End there is no resume; a mid-stream failure restarts from 0 within
// the retry budget. prog index 0 tracks bytes written so the terminal persist
// reflects real progress.
func (e *Engine) runWholeBody(ctx context.Context, dl *Download, part *os.File, prog *segProgress, limiter *atomic.Pointer[rateLimiter]) error {
	bufp := e.bufPool.Get().(*[]byte)
	defer e.bufPool.Put(bufp)
	buf := *bufp

	var lastErr error
	for attempt := 0; attempt <= e.cfg.MaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt > 0 {
			if err := sleepCtx(ctx, e.cfg.RetryBackoff.Duration()); err != nil {
				return err
			}
		}

		// The fallback has no resume: every attempt restreams the whole body, so
		// the part is reset to empty to drop any stale tail from a prior attempt or
		// run before writing from offset 0.
		if err := part.Truncate(0); err != nil {
			return fmt.Errorf("engine: reset fallback file: %w", err)
		}
		prog.store(0, 0)

		body, err := e.fetcher.Get(ctx, dl.URL)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			continue
		}

		err = e.streamInto(ctx, body, part, 0, dl, 0, prog, nil, 0, buf, limiter)
		_ = body.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			lastErr = err
			continue
		}
		// When the size is known (a no-range server that still discloses
		// Content-Length), a clean EOF short of TotalSize is a truncated body, not a
		// finished download: treat it as a short read and retry. With an unknown size
		// (TotalSize <= 0) there is no length to check, so io.EOF is completion.
		if dl.TotalSize > 0 && prog.load(0) != dl.TotalSize {
			lastErr = fmt.Errorf("short body: %d of %d bytes", prog.load(0), dl.TotalSize)
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("download incomplete")
	}
	return fmt.Errorf("engine: whole-body segment: %w", lastErr)
}

// streamInto copies body into part starting at baseOff, advancing the segment's
// live completed counter (lock-free) and checkpointing to the store on a coarse
// cadence (never per chunk). baseOff is segStart + already-completed bytes for a
// ranged segment, or 0 for the whole-body fallback. plan/segStart are the
// work-stealing handles (nil/0 on the static and whole-body paths). The copy runs
// in copySegment so the zero-alloc benchmark covers the exact hot-path call.
func (e *Engine) streamInto(ctx context.Context, body io.Reader, part *os.File, baseOff int64, dl *Download, idx int, prog *segProgress, plan *livePlan, segStart int64, buf []byte, limiter *atomic.Pointer[rateLimiter]) error {
	startCompleted := prog.load(idx)

	tracked := &progressWriter{
		dst:           &offsetWriter{f: part, off: baseOff},
		ctx:           ctx,
		limiter:       limiter,
		globalLimiter: &e.globalLimiter,
		prog:          prog,
		segs:          dl.Segments,
		plan:          plan,
		start:         segStart,
		idx:           idx,
		base:          startCompleted,
		store:         e.store,
		downloadID:    dl.ID,
		nextFlush:     time.Now().Add(checkpointInterval),
	}

	_, err := copySegment(tracked, body, buf)
	tracked.checkpoint(true) // persist final progress for resume
	return err
}

// progressWriter wraps the offset writer to advance the segment's live completed
// counter (lock-free) after each buffer flush and to checkpoint to the store on a
// time cadence. When plan is non-nil (a work-stealing run) it also clips each flush
// to the segment's live End so a donor never writes into a tail a thief took.
type progressWriter struct {
	dst io.Writer

	ctx context.Context

	// limiter (per-download) and globalLimiter (engine-wide) throttle each flush at
	// the buffer boundary. Each is an atomic pointer the daemon can swap live, so a
	// rate change throttles an in-flight transfer at once; Write loads each once per
	// flush. A loaded nil (or a nil holder) means "no cap": the uncapped path takes
	// the un-throttled code path with no lock and no allocation. A nil holder also
	// supports tests that construct a progressWriter without a limiter.
	limiter       *atomic.Pointer[rateLimiter]
	globalLimiter *atomic.Pointer[rateLimiter]

	prog       *segProgress
	segs       []Segment // durable shape, used only when plan == nil
	plan       *livePlan // live End source for a work-stealing run; nil otherwise
	start      int64     // segment Start, for the clip math (plan runs only)
	idx        int
	base       int64 // completed bytes at the start of this stream
	written    int64 // bytes written during this stream
	store      Store
	downloadID string
	nextFlush  time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	// Bandwidth admission at the flush boundary (off the per-byte path): gate len(b),
	// the bytes pulled off the wire this flush, through the per-download then the
	// global bucket. Each limiter is loaded once per flush so a live rate change is
	// honored immediately; an atomic load of nil keeps the uncapped path lock-free
	// and allocation-free (proved by the -benchmem progressWriter benchmarks). WaitN
	// honors ctx, so a paused/cancelled transfer aborts here instead of holding
	// tokens. On a steal-clip below we admit len(b) but write only room; the
	// over-count is bounded by one buffer per (rare) steal and is not refunded — the
	// bytes were already read from the body.
	var perDL, global *rateLimiter
	if p.limiter != nil {
		perDL = p.limiter.Load()
	}
	if p.globalLimiter != nil {
		global = p.globalLimiter.Load()
	}
	if perDL != nil {
		if err := perDL.WaitN(p.ctx, len(b)); err != nil {
			return 0, err
		}
	}
	if global != nil {
		if err := global.WaitN(p.ctx, len(b)); err != nil {
			return 0, err
		}
	}

	if p.plan != nil {
		// Re-read the live End at this flush boundary (off the per-byte path). A
		// steal may have shrunk it; clip so this worker never writes into the
		// donated tail. See the steal-safety invariant in steal.go.
		room := (p.plan.endLoad(p.idx) - p.start + 1) - (p.base + p.written)
		if room <= 0 {
			return 0, errSegmentStolen // already filled to the shrunk End
		}
		if int64(len(b)) >= room {
			n, err := p.dst.Write(b[:room])
			if n > 0 {
				p.written += int64(n)
				p.prog.store(p.idx, p.base+p.written)
			}
			if err != nil {
				return n, err // a real short-write/IO error, not the steal stop
			}
			return n, errSegmentStolen // clean stop exactly at the shrunk End
		}
	}

	n, err := p.dst.Write(b)
	if n > 0 {
		p.written += int64(n)
		// Lock-free: this worker is the sole writer of its index.
		p.prog.store(p.idx, p.base+p.written)
	}
	if err == nil && time.Now().After(p.nextFlush) {
		p.checkpoint(false)
		p.nextFlush = time.Now().Add(checkpointInterval)
	}
	return n, err
}

// checkpoint persists the segment's current Completed off the hot path. Errors
// are swallowed: a failed checkpoint only costs re-downloaded bytes on resume,
// never correctness, and must not abort an otherwise healthy transfer.
func (p *progressWriter) checkpoint(final bool) {
	seg := p.liveSegment()
	if !final && seg.Completed == p.base {
		return // nothing new since the last checkpoint
	}
	// Detach from the run context: a cancelled parent (a sibling's error, a
	// pause, or shutdown) must not abort this write mid-query and leave the
	// connection wedged. Cap it so a stuck store cannot outlive the transfer.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(p.ctx), checkpointWriteTimeout)
	defer cancel()
	_ = p.store.UpdateSegment(ctx, p.downloadID, seg)
}

// liveSegment builds the segment value to persist. On a work-stealing run it reads
// the live plan (the End may have shrunk since planning), never the stale durable
// slice, so a donor's checkpoint can't resurrect its pre-steal End and corrupt the
// resumable tiling.
func (p *progressWriter) liveSegment() Segment {
	if p.plan != nil {
		return Segment{Index: p.idx, Start: p.start, End: p.plan.endLoad(p.idx), Completed: p.prog.load(p.idx)}
	}
	return p.prog.segmentAt(p.segs, p.idx)
}

// sleepCtx waits for d, returning ctx.Err() if the context is cancelled first. A
// non-positive d returns immediately (honoring an already-cancelled ctx).
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
