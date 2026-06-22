package engine

import (
	"context"
	"io"
)

// ProbeInfo is the engine's view of a remote resource, returned by Fetcher.Probe.
// It mirrors what the HTTP layer learns from a probe but uses only engine types,
// so the engine never imports the HTTP adapter.
type ProbeInfo struct {
	FinalURL       string // URL after redirects; subsequent requests use it
	Size           int64  // total bytes, or -1 if the server did not disclose it
	SupportsRanges bool
	ETag           string
	LastModified   string
	Filename       string // server-suggested name (already sanitized), or ""
}

// Fetcher is the engine-owned HTTP port. An adapter (internal/httpfetch) binds a
// concrete HTTP client to it, keeping the engine a pure library: the engine
// depends on this interface, never on the HTTP implementation.
//
// Returned bodies stream the response; the caller closes them and must not
// buffer them into memory.
// Every method takes the download's RequestOptions so the adapter can attach
// per-download credentials/headers (Basic auth, Referer, Cookie). The zero value
// adds nothing, so an unauthenticated download is unchanged.
type Fetcher interface {
	// Probe reports size, range support, validators, and a suggested filename
	// without downloading the body.
	Probe(ctx context.Context, url string, opts RequestOptions) (ProbeInfo, error)

	// RangeGet streams bytes [start, end] (inclusive) for one segment.
	RangeGet(ctx context.Context, url string, start, end int64, opts RequestOptions) (io.ReadCloser, error)

	// Get streams the full body. The engine uses it only for the single-segment
	// fallback (unknown size or no range support), reading sequentially from
	// offset 0 until io.EOF.
	Get(ctx context.Context, url string, opts RequestOptions) (io.ReadCloser, error)
}
