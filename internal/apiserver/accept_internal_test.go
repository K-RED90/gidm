package apiserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// scriptedListener replays a fixed sequence of Accept outcomes, then reports the
// listener as closed. acceptLoop calls Accept serially, so no locking is needed.
type scriptedListener struct {
	errs []error
	i    int
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	if l.i >= len(l.errs) {
		return nil, net.ErrClosed
	}
	err := l.errs[l.i]
	l.i++
	return nil, err
}

func (l *scriptedListener) Close() error   { return nil }
func (l *scriptedListener) Addr() net.Addr { return nil }

// TestAcceptLoopBacksOffOnError proves a transient Accept error is retried with a
// real delay (never a hot spin) and that a closed listener ends the loop cleanly.
// Two transient errors must cost at least baseDelay + 2*baseDelay = 15ms.
func TestAcceptLoopBacksOffOnError(t *testing.T) {
	s := &Server{logger: discardLogger()}
	l := &scriptedListener{errs: []error{errors.New("temporary"), errors.New("temporary")}}

	start := time.Now()
	if err := s.acceptLoop(context.Background(), l); err != nil {
		t.Fatalf("acceptLoop = %v, want nil after the listener closes", err)
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Errorf("acceptLoop returned in %v; want >=15ms of backoff (a hot spin would return near-instantly)", elapsed)
	}
}

// TestAcceptLoopCancelDuringBackoff proves a shutdown during the backoff sleep
// returns promptly rather than waiting out the delay.
func TestAcceptLoopCancelDuringBackoff(t *testing.T) {
	s := &Server{logger: discardLogger()}
	// Never-closing source of transient errors, so the loop is in backoff when the
	// context is cancelled.
	l := &scriptedListener{errs: make([]error, 0)}
	for range 1000 {
		l.errs = append(l.errs, errors.New("temporary"))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the first backoff select must take the ctx branch

	done := make(chan error, 1)
	go func() { done <- s.acceptLoop(ctx, l) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("acceptLoop = %v, want nil on context cancel", err)
		}
	case <-time.After(time.Second):
		t.Fatal("acceptLoop did not return promptly after context cancel during backoff")
	}
}

func TestNextAcceptDelay(t *testing.T) {
	cases := []struct {
		in, want time.Duration
	}{
		{0, 5 * time.Millisecond},
		{5 * time.Millisecond, 10 * time.Millisecond},
		{500 * time.Millisecond, time.Second},
		{time.Second, time.Second}, // capped
	}
	for _, c := range cases {
		if got := nextAcceptDelay(c.in); got != c.want {
			t.Errorf("nextAcceptDelay(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
