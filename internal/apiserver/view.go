package apiserver

import (
	"time"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/internal/engine"
)

// toView projects an engine.Download onto the flat, pure-data api.DownloadView
// returned over the wire. This is the single boundary where engine types become
// wire types, so the wire shape can evolve independently of the engine model.
// Downloaded is summed from the segments' Completed counters and SpeedBps is the
// Manager's sampled rate (it folds both into d before this runs). EtaSecs is
// derived here: remaining bytes over the live rate, or -1 when it cannot be known
// (no live rate or unknown total).
//
// full controls the per-connection Segments slice: the status endpoint passes
// true so the properties view always sees the breakdown, while the polled list
// passes false and includes Segments only for in-flight downloads — keeping the
// frequent list payload lean while still feeding the table's live segmented bar.
func toView(d *engine.Download, full bool) api.DownloadView {
	var downloaded int64
	for _, seg := range d.Segments {
		downloaded += seg.Completed
	}
	eta := int64(-1)
	if d.SpeedBps > 0 && d.TotalSize > 0 {
		remaining := max(d.TotalSize-downloaded, 0)
		eta = remaining / d.SpeedBps
	}
	v := api.DownloadView{
		ID:           d.ID,
		URL:          d.URL,
		Status:       statusToView(d.Status),
		Priority:     priorityToView(d.Priority),
		TotalSize:    d.TotalSize,
		Downloaded:   downloaded,
		SpeedBps:     d.SpeedBps,
		EtaSecs:      eta,
		Destination:  d.Destination,
		Checksum:     d.Checksum,
		SegmentCount: d.SegmentCount,
		MaxRate:      d.MaxRate,
		CreatedAt:    formatTime(d.CreatedAt),
		UpdatedAt:    formatTime(d.UpdatedAt),
	}
	if full || d.Status == engine.StatusActive || d.Status == engine.StatusPaused {
		v.Segments = segmentsToView(d.Segments)
	}
	return v
}

// toConfigView projects the engine's runtime Settings onto the api.ConfigView
// wire shape (the get-config result and the set-config echo).
func toConfigView(s engine.Settings) api.ConfigView {
	return api.ConfigView{
		DownloadDir:         s.DownloadDir,
		SegmentsPerDownload: s.SegmentsPerDownload,
		DefaultPriority:     priorityToView(s.DefaultPriority),
		MaxRate:             s.MaxRate,
		PerDownloadMaxRate:  s.PerDownloadMaxRate,
	}
}

// applyConfigPatch overlays a set-config request's present (non-nil) fields onto
// the current settings, leaving the rest unchanged. The result is the full
// settings to apply and persist — the merge that gives set-config its patch
// semantics.
func applyConfigPatch(cur engine.Settings, sc api.SetConfig) engine.Settings {
	if sc.DownloadDir != nil {
		cur.DownloadDir = *sc.DownloadDir
	}
	if sc.SegmentsPerDownload != nil {
		cur.SegmentsPerDownload = *sc.SegmentsPerDownload
	}
	if sc.DefaultPriority != nil {
		cur.DefaultPriority = priorityFromView(*sc.DefaultPriority, cur.DefaultPriority)
	}
	if sc.MaxRate != nil {
		cur.MaxRate = *sc.MaxRate
	}
	if sc.PerDownloadMaxRate != nil {
		cur.PerDownloadMaxRate = *sc.PerDownloadMaxRate
	}
	return cur
}

// segmentsToView projects the engine's per-segment progress onto the wire shape.
func segmentsToView(segs []engine.Segment) []api.SegmentView {
	if len(segs) == 0 {
		return nil
	}
	out := make([]api.SegmentView, len(segs))
	for i, s := range segs {
		out[i] = api.SegmentView{Index: s.Index, Start: s.Start, End: s.End, Completed: s.Completed}
	}
	return out
}

// formatTime renders a timestamp as RFC3339, or "" for the zero value so the
// field is omitted on the wire.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// priorityToView maps an engine.Priority to the api.Priority wire enum; an
// out-of-range value (which should never reach the wire) projects to normal.
func priorityToView(p engine.Priority) api.Priority {
	switch p {
	case engine.PriorityLow:
		return api.PriorityLow
	case engine.PriorityHigh:
		return api.PriorityHigh
	default:
		return api.PriorityNormal
	}
}

// priorityFromView maps a wire priority to an engine.Priority. The empty string
// resolves to fallback (the server's configured default), so an add that omits
// priority gets the daemon default rather than a hardcoded normal. Unknown values
// are already rejected by api.ValidatePriority before this runs.
func priorityFromView(p api.Priority, fallback engine.Priority) engine.Priority {
	switch p {
	case api.PriorityLow:
		return engine.PriorityLow
	case api.PriorityNormal:
		return engine.PriorityNormal
	case api.PriorityHigh:
		return engine.PriorityHigh
	default:
		return fallback
	}
}

// statusToView maps an engine.Status to the api.DownloadStatus wire enum. The
// engine's queued/active/paused/completed/failed map 1:1. The wire protocol has
// no "canceled" status, so engine.StatusCanceled — an operator-stopped,
// resumable state — is mapped to api.StatusPaused, its closest wire equivalent.
// This is the single documented drift point per api/view.go's note: if
// a client ever needs to distinguish canceled, the wire enum must gain it first.
func statusToView(s engine.Status) api.DownloadStatus {
	switch s {
	case engine.StatusQueued:
		return api.StatusQueued
	case engine.StatusActive:
		return api.StatusActive
	case engine.StatusPaused, engine.StatusCanceled:
		return api.StatusPaused
	case engine.StatusCompleted:
		return api.StatusCompleted
	case engine.StatusFailed:
		return api.StatusFailed
	default:
		// An unknown engine status should never reach the wire; failed is the safest
		// conservative projection.
		return api.StatusFailed
	}
}
