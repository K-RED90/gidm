package engine

import "sync/atomic"

// segProgress holds the live per-segment completed-byte counters, one int64 per
// segment, indexed the same as Download.Segments. Each worker owns exactly one
// index and updates it lock-free on the hot path (CLAUDE.md principle 2: the
// per-chunk loop must not contend on a shared mutex). Readers that need a
// consistent structural view (checkpoint, terminal persist) snapshot the counters
// atomically; the Download.Segments slice carries the durable Start/End/Index
// shape, while these counters are the authoritative Completed values until a
// snapshot folds them back in.
type segProgress struct {
	completed []atomic.Int64
}

// newSegProgress builds the counter set for segs, seeding each counter from the
// segment's persisted Completed so a resumed download starts from its checkpoint.
func newSegProgress(segs []Segment) *segProgress {
	p := &segProgress{completed: make([]atomic.Int64, len(segs))}
	for i := range segs {
		p.completed[i].Store(segs[i].Completed)
	}
	return p
}

// load reads segment idx's completed bytes atomically.
func (p *segProgress) load(idx int) int64 { return p.completed[idx].Load() }

// store sets segment idx's completed bytes atomically. Only the owning worker
// writes its index, so there is never write-write contention.
func (p *segProgress) store(idx int, v int64) { p.completed[idx].Store(v) }

// snapshotInto folds the live counters back into segs[i].Completed, returning the
// summed completed bytes. Callers hold the structural mutex so the snapshot pairs
// the durable shape with a coherent set of live counters.
func (p *segProgress) snapshotInto(segs []Segment) int64 {
	var total int64
	for i := range segs {
		c := p.completed[i].Load()
		segs[i].Completed = c
		total += c
	}
	return total
}

// segmentAt returns a copy of segs[idx] with its live Completed folded in, for
// callers (e.g. checkpoint) that need a single up-to-date Segment value.
func (p *segProgress) segmentAt(segs []Segment, idx int) Segment {
	s := segs[idx]
	s.Completed = p.completed[idx].Load()
	return s
}
