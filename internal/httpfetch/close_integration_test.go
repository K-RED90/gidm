package httpfetch_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
	"github.com/K-RED90/gidm/internal/engine"
	"github.com/K-RED90/gidm/internal/httpfetch"
	"github.com/K-RED90/gidm/internal/httpx"
	"github.com/K-RED90/gidm/internal/store/sqlite"
)

// wireWithStore is wire's sibling that also hands back the store so a test can
// drive Close itself. Work-stealing is on so the steal-persist path runs.
func wireWithStore(t *testing.T, srv *httptest.Server) (*engine.Engine, *sqlite.Store) {
	t.Helper()
	dl := dlConfig()
	dl.WorkStealing = true

	client, err := httpx.New(config.Network{}, dl, httpx.WithRoundTripper(srv.Client().Transport))
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	store, err := sqlite.New(filepath.Join(t.TempDir(), "gidm.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	cfg := config.Config{Download: dl, Paths: config.Paths{DownloadDir: t.TempDir()}}
	return engine.New(cfg, httpfetch.New(client), store), store
}

// closeWithin runs store.Close on a deadline; the whole point of the fix is that
// it can never block forever, so a hang here is the bug.
func closeWithin(t *testing.T, store *sqlite.Store, d time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- store.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("store.Close: %v", err)
		}
	case <-time.After(d):
		t.Fatal("store.Close() hung")
	}
}

func rangeServer(t *testing.T) *httptest.Server {
	t.Helper()
	content := bytes.Repeat([]byte("gidm-close-"), 600000) // ~6.6 MiB, range-served
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "big.bin", time.Time{}, bytes.NewReader(content))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestStoreCloseAfterCompletedDownload is the reported scenario end to end: after
// a download finishes through the real sqlite store, Close must return.
func TestStoreCloseAfterCompletedDownload(t *testing.T) {
	srv := rangeServer(t)
	e, store := wireWithStore(t, srv)

	dl, err := e.Download(context.Background(), srv.URL+"/big.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if dl.Status != engine.StatusCompleted {
		t.Fatalf("status = %q, want completed", dl.Status)
	}

	closeWithin(t, store, 10*time.Second)
}

// TestStoreCloseAfterCanceledDownload cancels the run while checkpoint and
// steal-persist writes may be in flight — the race the fix targets — then
// requires Close to return regardless of the download's outcome.
func TestStoreCloseAfterCanceledDownload(t *testing.T) {
	srv := rangeServer(t)
	e, store := wireWithStore(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(time.Millisecond) // let some segments start before pulling the rug
		cancel()
	}()
	_, _ = e.Download(ctx, srv.URL+"/big.bin") // outcome irrelevant; Close must not hang

	closeWithin(t, store, 10*time.Second)
}
