package httpfetch_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
	"github.com/K-RED90/gidm/internal/engine"
	"github.com/K-RED90/gidm/internal/httpfetch"
	"github.com/K-RED90/gidm/internal/httpx"
	"github.com/K-RED90/gidm/internal/store/sqlite"
)

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func dlConfig() config.Download {
	return config.Download{
		MaxConcurrent:       4,
		SegmentsPerDownload: 4,
		BufferSize:          8 << 10,
		Timeout:             config.Duration(5 * time.Second),
		MaxRetries:          3,
		RetryBackoff:        config.Duration(time.Millisecond),
	}
}

// wire builds a real httpx.Client routed at srv via WithRoundTripper, an
// engine.Fetcher adapter over it, and a temp sqlite store, all through engine.New
// — the full decoupled stack end to end.
func wire(t *testing.T, srv *httptest.Server, dl config.Download) (*engine.Engine, string) {
	t.Helper()
	client, err := httpx.New(config.Network{}, dl, httpx.WithRoundTripper(srv.Client().Transport))
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	store, err := sqlite.New(filepath.Join(t.TempDir(), "gidm.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	dir := t.TempDir()
	cfg := config.Config{Download: dl, Paths: config.Paths{DownloadDir: dir}}
	return engine.New(cfg, httpfetch.New(client), store), dir
}

func TestEndToEndRangeDownload(t *testing.T) {
	content := bytes.Repeat([]byte("gidm-range-"), 400000) // ~4.4 MiB, range-served
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Disposition", `attachment; filename="big.bin"`)
		// http.ServeContent honors Range and sets Accept-Ranges/Content-Range.
		http.ServeContent(w, r, "big.bin", time.Time{}, bytes.NewReader(content))
	}))
	defer srv.Close()

	e, dir := wire(t, srv, dlConfig())
	dl, err := e.Download(context.Background(), srv.URL+"/big.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if dl.Status != engine.StatusCompleted {
		t.Errorf("status = %q, want completed", dl.Status)
	}

	dest := filepath.Join(dir, "big.bin")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("downloaded bytes do not match source")
	}
	if _, statErr := os.Stat(dest + ".part"); !os.IsNotExist(statErr) {
		t.Errorf(".part file left behind: %v", statErr)
	}
}

func TestEndToEndWholeBodyFallback(t *testing.T) {
	content := bytes.Repeat([]byte("nostream-"), 200000) // ~1.8 MiB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// No Accept-Ranges, no Content-Length: forces the Probe to report no range
		// support and unknown size, so the engine takes the Get fallback.
		w.Header().Set("Accept-Ranges", "none")
		w.Header().Set("Content-Disposition", `attachment; filename="whole.bin"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	e, dir := wire(t, srv, dlConfig())
	dl, err := e.Download(context.Background(), srv.URL+"/whole.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if dl.Status != engine.StatusCompleted {
		t.Errorf("status = %q, want completed", dl.Status)
	}

	got, err := os.ReadFile(filepath.Join(dir, "whole.bin"))
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("fallback download bytes do not match source")
	}
}
