package sqlite

import (
	"errors"
	"testing"
	"time"
)

func TestCloseWithDeadlineReturnsError(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	err := closeWithDeadline(func() error { return boom }, time.Second)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped %v", err, boom)
	}
}

func TestCloseWithDeadlineSucceeds(t *testing.T) {
	t.Parallel()

	if err := closeWithDeadline(func() error { return nil }, time.Second); err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}
}

// TestCloseWithDeadlineTimesOut is the regression guard for the reported hang: a
// close that never returns must not hold the caller forever.
func TestCloseWithDeadlineTimesOut(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	t.Cleanup(func() { close(block) }) // release the goroutine after the test

	done := make(chan error, 1)
	go func() {
		done <- closeWithDeadline(func() error { <-block; return nil }, 50*time.Millisecond)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Close = nil, want timeout error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closeWithDeadline did not return despite its own deadline")
	}
}

// TestStoreCloseRealDB exercises the real bounded Close against a live database.
func TestStoreCloseRealDB(t *testing.T) {
	t.Parallel()

	s, err := New(t.TempDir() + "/gidm.db")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
