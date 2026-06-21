package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/K-RED90/gidm/internal/engine"
)

// UpdateSegment upserts a single segment row; the engine calls this on a
// checkpoint cadence, never on the per-chunk hot path.
func (s *Store) UpdateSegment(ctx context.Context, downloadID string, seg engine.Segment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, upsertSegment,
		downloadID, seg.Index, seg.Start, seg.End, seg.Completed)
	if err != nil {
		return fmt.Errorf("sqlite: update segment %d of %q: %w", seg.Index, downloadID, err)
	}
	return nil
}

func (s *Store) loadSegments(ctx context.Context, downloadID string) ([]engine.Segment, error) {
	rows, err := s.db.QueryContext(ctx, selectSegments, downloadID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: load segments %q: %w", downloadID, err)
	}
	defer func() { _ = rows.Close() }()

	segs := []engine.Segment{}
	for rows.Next() {
		var seg engine.Segment
		if err := rows.Scan(&seg.Index, &seg.Start, &seg.End, &seg.Completed); err != nil {
			return nil, fmt.Errorf("sqlite: load segments %q: scan: %w", downloadID, err)
		}
		segs = append(segs, seg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: load segments %q: %w", downloadID, err)
	}
	return segs, nil
}

// replaceSegments swaps the download's entire segment set within tx, keeping
// SaveDownload atomic. The wholesale delete ensures segments dropped since the
// last save do not linger.
func replaceSegments(ctx context.Context, tx *sql.Tx, downloadID string, segs []engine.Segment) error {
	if _, err := tx.ExecContext(ctx, deleteSegments, downloadID); err != nil {
		return fmt.Errorf("sqlite: clear segments %q: %w", downloadID, err)
	}
	for _, seg := range segs {
		_, err := tx.ExecContext(ctx, upsertSegment,
			downloadID, seg.Index, seg.Start, seg.End, seg.Completed)
		if err != nil {
			return fmt.Errorf("sqlite: save segment %d of %q: %w", seg.Index, downloadID, err)
		}
	}
	return nil
}

const upsertSegment = `
INSERT INTO segments (download_id, idx, "start", "end", completed)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(download_id, idx) DO UPDATE SET
	"start"   = excluded."start",
	"end"     = excluded."end",
	completed = excluded.completed;`

const deleteSegments = `DELETE FROM segments WHERE download_id = ?;`

const selectSegments = `
SELECT idx, "start", "end", completed FROM segments WHERE download_id = ? ORDER BY idx;`
