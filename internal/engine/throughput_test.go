package engine_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// BenchmarkDownloadThroughput measures end-to-end download throughput (MB/s via
// b.SetBytes) and allocations for a range of segment counts over an unthrottled
// loopback server — the baseline for the engine's own overhead and for proving
// segmentation scales. Run: go test -run=^$ -bench=DownloadThroughput -benchmem ./internal/engine
func BenchmarkDownloadThroughput(b *testing.B) {
	const size = 8 << 20
	data := makeData(size)
	srv := newRangeServer(b, data, `"v1"`, 0, false) // unthrottled, HTTP/1.1
	ctx := context.Background()

	for _, segs := range []int{1, 4, 8, 16} {
		b.Run(fmt.Sprintf("segments=%d", segs), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				eng, dir := newEngine(b, segs, false, 0)
				b.StartTimer()

				if _, err := eng.Download(ctx, srv.URL); err != nil {
					b.Fatalf("download: %v", err)
				}

				b.StopTimer()
				_ = os.RemoveAll(dir)
				b.StartTimer()
			}
		})
	}
}

// TestHTTP1BeatsHTTP2UnderPerConnThrottle is the headline claim, as a regression
// guard: under a per-connection bandwidth cap (what real CDNs apply), forcing
// HTTP/1.1 fans a download across N connections (aggregate ~= N x rate), while
// HTTP/2 multiplexes every segment onto one connection (aggregate ~= rate). H1
// must therefore be far faster — the reason gidm defaults ForceAttemptHTTP2 off.
func TestHTTP1BeatsHTTP2UnderPerConnThrottle(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput comparison streams a throttled file; skipped in -short")
	}
	const (
		size        = 8 << 20
		ratePerConn = 4 << 20 // 4 MiB/s per TCP connection
		segments    = 8
	)
	data := makeData(size)

	run := func(http2 bool) time.Duration {
		srv := newRangeServer(t, data, `"v1"`, ratePerConn, http2)
		eng, _ := newEngine(t, segments, http2, 0)
		start := time.Now()
		dl, err := eng.Download(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("download (http2=%v): %v", http2, err)
		}
		if fi, err := os.Stat(dl.Destination); err != nil || fi.Size() != int64(size) {
			t.Fatalf("download (http2=%v): wrong output: size=%v err=%v", http2, fi, err)
		}
		return time.Since(start)
	}

	h1 := run(false)
	h2 := run(true)
	t.Logf("%d segments, %d MiB, %d MiB/s/connection: HTTP/1.1=%v  HTTP/2=%v  (H1 %.1fx faster)",
		segments, size>>20, ratePerConn>>20, h1.Round(time.Millisecond), h2.Round(time.Millisecond),
		float64(h2)/float64(h1))

	if h1*2 >= h2 {
		t.Errorf("expected HTTP/1.1 to be far faster than HTTP/2 under a per-connection throttle; got H1=%v H2=%v", h1, h2)
	}
}
