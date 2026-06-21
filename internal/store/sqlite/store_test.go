package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/engine"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	s, err := New(filepath.Join(t.TempDir(), "gidm.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func sampleDownload(id string) *engine.Download {
	now := time.Now().UTC()
	return &engine.Download{
		ID:           id,
		URL:          "https://example.com/" + id + ".iso",
		Destination:  "/tmp/" + id + ".iso",
		TotalSize:    3000,
		Status:       engine.StatusActive,
		ETag:         `"abc123"`,
		LastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
		Checksum:     "sha256:deadbeef",
		Segments: []engine.Segment{
			{Index: 0, Start: 0, End: 999, Completed: 500},
			{Index: 1, Start: 1000, End: 1999, Completed: 0},
			{Index: 2, Start: 2000, End: 2999, Completed: 1000},
		},
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
	}
}

func assertDownloadEqual(t *testing.T, got, want *engine.Download) {
	t.Helper()

	if got.ID != want.ID || got.URL != want.URL || got.Destination != want.Destination ||
		got.TotalSize != want.TotalSize || got.Status != want.Status || got.ETag != want.ETag ||
		got.LastModified != want.LastModified || got.Checksum != want.Checksum {
		t.Errorf("scalar fields mismatch:\n got=%+v\nwant=%+v", got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, want.UpdatedAt)
	}
	if len(got.Segments) != len(want.Segments) {
		t.Fatalf("len(Segments) = %d, want %d", len(got.Segments), len(want.Segments))
	}
	for i := range want.Segments {
		if got.Segments[i] != want.Segments[i] {
			t.Errorf("Segments[%d] = %+v, want %+v", i, got.Segments[i], want.Segments[i])
		}
	}
}

func TestNewRejectsEmptyPath(t *testing.T) {
	t.Parallel()

	if _, err := New(""); err == nil {
		t.Error("New(\"\") = nil error, want error")
	}
}

func TestDurabilityAcrossReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gidm.db")

	want := sampleDownload("durable")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.SaveDownload(ctx, want); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}
	if err := s.SetSetting(ctx, "k", "v"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := New(path)
	if err != nil {
		t.Fatalf("New (reopen): %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	got, err := reopened.LoadDownload(ctx, want.ID)
	if err != nil {
		t.Fatalf("LoadDownload after reopen: %v", err)
	}
	assertDownloadEqual(t, got, want)

	if v, err := reopened.GetSetting(ctx, "k"); err != nil || v != "v" {
		t.Errorf("GetSetting after reopen = %q, %v; want %q, nil", v, err, "v")
	}
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	const workers = 16
	var wg sync.WaitGroup
	errCh := make(chan error, workers*4)

	for w := range workers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			d := sampleDownload(fmt.Sprintf("dl-%d", n))
			if err := s.SaveDownload(ctx, d); err != nil {
				errCh <- fmt.Errorf("save %d: %w", n, err)
				return
			}
			for i := range d.Segments {
				d.Segments[i].Completed = d.Segments[i].Size()
				if err := s.UpdateSegment(ctx, d.ID, d.Segments[i]); err != nil {
					errCh <- fmt.Errorf("update %d/%d: %w", n, i, err)
					return
				}
			}
			if _, err := s.LoadDownload(ctx, d.ID); err != nil {
				errCh <- fmt.Errorf("load %d: %w", n, err)
				return
			}
			if _, err := s.ListDownloads(ctx); err != nil {
				errCh <- fmt.Errorf("list %d: %w", n, err)
			}
		}(w)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	got, err := s.ListDownloads(ctx)
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(got) != workers {
		t.Errorf("len = %d, want %d", len(got), workers)
	}
}
