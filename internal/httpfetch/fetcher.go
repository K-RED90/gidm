package httpfetch

import (
	"context"
	"io"

	"github.com/K-RED90/gidm/internal/engine"
	"github.com/K-RED90/gidm/internal/httpx"
)

// Fetcher binds a pooled *httpx.Client to engine.Fetcher, translating the HTTP
// layer's types into engine types so the engine never imports httpx.
type Fetcher struct {
	c *httpx.Client
}

var _ engine.Fetcher = (*Fetcher)(nil)

// New wraps an httpx.Client as an engine.Fetcher. The client is shared across
// every segment, so connections pool and keep-alive holds.
func New(c *httpx.Client) *Fetcher {
	return &Fetcher{c: c}
}

// Probe maps an httpx.ProbeResult onto engine.ProbeInfo.
func (f *Fetcher) Probe(ctx context.Context, url string) (engine.ProbeInfo, error) {
	pr, err := f.c.Probe(ctx, url)
	if err != nil {
		return engine.ProbeInfo{}, err
	}
	return engine.ProbeInfo{
		FinalURL:       pr.URL,
		Size:           pr.Size,
		SupportsRanges: pr.SupportsRanges,
		ETag:           pr.ETag,
		LastModified:   pr.LastModified,
		Filename:       pr.Filename,
	}, nil
}

// RangeGet streams one segment's bytes, returning the response body for the
// engine to copy and close.
func (f *Fetcher) RangeGet(ctx context.Context, url string, start, end int64) (io.ReadCloser, error) {
	resp, err := f.c.RangeGet(ctx, url, start, end)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Get streams the full body for the single-segment fallback.
func (f *Fetcher) Get(ctx context.Context, url string) (io.ReadCloser, error) {
	resp, err := f.c.Get(ctx, url)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}
