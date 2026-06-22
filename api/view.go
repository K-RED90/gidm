package api

// DownloadStatus is a download's lifecycle state on the wire. These constants
// mirror internal/engine's Status values as plain strings but are declared here
// so the CLI never transitively imports the engine. They can drift if the
// engine gains a status; the daemon's engine->view mapping is the single place
// that must be updated to reflect such a change.
type DownloadStatus string

const (
	StatusQueued    DownloadStatus = "queued"
	StatusActive    DownloadStatus = "active"
	StatusPaused    DownloadStatus = "paused"
	StatusCompleted DownloadStatus = "completed"
	StatusFailed    DownloadStatus = "failed"
)

// Priority is a download's scheduling priority on the wire. Like DownloadStatus
// it mirrors internal/engine's Priority as plain strings so the CLI never imports
// the engine; the daemon's engine<->view mapping is the single translation point.
// The empty string is treated as PriorityNormal, so a client that omits priority
// (and an older client unaware of the field) keeps working.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
)

// DownloadView is the pure-data projection of a download returned by status and
// list. It is deliberately not engine.Download: keeping a separate, flat view
// keeps this package a leaf and lets the wire shape evolve independently of the
// engine's internal model.
type DownloadView struct {
	ID         string         `json:"id"`
	URL        string         `json:"url"`
	Status     DownloadStatus `json:"status"`
	Priority   Priority       `json:"priority"`
	TotalSize  int64          `json:"total_size"`
	Downloaded int64          `json:"downloaded"`
	// SpeedBps is the live transfer rate in bytes/sec (0 when not downloading).
	// EtaSecs is the estimated seconds remaining, or -1 when unknown (no live rate
	// or unknown total). Both are server-computed so every client shows the same
	// value rather than each deriving its own from poll deltas.
	SpeedBps    int64  `json:"speed_bps"`
	EtaSecs     int64  `json:"eta_secs"`
	Destination string `json:"destination"`

	// The fields below are optional detail, populated for the properties view and
	// (for Segments) the per-connection progress display. They are omitempty so the
	// common list payload stays lean and older clients that ignore them are unaffected.
	Checksum     string `json:"checksum,omitempty"`      // e.g. "sha256:…", empty when none
	SegmentCount int    `json:"segment_count,omitempty"` // parallel connections planned
	MaxRate      int    `json:"max_rate,omitempty"`      // per-download cap, bytes/sec; 0 = inherit default
	CreatedAt    string `json:"created_at,omitempty"`    // RFC3339, empty when zero
	UpdatedAt    string `json:"updated_at,omitempty"`    // RFC3339, empty when zero

	// Segments carries each connection's byte range and live progress (IDM-style
	// "download progress by connections"). To keep the polled list lean it is
	// included only for in-flight downloads there; the status endpoint always
	// includes it.
	Segments []SegmentView `json:"segments,omitempty"`
}

// SegmentView is one parallel connection's byte range and live downloaded count.
// Completed is the live, in-flight byte total for an active download (the daemon
// folds the worker counters in before projecting), so a client can show each
// connection's progress as Completed/(End-Start+1).
type SegmentView struct {
	Index     int   `json:"index"`
	Start     int64 `json:"start"`     // inclusive
	End       int64 `json:"end"`       // inclusive
	Completed int64 `json:"completed"` // bytes written within the range
}

type AddResult struct {
	ID string `json:"id"`
}

type ListResult struct {
	Downloads []DownloadView `json:"downloads"`
}

type StatusResult struct {
	Download DownloadView `json:"download"`
}

// ConfigView is the daemon's current runtime-tunable configuration, returned by
// get-config and echoed by set-config. Rates are bytes/sec; 0 means unlimited.
// The non-tunable engine internals (buffer size, retries, timeouts, work-stealing)
// are deliberately not exposed — they stay config-file-only.
type ConfigView struct {
	DownloadDir         string   `json:"download_dir"`
	SegmentsPerDownload int      `json:"segments_per_download"`
	DefaultPriority     Priority `json:"default_priority"`
	MaxRate             int      `json:"max_rate"`              // engine-wide cap
	PerDownloadMaxRate  int      `json:"per_download_max_rate"` // default per-download cap
}

type ConfigResult struct {
	Config ConfigView `json:"config"`
}

// PingResult carries the server's protocol version. Uptime is intentionally
// omitted: ping is a pure health/version check and exposing process uptime
// would leak server state with no client use.
type PingResult struct {
	Version int `json:"version"`
}
