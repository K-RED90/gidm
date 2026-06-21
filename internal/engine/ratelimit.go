package engine

import (
	"context"
	"sync"
	"time"
)

// clock abstracts time so the limiter is deterministically testable without wall
// sleeps. now reads the current instant; sleep blocks for d or until ctx is done,
// returning ctx.Err() on cancel. Production uses realClock; tests inject a virtual
// clock whose sleep advances now by d and returns at once, so a test asserts exact
// throttle durations with no real waiting.
type clock interface {
	now() time.Time
	sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) now() time.Time { return time.Now() }

// sleep is the production wait: a single timer, allocated only when we actually
// throttle (d > 0). It mirrors sleepCtx, honoring ctx and returning ctx.Err() on
// cancel so a paused/cancelled transfer aborts promptly instead of holding tokens.
func (realClock) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// rateLimiter is an in-house token bucket: tokens accrue at rate bytes/sec up to
// burst, and WaitN blocks until n tokens are available, then consumes them. It is
// safe for concurrent callers — one global bucket is shared by every segment of
// every download — and the only shared state is the mutex.
//
// A nil *rateLimiter means "unlimited": callers MUST nil-check before calling, so
// the uncapped path takes no lock and allocates nothing (the zero-alloc hot path).
// The throttle hook lives at the per-flush write boundary, never the per-byte loop.
type rateLimiter struct {
	mu     sync.Mutex
	rate   float64   // tokens (bytes) per second; > 0 (a 0-rate limiter is never built)
	burst  float64   // bucket capacity; MUST be >= the largest WaitN argument (see rateBurst)
	tokens float64   // current tokens, in [0, burst]
	last   time.Time // last refill instant, read from clk.now()
	clk    clock
}

func newRateLimiter(rate, burst int64) *rateLimiter {
	return newRateLimiterClock(rate, burst, realClock{})
}

func newRateLimiterClock(rate, burst int64, clk clock) *rateLimiter {
	return &rateLimiter{
		rate:   float64(rate),
		burst:  float64(burst),
		tokens: float64(burst), // start full: the first flush is never throttled
		last:   clk.now(),
		clk:    clk,
	}
}

// WaitN blocks until n tokens are available, consumes them, and returns nil. On ctx
// cancellation it returns ctx.Err() without consuming tokens. n is clamped to burst
// defensively, but the burst invariant (rateBurst) already guarantees n <= burst, so
// WaitN can always eventually succeed and never deadlocks.
//
// Fast path (tokens already available): one mutex lock, float math, unlock, return —
// zero allocations, which the limited progressWriter.Write benchmark proves. The only
// allocation (a timer in clk.sleep) happens solely when we must actually throttle.
func (l *rateLimiter) WaitN(ctx context.Context, n int) error {
	if n <= 0 {
		return nil
	}
	need := float64(n)
	if need > l.burst {
		need = l.burst // unreachable under the rateBurst invariant; belt and suspenders
	}
	for {
		l.mu.Lock()
		now := l.clk.now()
		if elapsed := now.Sub(l.last); elapsed > 0 {
			l.tokens += elapsed.Seconds() * l.rate
			if l.tokens > l.burst {
				l.tokens = l.burst
			}
			l.last = now
		}
		if l.tokens >= need {
			l.tokens -= need
			l.mu.Unlock()
			return nil
		}
		// Release the lock before sleeping so other callers refill/consume meanwhile,
		// then re-check after waking. Consumption only happens above under the lock
		// when tokens >= need, so a stale wait estimate costs at most an extra loop and
		// never an over-grant. Strict per-segment fairness is not promised, only the
		// aggregate cap.
		wait := time.Duration((need - l.tokens) / l.rate * float64(time.Second))
		l.mu.Unlock()
		if wait <= 0 {
			wait = time.Microsecond // guard a zero-duration spin when the deficit rounds out
		}
		if err := l.clk.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// rateBurst derives a bucket capacity for a byte/sec rate. The floor of 2*bufSize
// enforces the deadlock invariant burst >= maxFlushSize (a flush is at most one
// BufferSize): a smaller burst could never admit one buffer, stalling the worker
// forever. The default of rate/10 (~100 ms of bandwidth) smooths bursty flush
// cadence without letting the average exceed rate. A positive cfgBurst overrides the
// default but is still floored.
func rateBurst(rate int64, bufSize int, cfgBurst int64) int64 {
	burst := cfgBurst
	if burst <= 0 {
		burst = rate / 10
	}
	if floor := int64(2 * bufSize); burst < floor {
		burst = floor
	}
	return burst
}
