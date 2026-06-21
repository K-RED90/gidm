package engine

import "time"

type Status string

const (
	StatusQueued    Status = "queued"
	StatusActive    Status = "active"
	StatusPaused    Status = "paused"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

type Download struct {
	ID          string
	URL         string
	Destination string
	TotalSize   int64
	Status      Status

	// Revalidated on resume to detect that the remote file changed.
	ETag         string
	LastModified string

	Checksum string // e.g. "sha256:abc123", verified on completion

	Segments []Segment

	CreatedAt time.Time
	UpdatedAt time.Time
}

type Segment struct {
	Index int
	Start int64 // inclusive
	End   int64 // inclusive

	// Bytes already written; on resume the worker requests from Start+Completed.
	Completed int64
}

func (s Segment) Size() int64      { return s.End - s.Start + 1 }
func (s Segment) Remaining() int64 { return s.Size() - s.Completed }
func (s Segment) Done() bool       { return s.Completed >= s.Size() }
