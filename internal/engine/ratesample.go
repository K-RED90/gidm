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
			m.rates[id] = &rateState{lastBytes: cur, lastAt: now}
			continue
		}
		dt := now.Sub(rs.lastAt).Seconds()
		if dt <= 0 {
			continue
		}
		inst := float64(cur-rs.lastBytes) / dt
		if inst < 0 {
			inst = 0 // counters fell (a restart from scratch); never report negative
		}
		if rs.bps == 0 {
			rs.bps = int64(inst)
		} else {
			rs.bps = int64(rateEWMAAlpha*inst + (1-rateEWMAAlpha)*float64(rs.bps))
		}
		rs.lastBytes = cur
		rs.lastAt = now
	}
}
