package httpfetch_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
	"github.com/K-RED90/gidm/internal/engine"
	"github.com/K-RED90/gidm/internal/httpfetch"
	"github.com/K-RED90/gidm/internal/httpx"
	"github.com/K-RED90/gidm/internal/store/sqlite"
)

// wireManager builds the full decoupled stack — a real httpx.Client routed at
// srv, the engine.Fetcher adapter over it, a temp sqlite store, the engine, and
// a Manager — proving the supervisor works against the production store and a
// real HTTP server, not just the in-package fakes.
func wireManager(t *testing.T, srv *httptest.Server, dl config.Download, maxConcurrent int) (*engine.Manager, *sqlite.Store, string) {
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
	e := engine.New(cfg, httpfetch.New(client), store)
	return engine.NewManager(e, store, maxConcurrent), store, dir
}

func waitManagerStatus(t *testing.T, m *engine.Manager, id string, want engine.Status, within time.Duration) *engine.Download {
	t.Helper()
	deadline := time.Now().Add(within)
	var last engine.Status
	for time.Now().Before(deadline) {
		d, err := m.Get(context.Background(), id)
		if err == nil {
			last = d.Status
			if d.Status == want {
				return d
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("download %q status = %q, want %q within %s", id, last, want, within)
	return nil
}

func TestManagerEndToEndConcurrent(t *testing.T) {
	content := bytes.Repeat([]byte("gidm-mgr-"), 200000) // ~1.8 MiB, range-served
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		// Serve a distinct filename per path so concurrent downloads do not collide.
		name := filepath.Base(r.URL.Path)
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(content))
	}))
	defer srv.Close()

	m, store, dir := wireManager(t, srv, dlConfig(), 2)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	const n = 4
	ids := make([]string, n)
	for i := range ids {
		id, err := m.Submit(context.Background(), srv.URL+"/file-"+itoa(i)+".bin", engine.PriorityNormal, engine.AddOptions{})
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
		ids[i] = id
	}

	for i, id := range ids {
		waitManagerStatus(t, m, id, engine.StatusCompleted, 15*time.Second)
		got, err := os.ReadFile(filepath.Join(dir, "file-"+itoa(i)+".bin"))
		if err != nil {
			t.Fatalf("read dest %d: %v", i, err)
		}
		if sha256Hex(got) != sha256Hex(content) {
			t.Errorf("download %d bytes do not match source", i)
		}
		persisted, err := store.LoadDownload(context.Background(), id)
		if err != nil {
			t.Fatalf("LoadDownload %d: %v", i, err)
		}
		if persisted.Status != engine.StatusCompleted {
			t.Errorf("persisted status %d = %q, want completed", i, persisted.Status)
		}
	}
}

func TestManagerEndToEndPauseResume(t *testing.T) {
	content := bytes.Repeat([]byte("gidm-pr-"), 300000) // ~2.4 MiB
	gate := make(chan struct{})
	var gatedOnce atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Disposition", `attachment; filename="pr.bin"`)
		// The probe sends Range: bytes=0-0; let it through so the engine learns the
		// size and opens the .part. Hold the first *full* segment request open until
		// released, so the manager can pause a genuinely in-flight transfer; the
		// resume request then serves immediately.
		rng := r.Header.Get("Range")
		if rng != "" && rng != "bytes=0-0" && gatedOnce.CompareAndSwap(false, true) {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		http.ServeContent(w, r, "pr.bin", time.Time{}, bytes.NewReader(content))
	}))
	defer srv.Close()

	cfg := dlConfig()
	cfg.SegmentsPerDownload = 1 // single in-flight body so the gate catches it
	m, store, dir := wireManager(t, srv, cfg, 2)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	id, err := m.Submit(context.Background(), srv.URL+"/pr.bin", engine.PriorityNormal, engine.AddOptions{})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitManagerStatus(t, m, id, engine.StatusActive, 5*time.Second)
	time.Sleep(100 * time.Millisecond)

	if err := m.Pause(context.Background(), id); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	waitManagerStatus(t, m, id, engine.StatusPaused, 5*time.Second)

	dest := filepath.Join(dir, "pr.bin")
	if _, err := os.Stat(dest + ".part"); err != nil {
		t.Errorf(".part missing after pause: %v", err)
	}

	close(gate) // let any future request serve immediately
	if err := m.Resume(context.Background(), id); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitManagerStatus(t, m, id, engine.StatusCompleted, 15*time.Second)

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("resumed file does not match source")
	}
	persisted, err := store.LoadDownload(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if persisted.Status != engine.StatusCompleted {
		t.Errorf("persisted status = %q, want completed", persisted.Status)
	}
}

// itoa avoids importing strconv just for a single-digit index.
func itoa(i int) string { return string(rune('0' + i)) }
