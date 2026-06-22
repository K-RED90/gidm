package engine_test

// Throughput test harness: a local TLS file server with Range support and an
// optional per-TCP-connection bandwidth cap, plus an in-memory Store and an
// engine builder. The per-connection throttle is the crux of the H1-vs-H2
// comparison: HTTP/1.1 opens one connection per segment (N throttle buckets, so
// aggregate ~= N x rate), while HTTP/2 multiplexes every segment onto a single
// connection (one bucket, so aggregate ~= rate) — modelling the per-connection
// rate limits real CDNs apply.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
	"github.com/K-RED90/gidm/internal/engine"
	"github.com/K-RED90/gidm/internal/httpfetch"
	"github.com/K-RED90/gidm/internal/httpx"
)

// makeData returns n bytes with a position-dependent pattern (cheap, deterministic).
func makeData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// connLimiter is a token bucket shared by every request on one TCP connection.
// The lock is held across the sleep so concurrent HTTP/2 streams on the same
// connection share a single rate, matching a per-connection server cap.
type connLimiter struct {
	mu     sync.Mutex
	rate   float64 // bytes/sec; <= 0 disables throttling
	tokens float64
	last   time.Time
}

func (l *connLimiter) take(n int) {
	if l == nil || l.rate <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.tokens += now.Sub(l.last).Seconds() * l.rate
	if l.tokens > l.rate { // cap the burst at ~1s of bandwidth
		l.tokens = l.rate
	}
	l.last = now
	if l.tokens >= float64(n) {
		l.tokens -= float64(n)
		return
	}
	deficit := float64(n) - l.tokens
	l.tokens = 0
	time.Sleep(time.Duration(deficit / l.rate * float64(time.Second)))
}

type limiterKey struct{}

func writeThrottled(w io.Writer, data []byte, l *connLimiter) {
	const chunk = 32 * 1024
	for len(data) > 0 {
		n := min(chunk, len(data))
		l.take(n)
		if _, err := w.Write(data[:n]); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		data = data[n:]
	}
}

// parseRange handles the single-range forms the engine emits: "bytes=start-end"
// (segment fetch) and "bytes=start-" (open-ended). Multi-range is unsupported.
func parseRange(h string, size int64) (start, end int64, ok bool) {
	if !strings.HasPrefix(h, "bytes=") {
		return 0, 0, false
	}
	spec := strings.TrimPrefix(h, "bytes=")
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(spec[:dash]), 10, 64)
	if err != nil {
		return 0, 0, false
	}
	if e := strings.TrimSpace(spec[dash+1:]); e == "" {
		end = size - 1
	} else if end, err = strconv.ParseInt(e, 10, 64); err != nil {
		return 0, 0, false
	}
	if start < 0 || end >= size || start > end {
		return 0, 0, false
	}
	return start, end, true
}

// newRangeServer starts a TLS server that serves data with Range support, an
// ETag validator, and an optional per-connection rate cap. http2 toggles ALPN h2.
func newRangeServer(tb testing.TB, data []byte, etag string, ratePerConn float64, http2 bool) *httptest.Server {
	handler := func(w http.ResponseWriter, r *http.Request) {
		var lim *connLimiter
		if v := r.Context().Value(limiterKey{}); v != nil {
			lim = v.(*connLimiter)
		}
		w.Header().Set("Accept-Ranges", "bytes")
		if etag != "" {
			w.Header().Set("ETag", etag)
		}
		rng := r.Header.Get("Range")
		if rng == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusOK)
			writeThrottled(w, data, lim)
			return
		}
		start, end, ok := parseRange(rng, int64(len(data)))
		if !ok {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		writeThrottled(w, data[start:end+1], lim)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(handler))
	srv.EnableHTTP2 = http2
	// One limiter per TCP connection, shared by all requests (H2 streams) on it.
	srv.Config.ConnContext = func(ctx context.Context, _ net.Conn) context.Context {
		if ratePerConn <= 0 {
			return ctx
		}
		return context.WithValue(ctx, limiterKey{}, &connLimiter{rate: ratePerConn, last: time.Now()})
	}
	srv.StartTLS()
	tb.Cleanup(srv.Close)
	return srv
}

// newEngine builds an engine wired to a real httpx transport (so the HTTP2 flag
// is exercised end to end) and an in-memory store, downloading into a temp dir
// it returns for the caller to clean between benchmark iterations. stallTimeout
// sets the per-segment stall watchdog (0 disables it).
func newEngine(tb testing.TB, segments int, http2 bool, stallTimeout time.Duration) (*engine.Engine, string) {
	netCfg := config.Network{
		UserAgent:        "gidm-bench",
		TLSSkipVerify:    true,
		HTTP2:            http2,
		SocketBufferSize: 128 * 1024,
	}
	dlCfg := config.Download{
		MaxConcurrent:       4,
		SegmentsPerDownload: segments,
		BufferSize:          64 * 1024,
		Timeout:             config.Duration(30 * time.Second),
		MaxRetries:          5,
		RetryBackoff:        config.Duration(20 * time.Millisecond),
		WorkStealing:        false, // fixed segment count for a clean N-connection comparison
		MaxSegments:         segments,
		MinStealSize:        1 << 20,
		StallTimeout:        config.Duration(stallTimeout),
		DefaultPriority:     "normal",
	}
	client, err := httpx.New(netCfg, dlCfg)
	if err != nil {
		tb.Fatalf("httpx.New: %v", err)
	}
	dir := tb.TempDir()
	cfg := config.Config{Download: dlCfg, Network: netCfg, Paths: config.Paths{DownloadDir: dir}}
	return engine.New(cfg, httpfetch.New(client), newMemStore()), dir
}

// memStore is a minimal in-memory engine.Store for tests/benchmarks.
type memStore struct {
	mu        sync.Mutex
	downloads map[string]*engine.Download
	settings  map[string]string
}

func newMemStore() *memStore {
	return &memStore{downloads: map[string]*engine.Download{}, settings: map[string]string{}}
}

func cloneDownload(d *engine.Download) *engine.Download {
	cp := *d
	cp.Segments = append([]engine.Segment(nil), d.Segments...)
	return &cp
}

func (s *memStore) SaveDownload(_ context.Context, d *engine.Download) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.downloads[d.ID] = cloneDownload(d)
	return nil
}

func (s *memStore) LoadDownload(_ context.Context, id string) (*engine.Download, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.downloads[id]
	if !ok {
		return nil, engine.ErrNotFound
	}
	return cloneDownload(d), nil
}

func (s *memStore) ListDownloads(_ context.Context) ([]*engine.Download, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*engine.Download, 0, len(s.downloads))
	for _, d := range s.downloads {
		out = append(out, cloneDownload(d))
	}
	return out, nil
}

func (s *memStore) DeleteDownload(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.downloads, id)
	return nil
}

func (s *memStore) UpdateSegment(_ context.Context, id string, seg engine.Segment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.downloads[id]
	if !ok {
		return engine.ErrNotFound
	}
	for i := range d.Segments {
		if d.Segments[i].Index == seg.Index {
			d.Segments[i] = seg
			return nil
		}
	}
	d.Segments = append(d.Segments, seg)
	return nil
}

func (s *memStore) GetSetting(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.settings[key]
	if !ok {
		return "", engine.ErrNotFound
	}
	return v, nil
}

func (s *memStore) SetSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[key] = value
	return nil
}

func (s *memStore) Close() error { return nil }
