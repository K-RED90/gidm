package engine

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("engine: not found")

// Store persists downloads and settings. Implementations must be safe for
// concurrent use.
type Store interface {
	SaveDownload(ctx context.Context, d *Download) error
	LoadDownload(ctx context.Context, id string) (*Download, error)
	ListDownloads(ctx context.Context) ([]*Download, error)
	DeleteDownload(ctx context.Context, id string) error

	// UpdateSegment is called on a checkpoint cadence, never on the per-chunk
	// hot path, so frequent byte updates never touch the database.
	UpdateSegment(ctx context.Context, downloadID string, seg Segment) error

	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error

	Close() error
}
