package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/K-RED90/gidm/internal/engine"
)

func TestSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.GetSetting(ctx, "max_conns"); !errors.Is(err, engine.ErrNotFound) {
		t.Errorf("GetSetting(missing) = %v, want ErrNotFound", err)
	}

	if err := s.SetSetting(ctx, "max_conns", "8"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	got, err := s.GetSetting(ctx, "max_conns")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != "8" {
		t.Errorf("GetSetting = %q, want %q", got, "8")
	}

	// SetSetting upserts.
	if err := s.SetSetting(ctx, "max_conns", "16"); err != nil {
		t.Fatalf("SetSetting (update): %v", err)
	}
	if got, _ := s.GetSetting(ctx, "max_conns"); got != "16" {
		t.Errorf("GetSetting after update = %q, want %q", got, "16")
	}
}
