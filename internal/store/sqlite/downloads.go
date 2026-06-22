package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/K-RED90/gidm/internal/engine"
)

// SaveDownload upserts the download and its full set of segments atomically.
func (s *Store) SaveDownload(ctx context.Context, d *engine.Download) error {
	if d == nil {
		return errors.New("sqlite: save download: nil download")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: save download %q: begin: %w", d.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, upsertDownload,
		d.ID, d.URL, d.Destination, d.TotalSize, string(d.Status),
		d.ETag, d.LastModified, d.Checksum,
		formatTime(d.CreatedAt), formatTime(d.UpdatedAt), int(d.Priority), d.SegmentCount, d.MaxRate,
	)
	if err != nil {
		return fmt.Errorf("sqlite: save download %q: %w", d.ID, err)
	}

	if err := replaceSegments(ctx, tx, d.ID, d.Segments); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: save download %q: commit: %w", d.ID, err)
	}
	return nil
}

// LoadDownload returns the download with its segments ordered by index, or
// engine.ErrNotFound if no such download exists.
func (s *Store) LoadDownload(ctx context.Context, id string) (*engine.Download, error) {
	d, err := scanDownload(s.db.QueryRowContext(ctx, selectDownload, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("sqlite: load download %q: %w", id, engine.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: load download %q: %w", id, err)
	}

	d.Segments, err = s.loadSegments(ctx, id)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// ListDownloads returns every download, each with its segments, ordered by
// creation time.
func (s *Store) ListDownloads(ctx context.Context) ([]*engine.Download, error) {
	rows, err := s.db.QueryContext(ctx, selectDownloads)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list downloads: %w", err)
	}
	defer func() { _ = rows.Close() }()

	downloads := []*engine.Download{}
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list downloads: %w", err)
		}
		downloads = append(downloads, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list downloads: %w", err)
	}

	for _, d := range downloads {
		if d.Segments, err = s.loadSegments(ctx, d.ID); err != nil {
			return nil, err
		}
	}
	return downloads, nil
}

// DeleteDownload removes a download and (by cascade) its segments. Deleting a
// missing download is not an error.
func (s *Store) DeleteDownload(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.ExecContext(ctx, deleteDownload, id); err != nil {
		return fmt.Errorf("sqlite: delete download %q: %w", id, err)
	}
	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, letting scanDownload
// serve single-row and multi-row queries alike.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanDownload(sc rowScanner) (*engine.Download, error) {
	var (
		d                    engine.Download
		status               string
		createdAt, updatedAt string
		priority             int
	)
	err := sc.Scan(&d.ID, &d.URL, &d.Destination, &d.TotalSize, &status,
		&d.ETag, &d.LastModified, &d.Checksum, &createdAt, &updatedAt, &priority, &d.SegmentCount, &d.MaxRate)
	if err != nil {
		return nil, err
	}

	d.Status = engine.Status(status)
	d.Priority = engine.Priority(priority)
	if d.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, fmt.Errorf("created_at %q: %w", createdAt, err)
	}
	if d.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, fmt.Errorf("updated_at %q: %w", updatedAt, err)
	}
	return &d, nil
}

const upsertDownload = `
INSERT INTO downloads
	(id, url, destination, total_size, status, etag, last_modified, checksum, created_at, updated_at, priority, segment_count, max_rate)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	url           = excluded.url,
	destination   = excluded.destination,
	total_size    = excluded.total_size,
	status        = excluded.status,
	etag          = excluded.etag,
	last_modified = excluded.last_modified,
	checksum      = excluded.checksum,
	updated_at    = excluded.updated_at,
	priority      = excluded.priority,
	segment_count = excluded.segment_count,
	max_rate      = excluded.max_rate;`

const selectDownload = `
SELECT id, url, destination, total_size, status, etag, last_modified, checksum, created_at, updated_at, priority, segment_count, max_rate
FROM downloads WHERE id = ?;`

const selectDownloads = `
SELECT id, url, destination, total_size, status, etag, last_modified, checksum, created_at, updated_at, priority, segment_count, max_rate
FROM downloads ORDER BY created_at, id;`

const deleteDownload = `DELETE FROM downloads WHERE id = ?;`
