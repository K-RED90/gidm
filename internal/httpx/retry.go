package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// do issues method+rawURL, applying setup to each attempt's request, and retries
// transient failures (network errors, 429, 5xx) up to maxRetries with jittered
// exponential backoff. It honors Retry-After and ctx cancellation. A successful
// or non-retryable response is returned with its body open for the caller to
// read and close; retries never touch a body the caller will stream.
func (c *Client) do(ctx context.Context, method, rawURL string, setup func(*http.Request)) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := c.newRequest(ctx, method, rawURL)
		if err != nil {
			return nil, err
		}
		if setup != nil {
			setup(req)
		}

		resp, err := c.hc.Do(req)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if attempt >= c.maxRetries || !retryableErr(err) {
				return nil, fmt.Errorf("httpx: %s %q: %w", method, rawURL, err)
			}
			if werr := c.wait(ctx, c.backoff(attempt)); werr != nil {
				return nil, werr
			}
			continue
		}

		if attempt < c.maxRetries && retryableStatus(resp.StatusCode) {
			delay := c.retryDelay(resp, attempt)
			drainAndClose(resp.Body)
			if werr := c.wait(ctx, delay); werr != nil {
				return nil, werr
			}
			continue
		}
		return resp, nil
	}
}

// retryableErr reports whether a transport error is worth retrying. Redirect
// policy violations are deterministic, so they are not.
func retryableErr(err error) bool {
	return !errors.Is(err, ErrTooManyRedirects) && !errors.Is(err, ErrInsecureRedirect)
}

// retryableStatus reports whether a status code signals a transient failure.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// retryDelay honors a Retry-After header when present and parseable, otherwise
// falls back to exponential backoff.
func (c *Client) retryDelay(resp *http.Response, attempt int) time.Duration {
	if d, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
		switch {
		case d < 0:
			return 0
		case d > maxRetryBackoff:
			return maxRetryBackoff
		default:
			return d
		}
	}
	return c.backoff(attempt)
}

// backoff returns the jittered exponential delay for a zero-based attempt,
// spread across [d, 2d) to avoid synchronized retry storms and capped.
func (c *Client) backoff(attempt int) time.Duration {
	base := c.retryBackoff
	if base <= 0 {
		base = time.Second
	}
	d := base
	for i := 0; i < attempt && d < maxRetryBackoff; i++ {
		d *= 2
	}
	if d > maxRetryBackoff {
		d = maxRetryBackoff
	}
	d += time.Duration(c.jitter() * float64(d))
	if d > maxRetryBackoff {
		d = maxRetryBackoff
	}
	return d
}

// parseRetryAfter parses a Retry-After value, which is either delay-seconds or an
// HTTP-date. A past date yields a negative duration the caller clamps.
func parseRetryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t), true
	}
	return 0, false
}

// wait sleeps for d, returning early if ctx is cancelled.
func (c *Client) wait(ctx context.Context, d time.Duration) error {
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

// drainAndClose discards a bounded prefix of a response body before closing, so
// the keep-alive connection can be reused for the common small error body.
func drainAndClose(body io.ReadCloser) {
	_, _ = io.CopyN(io.Discard, body, drainLimit)
	_ = body.Close()
}
