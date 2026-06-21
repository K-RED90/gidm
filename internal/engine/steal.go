package engine

import (
	"errors"
	"sync"
	"sync/atomic"
)

// errSegmentStolen is the sentinel a worker's progressWriter returns once it has
// filled up to its live End: it stopped at a buffer boundary because that boundary
// is the segment's end (a natural finish) or because a thief shrank the End to take
// the tail. runSegment confirms which under the coordinator lock (see settled); it
// is never a transfer failure and must not reach the run's fail()/cancel path.
var errSegmentStolen = errors.New("engine: segment filled to live end")

// livePlan is the run-time segment layout for a work-stealing transfer. It is
// pre-sized to maxSlots once and never reallocated, so the segProgress backing
// array it pairs with (also maxSlots long) is never moved while the Manager's
// observer reads it.
//
// starts[i] is the immutable Start of slot i; it is written once when the slot is
// activated under stealCoord.mu and only ever read afterwards, so the worker's
// lock-free read is safe by the happens-before edge of that mutex handoff. ends[i]
// is the slot's live End: a thief shrinks a donor's end atomically and the donor's
// worker re-reads it at flush boundaries (off the per-byte path) to stop before the
// donated tail. ends must be atomic because two goroutines touch it; starts need
// not be because only the activating thief writes it, before any reader sees the
// slot.
type livePlan struct {
	starts []int64
	ends   []atomic.Int64
}

// newLivePlan seeds the first len(segs) slots from segs and reserves room up to
// maxSlots for stolen sub-segments. maxSlots is clamped up to len(segs).
func newLivePlan(segs []Segment, maxSlots int) *livePlan {
	if maxSlots < len(segs) {
		maxSlots = len(segs)
	}
	p := &livePlan{
		starts: make([]int64, maxSlots),
		ends:   make([]atomic.Int64, maxSlots),
	}
	for i := range segs {
		p.starts[i] = segs[i].Start
		p.ends[i].Store(segs[i].End)
	}
	return p
}

func (p *livePlan) start(idx int) int64   { return p.starts[idx] }
func (p *livePlan) endLoad(idx int) int64 { return p.ends[idx].Load() }

// stealResult carries the two segments a steal reshaped: the shrunk donor and the
// new tail. The worker persists them via UpdateSegment after stealCoord.next
// returns (i.e. off the coordinator mutex) so a slow store never stalls work
// dispatch, while a mid-run crash still resumes a (repairable) layout.
type stealResult struct {
	donor Segment
	tail  Segment
}

// stealCoord dispatches segments to a fixed pool of workers and rebalances ranges
// mid-flight. All mutable state — cursor, active, donor shrink, slot activation, and
// the donor's done-decision (settled) — is serialized by mu, so a steal's tentative
// shrink and its undo can never race a donor's completion into a gap. The lock-free
// atomics in plan/prog are read under mu here for a coherent snapshot; the donor
// worker reads them lock-free on its hot path.
type stealCoord struct {
	mu     sync.Mutex
	plan   *livePlan
	prog   *segProgress
	cursor int   // next initial slot to hand out (0..n-1)
	active int   // number of activated slots (initial n, grows on each steal)
	n      int   // initial segment count
	max    int   // slot ceiling; active never exceeds it
	floor  int64 // a donor is split only when its unfetched remainder >= 2*floor
	margin int64 // safety gap kept between a donor's frontier and the split point
}

// newStealCoord builds the coordinator. floor is the minimum piece a split may
// produce; margin (one transfer buffer) is the gap kept between the donor's
// observed write frontier and the split point so the donor's last in-flight write
// can never cross into the stolen tail.
func newStealCoord(plan *livePlan, prog *segProgress, n, maxSlots int, floor, margin int64) *stealCoord {
	return &stealCoord{
		plan:   plan,
		prog:   prog,
		active: n,
		n:      n,
		max:    maxSlots,
		floor:  floor,
		margin: margin,
	}
}

// next returns the slot a calling worker should transfer. It hands out the initial
// segments first, then steals the tail of the fattest in-flight segment. When the
// returned slot came from a steal, res is non-nil and the caller must persist
// res.donor and res.tail (off-lock). ok is false when no work and no viable steal
// remain, signalling the worker to exit — remainders only ever shrink, so once
// nothing is stealable nothing will become stealable later.
func (c *stealCoord) next() (idx int, res *stealResult, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cursor < c.n {
		idx = c.cursor
		c.cursor++
		return idx, nil, true
	}
	return c.trySteal()
}

// trySteal splits the fattest stealable donor and activates a new tail slot, all
// under mu. It tentatively shrinks the donor's End, then re-reads the donor's
// frontier: a preempted thief could let a fast donor advance toward the split
// point while the End was being shrunk, so if the donor is no longer at least
// margin bytes behind the split, the shrink is undone and that donor is excluded
// for this call. A donor whose End was undone after it already clipped re-checks
// completeness under mu (settled) and re-fetches the restored remainder, so an undo
// can never strand bytes.
//
// Steal-safety invariant: the stolen tail [mid+1, oldEnd] is disjoint from every
// byte the donor has written or will write. The commit happens only when the
// donor's published frontier (start+completed) plus its single in-flight buffer
// (<= margin) is still at or below mid; every donor write after the shrink reads the
// new End and clips at mid. So the donor stops at or before mid and the tail starts
// at mid+1: no double-write, no gap.
func (c *stealCoord) trySteal() (int, *stealResult, bool) {
	var excluded []int
	for c.active < c.max {
		donorIdx, donorFrom, rem := c.fattest(excluded)
		if donorIdx < 0 || rem < 2*c.floor {
			return 0, nil, false
		}
		oldEnd := c.plan.ends[donorIdx].Load()
		donorStart := c.plan.starts[donorIdx]
		mid := donorFrom + rem/2 - 1 // donor's new inclusive End; it keeps [start, mid]

		c.plan.ends[donorIdx].Store(mid)

		// Did the donor advance into the safety margin while we shrank it?
		frontier := donorStart + c.prog.completed[donorIdx].Load()
		if frontier > mid-c.margin {
			c.plan.ends[donorIdx].Store(oldEnd) // undo; the donor keeps its full range
			excluded = append(excluded, donorIdx)
			continue
		}

		k := c.active
		c.plan.starts[k] = mid + 1
		c.plan.ends[k].Store(oldEnd)
		c.prog.completed[k].Store(0)
		c.active++
		return k, &stealResult{
			donor: Segment{Index: donorIdx, Start: donorStart, End: mid, Completed: donorFrom - donorStart},
			tail:  Segment{Index: k, Start: mid + 1, End: oldEnd, Completed: 0},
		}, true
	}
	return 0, nil, false
}

// fattest returns the active, non-excluded slot with the largest unfetched
// remainder, its first unfetched byte, and that remainder. idx is -1 when no slot
// qualifies.
func (c *stealCoord) fattest(excluded []int) (idx int, from int64, rem int64) {
	idx = -1
	for i := range c.active {
		if contains(excluded, i) {
			continue
		}
		f := c.plan.starts[i] + c.prog.completed[i].Load()
		r := c.plan.ends[i].Load() - f + 1
		if r > rem {
			idx, from, rem = i, f, r
		}
	}
	return idx, from, rem
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// settled reports, under the coordinator lock, whether segment idx has reached its
// current End — i.e. the worker that filled up to a clip boundary is genuinely done
// (a natural finish, or a steal that committed) rather than racing a steal that was
// undone (which restored a larger End to re-fetch). Taking mu here serializes the
// donor's done-decision with trySteal's shrink/undo, closing the gap window.
func (c *stealCoord) settled(idx int, completed int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return completed >= c.plan.ends[idx].Load()-c.plan.starts[idx]+1
}

// activeSegments rebuilds the durable segment slice from the live plan once all
// workers have stopped (no concurrency), so verifyComplete and the persisted record
// reflect the final post-steal layout. Index i pairs with slot i because slots are
// append-only and never reordered.
func (c *stealCoord) activeSegments() []Segment {
	segs := make([]Segment, c.active)
	for i := range c.active {
		segs[i] = Segment{
			Index:     i,
			Start:     c.plan.starts[i],
			End:       c.plan.ends[i].Load(),
			Completed: c.prog.load(i),
		}
	}
	return segs
}
