package apiserver

import (
	"github.com/K-RED90/gidm/internal/api"
	"github.com/K-RED90/gidm/internal/engine"
)

// toView projects an engine.Download onto the flat, pure-data api.DownloadView
// returned over the wire. This is the single boundary where engine types become
// wire types, so the wire shape can evolve independently of the engine model.
// Downloaded is summed from the segments' Completed counters (the Manager folds
// live progress into these before this runs).
func toView(d *engine.Download) api.DownloadView {
	var downloaded int64
	for _, seg := range d.Segments {
		downloaded += seg.Completed
	}
	return api.DownloadView{
		ID:          d.ID,
		URL:         d.URL,
		Status:      statusToView(d.Status),
		Priority:    priorityToView(d.Priority),
		TotalSize:   d.TotalSize,
		Downloaded:  downloaded,
		Destination: d.Destination,
	}
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
// This is the single documented drift point per internal/api/view.go's note: if
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
