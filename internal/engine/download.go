package engine

import (
	"fmt"
	"time"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusActive    Status = "active"
	StatusPaused    Status = "paused"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"

	// StatusCanceled is a terminal state set by Manager.Cancel. It is distinct
	// from StatusFailed (which marks a genuine transfer error): a canceled
	// download stopped on an operator's request, keeps its .part file, and may be
	// re-enqueued by Manager.Resume.
	StatusCanceled Status = "canceled"
)

// Priority orders queued downloads: when a concurrency slot frees, the Manager
// runs the highest-priority queued download, breaking ties by enqueue order
// (stable FIFO). The zero value is PriorityNormal, so a record persisted before
// priorities existed (its column defaults to 0) and an add that omits a priority
// both load as normal. Higher value = scheduled sooner.
type Priority int

const (
	PriorityLow    Priority = -1
	PriorityNormal Priority = 0
	PriorityHigh   Priority = 1
)

func (p Priority) Valid() bool { return p >= PriorityLow && p <= PriorityHigh }

func (p Priority) String() string {
	switch p {
	case PriorityLow:
		return "low"
	case PriorityHigh:
		return "high"
	default:
		return "normal"
	}
}

// ParsePriority maps a wire/config spelling to a Priority. The empty string is
// normal, so an omitted value passes through unchanged as the default.
func ParsePriority(s string) (Priority, error) {
	switch s {
	case "", "normal":
		return PriorityNormal, nil
	case "low":
		return PriorityLow, nil
	case "high":
		return PriorityHigh, nil
	default:
		return PriorityNormal, fmt.Errorf("engine: invalid priority %q", s)
	}
}

type Download struct {
	ID          string
	URL         string
	Destination string
	TotalSize   int64
	Status      Status
	Priority    Priority

	// Revalidated on resume to detect that the remote file changed.
	ETag         string
	LastModified string

	Checksum string // e.g. "sha256:abc123", verified on completion

	// SegmentCount is a per-download override for how many ranged segments to plan
	// and how many transfer workers to fan out. Zero means "use the configured
	// SegmentsPerDownload". It is clamped to [1, MaxSegments] at submit time and
	// persisted, so a resume re-plans the same way. The whole-body fallback (no
	// range support / unknown size) ignores it.
	SegmentCount int

	Segments []Segment

	CreatedAt time.Time
	UpdatedAt time.Time

	// SpeedBps is the live transfer rate in bytes/sec, folded in by the Manager for
	// an active download (0 otherwise). Transient: the store never reads or writes
	// it — it exists only to carry the sampled rate out to a snapshot.
	SpeedBps int64
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
