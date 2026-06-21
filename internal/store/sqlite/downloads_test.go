package sqlite

import (
	"context"
	"errors"
	"testing"

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
