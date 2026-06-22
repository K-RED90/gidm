package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/engine"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	want := sampleDownload("rt")
	if err := s.SaveDownload(ctx, want); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	got, err := s.LoadDownload(ctx, want.ID)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	assertDownloadEqual(t, got, want)
}

func TestSaveDownloadUpsertReplacesSegments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	d := sampleDownload("upsert")
	if err := s.SaveDownload(ctx, d); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	// Re-save with fewer segments and a changed status; the dropped segment must
	// not linger and the scalar fields must update.
	d.Status = engine.StatusCompleted
	d.Segments = []engine.Segment{{Index: 0, Start: 0, End: 999, Completed: 1000}}
	if err := s.SaveDownload(ctx, d); err != nil {
		t.Fatalf("SaveDownload (re-save): %v", err)
	}

	got, err := s.LoadDownload(ctx, d.ID)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	assertDownloadEqual(t, got, d)
}

func TestSaveDownloadRejectsNil(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)

	if err := s.SaveDownload(context.Background(), nil); err == nil {
		t.Error("SaveDownload(nil) = nil error, want error")
	}
}

func TestListDownloads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	ids := []string{"a", "b", "c"}
	for _, id := range ids {
		if err := s.SaveDownload(ctx, sampleDownload(id)); err != nil {
			t.Fatalf("SaveDownload %q: %v", id, err)
		}
	}

	got, err := s.ListDownloads(ctx)
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("len = %d, want %d", len(got), len(ids))
	}
	for _, d := range got {
		if len(d.Segments) == 0 {
			t.Errorf("download %q loaded without segments", d.ID)
		}
	}
}

func TestDeleteDownload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	d := sampleDownload("del")
	if err := s.SaveDownload(ctx, d); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	if err := s.DeleteDownload(ctx, d.ID); err != nil {
		t.Fatalf("DeleteDownload: %v", err)
	}

	if _, err := s.LoadDownload(ctx, d.ID); !errors.Is(err, engine.ErrNotFound) {
		t.Errorf("LoadDownload after delete = %v, want ErrNotFound", err)
	}

	// Deleting a missing download is a no-op, not an error.
	if err := s.DeleteDownload(ctx, "does-not-exist"); err != nil {
		t.Errorf("DeleteDownload(missing) = %v, want nil", err)
	}
}

func TestLoadMissingDownload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.LoadDownload(ctx, "nope"); !errors.Is(err, engine.ErrNotFound) {
		t.Errorf("LoadDownload(missing) = %v, want ErrNotFound", err)
	}
}

// TestPriorityRoundTrip saves each priority level and asserts it survives both a
// single Load and a List.
func TestPriorityRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	levels := map[string]engine.Priority{
		"low":    engine.PriorityLow,
		"normal": engine.PriorityNormal,
		"high":   engine.PriorityHigh,
	}
	for id, p := range levels {
		d := sampleDownload(id)
		d.Priority = p
		if err := s.SaveDownload(ctx, d); err != nil {
			t.Fatalf("SaveDownload %q: %v", id, err)
		}
		got, err := s.LoadDownload(ctx, id)
		if err != nil {
			t.Fatalf("LoadDownload %q: %v", id, err)
		}
		if got.Priority != p {
			t.Errorf("Load %q priority = %v, want %v", id, got.Priority, p)
		}
	}

	list, err := s.ListDownloads(ctx)
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	for _, d := range list {
		if d.Priority != levels[d.ID] {
			t.Errorf("List %q priority = %v, want %v", d.ID, d.Priority, levels[d.ID])
		}
	}
}

// legacySchema is the downloads/segments/settings schema as it stood before the
// priority column existed. A test writes a database with it to prove the additive
// migration backfills downloads.priority on an older database.
const legacySchema = `
CREATE TABLE downloads (
	id            TEXT PRIMARY KEY,
	url           TEXT NOT NULL,
	destination   TEXT NOT NULL,
	total_size    INTEGER NOT NULL,
	status        TEXT NOT NULL,
	etag          TEXT NOT NULL,
	last_modified TEXT NOT NULL,
	checksum      TEXT NOT NULL,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);
CREATE TABLE segments (
	download_id TEXT NOT NULL,
	idx         INTEGER NOT NULL,
	"start"     INTEGER NOT NULL,
	"end"       INTEGER NOT NULL,
	completed   INTEGER NOT NULL,
	PRIMARY KEY (download_id, idx)
);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`

// TestMigrationBackfillsPriorityForLegacyDB writes a database with the pre-priority
// schema, then opens it via New (running the migration) and asserts the added
// column defaults existing rows to PriorityNormal so an older database keeps loading.
func TestMigrationBackfillsPriorityForLegacyDB(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := db.ExecContext(ctx, legacySchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.ExecContext(ctx, `INSERT INTO downloads
		(id, url, destination, total_size, status, etag, last_modified, checksum, created_at, updated_at)
		VALUES ('old1', 'https://example.com/old.bin', '/tmp/old.bin', 1000, 'queued', '', '', '', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	// New runs migrate, which must ADD the missing priority column without dropping data.
	s, err := New(path)
	if err != nil {
		t.Fatalf("New (migrate legacy db): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	got, err := s.LoadDownload(ctx, "old1")
	if err != nil {
		t.Fatalf("LoadDownload after migration: %v", err)
	}
	if got.Priority != engine.PriorityNormal {
		t.Errorf("legacy row priority = %v, want normal (default)", got.Priority)
	}

	// The migration is idempotent: re-opening must not fail trying to re-add the column.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	again, err := New(path)
	if err != nil {
		t.Fatalf("New (reopen after migration): %v", err)
	}
	t.Cleanup(func() { _ = again.Close() })
}

// TestSegmentCountRoundTrip asserts a per-download segment override survives a
// save/load and a re-save (the upsert UPDATE path), independent of priority.
func TestSegmentCountRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	d := sampleDownload("segs")
	d.SegmentCount = 16
	if err := s.SaveDownload(ctx, d); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}
	got, err := s.LoadDownload(ctx, "segs")
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if got.SegmentCount != 16 {
		t.Errorf("Load SegmentCount = %d, want 16", got.SegmentCount)
	}

	// Upsert UPDATE path must persist a changed count too.
	d.SegmentCount = 2
	if err := s.SaveDownload(ctx, d); err != nil {
		t.Fatalf("SaveDownload (update): %v", err)
	}
	if got, err = s.LoadDownload(ctx, "segs"); err != nil {
		t.Fatalf("LoadDownload (after update): %v", err)
	}
	if got.SegmentCount != 2 {
		t.Errorf("updated SegmentCount = %d, want 2", got.SegmentCount)
	}
}

// TestMigrationBackfillsSegmentCountForLegacyDB writes a database with the
// pre-segment_count schema and asserts New's additive migration adds the column,
// defaulting existing rows to 0 (meaning "use the configured default").
func TestMigrationBackfillsSegmentCountForLegacyDB(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-segs.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := db.ExecContext(ctx, legacySchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.ExecContext(ctx, `INSERT INTO downloads
		(id, url, destination, total_size, status, etag, last_modified, checksum, created_at, updated_at)
		VALUES ('old2', 'https://example.com/old.bin', '/tmp/old.bin', 1000, 'queued', '', '', '', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	s, err := New(path)
	if err != nil {
		t.Fatalf("New (migrate legacy db): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	got, err := s.LoadDownload(ctx, "old2")
	if err != nil {
		t.Fatalf("LoadDownload after migration: %v", err)
	}
	if got.SegmentCount != 0 {
		t.Errorf("legacy row SegmentCount = %d, want 0 (default)", got.SegmentCount)
	}
}
