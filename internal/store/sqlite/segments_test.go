package sqlite

import (
	"context"
	"testing"
)

func TestUpdateSegmentPersists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	d := sampleDownload("seg")
	if err := s.SaveDownload(ctx, d); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	d.Segments[1].Completed = 750
	if err := s.UpdateSegment(ctx, d.ID, d.Segments[1]); err != nil {
		t.Fatalf("UpdateSegment: %v", err)
	}

	got, err := s.LoadDownload(ctx, d.ID)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if got.Segments[1].Completed != 750 {
		t.Errorf("Segments[1].Completed = %d, want 750", got.Segments[1].Completed)
	}
}
