package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// checkpointInterval bounds how often a worker persists a segment's progress.
// Persistence stays off the per-chunk hot path (CLAUDE.md): the worker checks
// elapsed time between buffer flushes and writes at most once per interval, plus
// a final write when the stream ends.
const checkpointInterval = time.Second

// runSegment transfers one ranged segment into part, retrying mid-stream body
// errors up to MaxRetries and resuming from the last checkpointed offset. It owns
// Download.Segments[idx] exclusively; its live Completed lives in prog (a lock-free
// atomic counter), so the per-chunk loop never contends on a process-wide mutex.
// Already-Done segments return immediately. After a clean stream it asserts the
// segment actually reached its end, so a short body (a clean EOF before the full
// range arrives) is retried rather than silently accepted (no truncated rename).
func (e *Engine) runSegment(ctx context.Context, dl *Download, idx int, part *os.File, prog *segProgress) error {
	seg := dl.Segments[idx] // Start/End/Index are immutable for the run; safe without a lock.
	if isDone(seg, prog.load(idx)) {
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

		from := seg.Start + prog.load(idx)
		if from > seg.End {
			break // fully fetched between attempts
		}

		body, err := e.fetcher.RangeGet(ctx, dl.URL, from, seg.End)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			continue
		}

		err = e.streamInto(ctx, body, part, from, dl, idx, prog, buf)
		_ = body.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			lastErr = err
			continue
		}
		// A clean copy is not proof of completeness: the Fetcher contract does not
		// guarantee a body fills the requested range, so a short read that ends in
		// io.EOF leaves the segment incomplete. Retry until the range is fully
		// backed; only then is success real.
		if isDone(seg, prog.load(idx)) {
			return nil
		}
		lastErr = io.ErrUnexpectedEOF
	}
	if lastErr == nil {
		lastErr = errors.New("segment incomplete")
	}
	return fmt.Errorf("engine: segment %d: %w", idx, lastErr)
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
func (e *Engine) runWholeBody(ctx context.Context, dl *Download, part *os.File, prog *segProgress) error {
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

		err = e.streamInto(ctx, body, part, 0, dl, 0, prog, buf)
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
// cadence (never per chunk). baseOff is seg.Start + already-completed bytes for a
// ranged segment, or 0 for the whole-body fallback. The copy runs in copySegment
// so the zero-alloc benchmark covers the exact hot-path call.
func (e *Engine) streamInto(ctx context.Context, body io.Reader, part *os.File, baseOff int64, dl *Download, idx int, prog *segProgress, buf []byte) error {
	startCompleted := prog.load(idx)

	tracked := &progressWriter{
		dst:        &offsetWriter{f: part, off: baseOff},
		ctx:        ctx,
		prog:       prog,
		segs:       dl.Segments,
		idx:        idx,
		base:       startCompleted,
		store:      e.store,
		downloadID: dl.ID,
		nextFlush:  time.Now().Add(checkpointInterval),
	}

	_, err := copySegment(tracked, body, buf)
	tracked.checkpoint(true) // persist final progress for resume
	return err
}

// progressWriter wraps the offset writer to advance the segment's live completed
// counter (lock-free) after each buffer flush and to checkpoint to the store on a
// time cadence.
type progressWriter struct {
	dst io.Writer

	ctx        context.Context
	prog       *segProgress
	segs       []Segment // immutable shape (Start/End/Index) for this run
	idx        int
	base       int64 // completed bytes at the start of this stream
	written    int64 // bytes written during this stream
	store      Store
	downloadID string
	nextFlush  time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
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
	seg := p.prog.segmentAt(p.segs, p.idx)
	if !final && seg.Completed == p.base {
		return // nothing new since the last checkpoint
	}
	_ = p.store.UpdateSegment(p.ctx, p.downloadID, seg)
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
