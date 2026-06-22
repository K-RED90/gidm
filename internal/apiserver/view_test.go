package apiserver

import (
	"testing"

	"github.com/K-RED90/gidm/internal/engine"
)

func TestToViewSpeedAndETA(t *testing.T) {
	d := &engine.Download{
		ID:        "d1",
		TotalSize: 1000,
		Segments:  []engine.Segment{{Index: 0, Start: 0, End: 999, Completed: 400}},
		SpeedBps:  200,
	}
	v := toView(d, true)
	if v.Downloaded != 400 {
		t.Errorf("Downloaded = %d, want 400", v.Downloaded)
	}
	if v.SpeedBps != 200 {
		t.Errorf("SpeedBps = %d, want 200", v.SpeedBps)
	}
	if v.EtaSecs != 3 { // (1000-400)/200
		t.Errorf("EtaSecs = %d, want 3", v.EtaSecs)
	}
	// full view projects the per-connection breakdown.
	if len(v.Segments) != 1 || v.Segments[0].Completed != 400 || v.Segments[0].End != 999 {
		t.Errorf("Segments = %+v, want one segment with Completed=400 End=999", v.Segments)
	}
}

// The polled list view (full=false) omits the per-connection segments for a
// terminal download to keep the payload lean, but still includes them for an
// in-flight one.
func TestToViewListSegmentsGating(t *testing.T) {
	segs := []engine.Segment{{Index: 0, Start: 0, End: 9, Completed: 10}}
	done := toView(&engine.Download{ID: "d", Status: engine.StatusCompleted, Segments: segs}, false)
	if done.Segments != nil {
		t.Errorf("completed list view Segments = %+v, want nil", done.Segments)
	}
	active := toView(&engine.Download{ID: "d", Status: engine.StatusActive, Segments: segs}, false)
	if len(active.Segments) != 1 {
		t.Errorf("active list view Segments = %+v, want 1", active.Segments)
	}
}

// Without a live rate (or with an unknown total) ETA is the -1 sentinel, never a
// misleading 0.
func TestToViewETAUnknown(t *testing.T) {
	noRate := toView(&engine.Download{ID: "a", TotalSize: 1000, SpeedBps: 0}, false)
	if noRate.EtaSecs != -1 {
		t.Errorf("no-rate EtaSecs = %d, want -1", noRate.EtaSecs)
	}
	unknownTotal := toView(&engine.Download{ID: "b", TotalSize: 0, SpeedBps: 500}, false)
	if unknownTotal.EtaSecs != -1 {
		t.Errorf("unknown-total EtaSecs = %d, want -1", unknownTotal.EtaSecs)
	}
}
