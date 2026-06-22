package httpx

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// ProbeResult is the metadata gidm needs before planning a download.
type ProbeResult struct {
	URL            string // final URL after redirects
	Size           int64  // total bytes, or -1 if the server did not disclose it
	SupportsRanges bool
	ETag           string
	LastModified   string
	Filename       string // sanitized Content-Disposition name, or "" if none
}

// Probe issues a one-byte ranged GET to learn a URL's size, range support, and
// validators without downloading the body. A 206 confirms ranges and carries the
// total in Content-Range; a 200 means the server ignored the range, so size comes
// from Content-Length and range support from Accept-Ranges.
func (c *Client) Probe(ctx context.Context, rawURL string, opts RequestOptions) (*ProbeResult, error) {
	resp, err := c.do(ctx, http.MethodGet, rawURL, func(req *http.Request) {
		opts.apply(req)
		req.Header.Set("Range", "bytes=0-0")
	})
	if err != nil {
		return nil, err
	}
	defer drainAndClose(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("httpx: probe %q: unexpected status %s", rawURL, resp.Status)
	}

	out := &ProbeResult{
		URL:          resp.Request.URL.String(),
		Size:         -1,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		Filename:     filenameFromResponse(resp),
	}
	if resp.StatusCode == http.StatusPartialContent {
		out.SupportsRanges = true
		out.Size = totalFromContentRange(resp.Header.Get("Content-Range"))
	} else {
		out.SupportsRanges = acceptsRanges(resp.Header.Get("Accept-Ranges"))
		if resp.ContentLength >= 0 {
			out.Size = resp.ContentLength
		}
	}
	return out, nil
}

// totalFromContentRange extracts TOTAL from "bytes START-END/TOTAL". An unknown
// ("*") total or any parse failure yields -1.
func totalFromContentRange(v string) int64 {
	i := strings.LastIndexByte(v, '/')
	if i < 0 {
		return -1
	}
	total := strings.TrimSpace(v[i+1:])
	if total == "" || total == "*" {
		return -1
	}
	n, err := strconv.ParseInt(total, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// acceptsRanges reports whether an Accept-Ranges header advertises byte ranges
// (as opposed to "none" or absence).
func acceptsRanges(v string) bool {
	return strings.Contains(strings.ToLower(v), "bytes")
}

// filenameFromResponse extracts and sanitizes a server-suggested filename from
// Content-Disposition, preferring the RFC 5987 filename* form.
func filenameFromResponse(resp *http.Response) string {
	cd := resp.Header.Get("Content-Disposition")
	if cd == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		return ""
	}
	name := params["filename*"]
	if name == "" {
		name = params["filename"]
	}
	return SanitizeFilename(name)
}
