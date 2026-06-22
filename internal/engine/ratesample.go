package engine

import "time"

// Rate sampling turns the engine's cumulative live byte counters into a smoothed
// transfer rate, off the per-chunk hot path (CLAUDE.md principle 2): one goroutine
// samples every active download once per interval and folds the instantaneous
// rate into an exponential moving average. The Manager surfaces the latest
// estimate through Download.SpeedBps so every client shows the same
// server-authoritative speed instead of each deriving its own from poll deltas.

const (
	// rateSampleInterval is how often the sampler reads the live counters. One
	// second matches the typical client poll cadence and keeps the average
	// responsive without busy-sampling.
	rateSampleInterval = time.Second
	// rateEWMAAlpha weights the newest instantaneous rate against the running
	// average; 0.4 smooths burst jitter while still tracking real speed changes.
	rateEWMAAlpha = 0.4
)

// rateState carries the previous byte/time sample and the smoothed rate for one
// active download. It lives under the Manager mutex alongside the live registry.
type rateState struct {
	lastBytes int64
	lastAt    time.Time
	bps       int64

	// Per-connection smoothed rates, one slot per live segment (IDM-style
	// per-connection speed). segLast is each slot's byte count at lastAt; segBps is
	// its EWMA bytes/sec. The download's live counters are pre-sized to the
	// work-stealing slot ceiling and never reallocated, so these slices, sized once
	// from that count, stay index-aligned for the life of the download.
	segLast []int64
	segBps  []int64
}

// sampleLoop runs until the base context is cancelled (Shutdown), sampling every
// active download once per interval. It is part of the worker WaitGroup, so a
// Shutdown waits for it to exit before returning.
func (m *Manager) sampleLoop() {
	defer m.workers.Done()
	t := time.NewTicker(rateSampleInterval)
	defer t.Stop()
	for {
		select {
		case <-m.baseCtx.Done():
			return
		case <-t.C:
			m.sample(m.clock())
		}
	}
}

// sample folds one rate observation for every active download into its EWMA at
// time now. The first observation for a download only seeds the baseline (no rate
// yet); later ones yield a smoothed bytes/sec. It is split out from sampleLoop so
// tests can drive deterministic timestamps without a goroutine or real clock.
func (m *Manager) sample(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, prog := range m.live {
		cur := prog.total()
		rs := m.rates[id]
		if rs == nil {
			m.rates[id] = &rateState{
				lastBytes: cur,
				lastAt:    now,
				segLast:   prog.loadAll(),
				segBps:    make([]int64, len(prog.completed)),
			}
			continue
		}
		dt := now.Sub(rs.lastAt).Seconds()
		if dt <= 0 {
			continue
		}
		rs.bps = ewma(rs.bps, instRate(cur, rs.lastBytes, dt))
		rs.lastBytes = cur
		sampleSegments(rs, prog, dt)
		rs.lastAt = now
	}
}

// sampleSegments folds one per-connection rate observation into rs.segBps. The
// slices are seeded to the live slot count (stable for the download's life), so a
// length guard only ever re-seeds defensively.
func sampleSegments(rs *rateState, prog *segProgress, dt float64) {
	n := len(prog.completed)
	if len(rs.segLast) != n || len(rs.segBps) != n {
		rs.segLast = prog.loadAll()
		rs.segBps = make([]int64, n)
		return // re-seeded this tick; a rate needs two observations
	}
	for i := 0; i < n; i++ {
		cur := prog.load(i)
		rs.segBps[i] = ewma(rs.segBps[i], instRate(cur, rs.segLast[i], dt))
		rs.segLast[i] = cur
	}
}

// instRate is the non-negative instantaneous bytes/sec between two cumulative
// counts over dt seconds. A counter that fell (a restart from scratch) reports
// zero rather than a negative rate.
func instRate(cur, last int64, dt float64) float64 {
	r := float64(cur-last) / dt
	if r < 0 {
		return 0
	}
	return r
}

// ewma folds an instantaneous rate into a running average; the first observation
// (prev == 0) seeds it.
func ewma(prev int64, inst float64) int64 {
	if prev == 0 {
		return int64(inst)
	}
	return int64(rateEWMAAlpha*inst + (1-rateEWMAAlpha)*float64(prev))
}
