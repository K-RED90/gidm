package engine

import (
	"testing"
	"time"
)

// newRateTestManager builds a Manager with just the maps the sampler touches, so
// the rate math can be exercised without an engine, store, or goroutine.
func newRateTestManager() *Manager {
	return &Manager{
		live:  make(map[string]*segProgress),
		rates: make(map[string]*rateState),
	}
}

func TestManagerSampleComputesRate(t *testing.T) {
	m := newRateTestManager()
	prog := newSegProgressSized([]Segment{{Index: 0, Start: 0, End: 9999}}, 1)
	m.trackProgress("d1", prog)

	t0 := time.Unix(1000, 0)
	m.sample(t0) // first sample seeds the baseline only
	if got := m.rates["d1"].bps; got != 0 {
		t.Fatalf("seed bps = %d, want 0", got)
	}

	prog.store(0, 1000) // 1000 bytes over the next second
	m.sample(t0.Add(time.Second))
	if got := m.rates["d1"].bps; got != 1000 {
		t.Fatalf("bps after 1000 B/s = %d, want 1000", got)
	}

	prog.store(0, 2000) // steady 1000 B/s keeps the EWMA at 1000
	m.sample(t0.Add(2 * time.Second))
	if got := m.rates["d1"].bps; got != 1000 {
		t.Fatalf("steady bps = %d, want 1000", got)
	}
}

// A counter that falls (a restart from scratch) must not produce a negative rate.
func TestManagerSampleNeverNegative(t *testing.T) {
	m := newRateTestManager()
	prog := newSegProgressSized([]Segment{{Index: 0, Start: 0, End: 9999}}, 1)
	m.trackProgress("d1", prog)

	t0 := time.Unix(0, 0)
	prog.store(0, 5000)
	m.sample(t0)
	prog.store(0, 1000) // dropped (re-probe restarted the download)
	m.sample(t0.Add(time.Second))
	if got := m.rates["d1"].bps; got < 0 {
		t.Fatalf("bps = %d, want >= 0", got)
	}
}

func TestManagerUntrackDropsRate(t *testing.T) {
	m := newRateTestManager()
	prog := newSegProgressSized([]Segment{{Index: 0, Start: 0, End: 9}}, 1)
	m.trackProgress("d1", prog)
	m.sample(time.Unix(0, 0))
	m.untrackProgress("d1")
	if _, ok := m.rates["d1"]; ok {
		t.Fatal("rate state lingered after untrackProgress")
	}
}

func TestSegProgressTotalSumsAllSlots(t *testing.T) {
	p := newSegProgressSized([]Segment{{Index: 0}, {Index: 1}}, 4) // 2 segments, 4 slots
	p.store(0, 10)
	p.store(1, 20)
	p.store(3, 5) // a work-stealing slot beyond the original two
	if got := p.total(); got != 35 {
		t.Fatalf("total = %d, want 35", got)
	}
}

// Per-connection speed is sampled independently per slot and folded into each
// Segment for the properties view's per-connection breakdown.
func TestManagerSamplePerSegmentRate(t *testing.T) {
	m := newRateTestManager()
	segs := []Segment{{Index: 0, Start: 0, End: 4999}, {Index: 1, Start: 5000, End: 9999}}
	prog := newSegProgressSized(segs, 2)
	m.trackProgress("d1", prog)

	t0 := time.Unix(1000, 0)
	m.sample(t0) // seed only

	prog.store(0, 1000) // connection 0: 1000 B/s
	prog.store(1, 3000) // connection 1: 3000 B/s
	m.sample(t0.Add(time.Second))

	rs := m.rates["d1"]
	if len(rs.segBps) != 2 || rs.segBps[0] != 1000 || rs.segBps[1] != 3000 {
		t.Fatalf("segBps = %v, want [1000 3000]", rs.segBps)
	}

	// foldLiveProgress carries the per-connection rate out to the snapshot.
	d := &Download{ID: "d1", Segments: append([]Segment(nil), segs...)}
	m.foldLiveProgress(d)
	if d.Segments[0].SpeedBps != 1000 || d.Segments[1].SpeedBps != 3000 {
		t.Fatalf("folded segment speeds = [%d %d], want [1000 3000]", d.Segments[0].SpeedBps, d.Segments[1].SpeedBps)
	}
}
