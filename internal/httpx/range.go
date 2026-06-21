package httpx

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// RangeGet fetches bytes [start, end] (inclusive) via a ranged GET. It expects a
// 206 whose Content-Range matches the request; a 200 means the server ignored the
// range and is rejected, since a full body would corrupt a segmented write. The
// returned response streams the body, which the caller must close; the body is
// not read here.
func (c *Client) RangeGet(ctx context.Context, rawURL string, start, end int64) (*http.Response, error) {
	if start < 0 || end < start {
		return nil, fmt.Errorf("httpx: invalid range [%d, %d]", start, end)
	}
	spec := fmt.Sprintf("bytes=%d-%d", start, end)
	resp, err := c.do(ctx, http.MethodGet, rawURL, func(req *http.Request) {
		req.Header.Set("Range", spec)
	})
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusPartialContent {
		drainAndClose(resp.Body)
		if resp.StatusCode == http.StatusOK {
			return nil, fmt.Errorf("httpx: range %s of %q: %w", spec, rawURL, ErrRangeNotSatisfied)
		}
		return nil, fmt.Errorf("httpx: range %s of %q: unexpected status %s", spec, rawURL, resp.Status)
	}
	if err := validateContentRange(resp.Header.Get("Content-Range"), start); err != nil {
		drainAndClose(resp.Body)
		return nil, fmt.Errorf("httpx: range %s of %q: %w", spec, rawURL, err)
	}
	return resp, nil
}

// validateContentRange confirms a 206 begins at the requested start byte, parsing
// the START from "bytes START-END/TOTAL".
func validateContentRange(v string, wantStart int64) error {
	const prefix = "bytes "
	s := strings.TrimSpace(v)
	if !strings.HasPrefix(s, prefix) {
		return fmt.Errorf("malformed content-range %q", v)
	}
	s = s[len(prefix):]
	dash := strings.IndexByte(s, '-')
	if dash < 0 {
		return fmt.Errorf("malformed content-range %q", v)
	}
	gotStart, err := strconv.ParseInt(strings.TrimSpace(s[:dash]), 10, 64)
	if err != nil {
		return fmt.Errorf("malformed content-range %q", v)
	}
	if gotStart != wantStart {
		return fmt.Errorf("content-range start %d != requested %d", gotStart, wantStart)
	}
	return nil
}
