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

// DownloadView is the pure-data projection of a download returned by status and
// list. It is deliberately not engine.Download: keeping a separate, flat view
// keeps this package a leaf and lets the wire shape evolve independently of the
// engine's internal model.
type DownloadView struct {
	ID          string         `json:"id"`
	URL         string         `json:"url"`
	Status      DownloadStatus `json:"status"`
	TotalSize   int64          `json:"total_size"`
	Downloaded  int64          `json:"downloaded"`
	Destination string         `json:"destination"`
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

// PingResult carries the server's protocol version. Uptime is intentionally
// omitted: ping is a pure health/version check and exposing process uptime
// would leak server state with no client use.
type PingResult struct {
	Version int `json:"version"`
}
