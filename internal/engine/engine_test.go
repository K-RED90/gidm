package engine

import (
	"context"
	"testing"

	"github.com/K-RED90/gidm/internal/config"
)

type stubStore struct{}

func (stubStore) SaveDownload(context.Context, *Download) error           { return nil }
func (stubStore) LoadDownload(context.Context, string) (*Download, error) { return nil, ErrNotFound }
func (stubStore) ListDownloads(context.Context) ([]*Download, error)      { return nil, nil }
func (stubStore) DeleteDownload(context.Context, string) error            { return nil }
func (stubStore) UpdateSegment(context.Context, string, Segment) error    { return nil }
func (stubStore) GetSetting(context.Context, string) (string, error)      { return "", ErrNotFound }
func (stubStore) SetSetting(context.Context, string, string) error        { return nil }
func (stubStore) Close() error                                            { return nil }

var _ Store = stubStore{}

func TestEngineReady(t *testing.T) {
	if New(config.Download{MaxConcurrent: 4}, stubStore{}).Ready() != true {
		t.Error("Ready() = false, want true for a wired engine")
	}
	if New(config.Download{MaxConcurrent: 4}, nil).Ready() != false {
		t.Error("Ready() = true, want false when store is nil")
	}
}

func TestSegmentArithmetic(t *testing.T) {
	s := Segment{Start: 0, End: 99, Completed: 100}
	if s.Size() != 100 {
		t.Errorf("Size() = %d, want 100", s.Size())
	}
	if !s.Done() {
		t.Error("Done() = false, want true")
	}
	if s.Remaining() != 0 {
		t.Errorf("Remaining() = %d, want 0", s.Remaining())
	}
}
