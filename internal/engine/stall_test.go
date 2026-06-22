package engine_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// newStallOnceServer serves data with Range support but makes the FIRST real
// segment request (the multi-byte one, not the probe's single byte) hang after a
// short prefix until the client drops it — simulating a peer that vanishes with
// no TCP RST. Every later request serves normally, so a working stall watchdog
// recovers by reconnecting and resuming from the checkpointed offset.
func newStallOnceServer(tb testing.TB, data []byte) *httptest.Server {
	var stalledOnce atomic.Bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"v1"`)
		start, end, ok := parseRange(r.Header.Get("Range"), int64(len(data)))
		if !ok {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		body := data[start : end+1]

		if len(body) > 1 && stalledOnce.CompareAndSwap(false, true) {
			n := min(64*1024, len(body)) // a prefix so progress advances once, then stalls
			_, _ = w.Write(body[:n])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done() // hang until the client's watchdog cancels the request
			return
		}
		_, _ = w.Write(body)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(handler))
	srv.StartTLS()
	tb.Cleanup(srv.Close)
	return srv
}

// TestStallWatchdogReconnectsAndCompletes proves the watchdog turns a dead-but-
// open connection into a quick reconnect: without it, the first segment's body
// read would block until TCP keep-alive notices (minutes) and the outer timeout
// would fire instead.
func TestStallWatchdogReconnectsAndCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("stall recovery waits out a watchdog timeout; skipped in -short")
	}
	const size = 2 << 20
	data := makeData(size)
	srv := newStallOnceServer(t, data)

	// One segment, so the connection that stalls is the only one; a short stall
	// timeout keeps the test quick.
	eng, _ := newEngine(t, 1, false, 400*time.Millisecond)

	done := make(chan error, 1)
	go func() {
		dl, err := eng.Download(context.Background(), srv.URL)
		if err != nil {
			done <- err
			return
		}
		if fi, statErr := os.Stat(dl.Destination); statErr != nil || fi.Size() != int64(size) {
			done <- fmt.Errorf("bad output: size=%v err=%v", fi, statErr)
			return
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("download did not recover from stall: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("download hung: stall watchdog did not drop the dead connection")
	}
}
