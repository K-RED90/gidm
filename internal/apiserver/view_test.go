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
	v := toView(d)
	if v.Downloaded != 400 {
		t.Errorf("Downloaded = %d, want 400", v.Downloaded)
	}
	if v.SpeedBps != 200 {
		t.Errorf("SpeedBps = %d, want 200", v.SpeedBps)
	}
	if v.EtaSecs != 3 { // (1000-400)/200
		t.Errorf("EtaSecs = %d, want 3", v.EtaSecs)
	}
}

// Without a live rate (or with an unknown total) ETA is the -1 sentinel, never a
// misleading 0.
func TestToViewETAUnknown(t *testing.T) {
	noRate := toView(&engine.Download{ID: "a", TotalSize: 1000, SpeedBps: 0})
	if noRate.EtaSecs != -1 {
		t.Errorf("no-rate EtaSecs = %d, want -1", noRate.EtaSecs)
	}
	unknownTotal := toView(&engine.Download{ID: "b", TotalSize: 0, SpeedBps: 500})
	if unknownTotal.EtaSecs != -1 {
		t.Errorf("unknown-total EtaSecs = %d, want -1", unknownTotal.EtaSecs)
	}
}
