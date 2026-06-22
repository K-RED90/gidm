package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// errStalled marks an attempt the stall watchdog aborted. It is retryable: the
// next attempt reconnects on a fresh connection, resuming from the checkpointed
// offset, so a dead-but-open socket costs one timeout rather than wedging forever.
var errStalled = errors.New("engine: segment stalled")

// stallWatch cancels a segment attempt whose connection delivers no new bytes for
// StallTimeout. A peer that vanishes without a TCP RST leaves a body read blocked
// until keep-alive probes notice — minutes — so the watcher polls the segment's
// lock-free progress counter (advanced once per buffer flush in
// progressWriter.Write) and, on a flat counter for the whole timeout, fires the
// attempt's cancel. The cancel unblocks the read (the body is bound to the same
// context), and runSegment treats stalled as retryable. The watcher only reads
// prog, so it never touches the per-chunk hot path.
type stallWatch struct {
	stalled atomic.Bool
	done    chan struct{}
}

// startStallWatch launches a watcher for segment idx; timeout <= 0 disables it
// (stalled never set, stop is a no-op). cancel is the attempt context's cancel.
func startStallWatch(cancel context.CancelFunc, prog *segProgress, idx int, timeout time.Duration) *stallWatch {
	w := &stallWatch{done: make(chan struct{})}
	if timeout <= 0 {
		close(w.done)
		return w
	}
	go func() {
		// Poll several times per timeout so a stall is caught within ~timeout.
		tick := max(timeout/4, 250*time.Millisecond)
		t := time.NewTicker(tick)
		defer t.Stop()

		last := prog.load(idx)
		idleSince := time.Now()
		for {
			select {
			case <-w.done:
				return
			case now := <-t.C:
				if cur := prog.load(idx); cur != last {
					last = cur
					idleSince = now
					continue
				}
				if now.Sub(idleSince) >= timeout {
					w.stalled.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	return w
}

// stop ends the watcher. The caller invokes it exactly once per attempt.
func (w *stallWatch) stop() {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
}
