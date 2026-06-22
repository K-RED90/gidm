package apiserver_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/internal/apiserver"
	"github.com/K-RED90/gidm/internal/config"
	"github.com/K-RED90/gidm/internal/engine"
	"github.com/K-RED90/gidm/internal/httpfetch"
	"github.com/K-RED90/gidm/internal/httpx"
	"github.com/K-RED90/gidm/internal/store/sqlite"
)

func dlConfig() config.Download {
	return config.Download{
		MaxConcurrent:       2,
		SegmentsPerDownload: 4,
		BufferSize:          32 * 1024,
		Timeout:             config.Duration(30 * time.Second),
		MaxRetries:          2,
		RetryBackoff:        config.Duration(10 * time.Millisecond),
	}
}

// TestFullStackEndToEnd wires the production stack — real httpx routed at an
// httptest file server, the httpfetch adapter, a temp sqlite store, the engine,
// a Manager, and the apiserver over a unix socket — and round-trips a download
// to completion over the wire.
func TestFullStackEndToEnd(t *testing.T) {
	content := bytes.Repeat([]byte("gidm-apisrv-"), 100000) // ~1.2 MiB, range-served
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Disposition", `attachment; filename="file.bin"`)
		http.ServeContent(w, r, "file.bin", time.Time{}, bytes.NewReader(content))
	}))
	defer httpSrv.Close()

	dl := dlConfig()
	client, err := httpx.New(config.Network{}, dl, httpx.WithRoundTripper(httpSrv.Client().Transport))
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	store, err := sqlite.New(filepath.Join(t.TempDir(), "gidm.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.Config{Download: dl, Paths: config.Paths{DownloadDir: t.TempDir()}}
	e := engine.New(cfg, httpfetch.New(client), store)
	mgr := engine.NewManager(e, store, dl.MaxConcurrent)
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("manager start: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })

	sock := tempSocketPath(t)
	srv := apiserver.New(mgr, testLogger(), config.Daemon{SocketPath: sock})
	served := make(chan error, 1)
	go func() { served <- srv.Serve(context.Background()) }()
	waitListening(t, sock)
	t.Cleanup(func() { _ = srv.Close(); <-served })

	// Submit a download over the wire.
	addResp := roundTrip(t, sock, api.NewAddRequest(httpSrv.URL+"/file.bin"))
	if !addResp.OK || addResp.Add == nil {
		t.Fatalf("add: %+v", addResp)
	}
	id := addResp.Add.ID

	// Poll status over the wire until completed.
	deadline := time.Now().Add(10 * time.Second)
	var last api.DownloadStatus
	for time.Now().Before(deadline) {
		r := roundTrip(t, sock, api.NewStatusRequest(id))
		if r.OK && r.Status != nil {
			last = r.Status.Download.Status
			if last == api.StatusCompleted {
				if r.Status.Download.TotalSize != int64(len(content)) {
					t.Fatalf("total size = %d, want %d", r.Status.Download.TotalSize, len(content))
				}
				if r.Status.Download.Downloaded != int64(len(content)) {
					t.Fatalf("downloaded = %d, want %d", r.Status.Download.Downloaded, len(content))
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("download did not complete; last wire status = %q", last)
}
