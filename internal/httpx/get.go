package httpx

import (
	"context"
	"fmt"
	"net/http"
)

// Get issues a full-body GET with no Range header, for the single-segment
// fallback the engine takes when a server's size is unknown or it advertises no
// range support. It accepts 200 (and tolerates an unsolicited 206) and rejects
// other statuses after draining and closing the body. The returned response
// streams the body, which the caller must close; the body is not read here.
//
// Like RangeGet it reuses the pooled transport via do, so keep-alive holds and
// only ctx governs the streaming read (no overall client timeout).
func (c *Client) Get(ctx context.Context, rawURL string) (*http.Response, error) {
	resp, err := c.do(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		drainAndClose(resp.Body)
		return nil, fmt.Errorf("httpx: get %q: unexpected status %s", rawURL, resp.Status)
	}
	return resp, nil
}
