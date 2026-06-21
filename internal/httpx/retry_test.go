package httpx

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
)

func TestRetrySucceedsAfterTransientFailures(t *testing.T) {
	var calls int32
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if atomic.AddInt32(&calls, 1) < 3 {
			return respFor(r, http.StatusServiceUnavailable, nil, "busy"), nil
		}
		return respFor(r, http.StatusOK, nil, "ok"), nil
	})
	c := newTestClient(t, config.Network{}, testDownload(5, time.Millisecond), WithRoundTripper(rt))

	resp, err := c.do(context.Background(), http.MethodGet, "http://x/", nil)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestRetryExhaustsBudget(t *testing.T) {
	var calls int32
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return respFor(r, http.StatusBadGateway, nil, "down"), nil
	})
	c := newTestClient(t, config.Network{}, testDownload(2, time.Millisecond), WithRoundTripper(rt))

	resp, err := c.do(context.Background(), http.MethodGet, "http://x/", nil)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer closeBody(t, resp)
	// After the budget is spent, do returns the last retryable response for the
	// caller to interpret — Probe/RangeGet turn it into an error.
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3 (1 + 2 retries)", got)
	}
}

func TestRetryFailsFastOn4xx(t *testing.T) {
	var calls int32
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return respFor(r, http.StatusNotFound, nil, "nope"), nil
	})
	c := newTestClient(t, config.Network{}, testDownload(5, time.Millisecond), WithRoundTripper(rt))

	resp, err := c.do(context.Background(), http.MethodGet, "http://x/", nil)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer closeBody(t, resp)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 404)", got)
	}
}

func TestRetryOnNetworkErrorThenExhausts(t *testing.T) {
	var calls int32
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("connection reset")
	})
	c := newTestClient(t, config.Network{}, testDownload(2, time.Millisecond), WithRoundTripper(rt))

	if _, err := c.do(context.Background(), http.MethodGet, "http://x/", nil); err == nil {
		t.Fatal("do: expected error after exhausting retries on network error")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestRetryRespectsContextCancellation(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return respFor(r, http.StatusServiceUnavailable, nil, "busy"), nil
	})
	// Long backoff so the test can only pass by honoring cancellation, not waiting.
	c := newTestClient(t, config.Network{}, testDownload(5, time.Second), WithRoundTripper(rt))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := c.do(ctx, http.MethodGet, "http://x/", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("returned after %s, want prompt cancellation", elapsed)
	}
}

func TestRetryHonorsRetryAfterSeconds(t *testing.T) {
	var calls int32
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			h := http.Header{"Retry-After": {"0"}}
			return respFor(r, http.StatusTooManyRequests, h, "slow down"), nil
		}
		return respFor(r, http.StatusOK, nil, "ok"), nil
	})
	// Huge base backoff: the test can only finish quickly if Retry-After: 0 wins.
	c := newTestClient(t, config.Network{}, testDownload(3, time.Hour), WithRoundTripper(rt))

	start := time.Now()
	resp, err := c.do(context.Background(), http.MethodGet, "http://x/", nil)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s; Retry-After: 0 should override the backoff", elapsed)
	}
}

func TestRetryHonorsRetryAfterHTTPDate(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	var calls int32
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			h := http.Header{"Retry-After": {past}}
			return respFor(r, http.StatusServiceUnavailable, h, "busy"), nil
		}
		return respFor(r, http.StatusOK, nil, "ok"), nil
	})
	// A past HTTP-date clamps to 0, so a huge backoff base must not be used.
	c := newTestClient(t, config.Network{}, testDownload(3, time.Hour), WithRoundTripper(rt))

	start := time.Now()
	resp, err := c.do(context.Background(), http.MethodGet, "http://x/", nil)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer closeBody(t, resp)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s; a past Retry-After date should clamp to 0", elapsed)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d, ok := parseRetryAfter("5"); !ok || d != 5*time.Second {
		t.Errorf("parseRetryAfter(\"5\") = %v, %v; want 5s, true", d, ok)
	}
	if _, ok := parseRetryAfter(""); ok {
		t.Error("parseRetryAfter(\"\") should report not-ok")
	}
	if _, ok := parseRetryAfter("not-a-date"); ok {
		t.Error("parseRetryAfter of garbage should report not-ok")
	}
}
