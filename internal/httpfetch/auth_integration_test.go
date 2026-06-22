package httpfetch_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/K-RED90/gidm/internal/config"
	"github.com/K-RED90/gidm/internal/engine"
	"github.com/K-RED90/gidm/internal/httpfetch"
	"github.com/K-RED90/gidm/internal/httpx"
	"github.com/K-RED90/gidm/internal/secret"
	"github.com/K-RED90/gidm/internal/store/sqlite"
)

// wireVault is wire with a credential vault, so the store can persist a
// download's encrypted Auth.
func wireVault(t *testing.T, srv *httptest.Server, dl config.Download) (*engine.Engine, string) {
	t.Helper()
	client, err := httpx.New(config.Network{}, dl, httpx.WithRoundTripper(srv.Client().Transport))
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	vault, err := secret.NewFromFile(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("secret.NewFromFile: %v", err)
	}
	store, err := sqlite.New(filepath.Join(t.TempDir(), "gidm.db"), sqlite.WithVault(vault))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	dir := t.TempDir()
	cfg := config.Config{Download: dl, Paths: config.Paths{DownloadDir: dir}}
	return engine.New(cfg, httpfetch.New(client), store), dir
}

// TestEndToEndBasicAuth drives the full httpx → httpfetch → engine stack against a
// server that requires HTTP Basic auth and a custom header: the download completes
// only when the per-download credentials are threaded through, and fails (401) when
// they are absent — proving auth flows the whole way down.
func TestEndToEndBasicAuth(t *testing.T) {
	payload := bytes.Repeat([]byte("Z"), 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "neo" || p != "trinity" {
			w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-Token") != "xyz" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	e, dir := wireVault(t, srv, dlConfig())
	auth := &engine.RequestOptions{
		Username: "neo",
		Password: "trinity",
		Headers:  map[string]string{"X-Token": "xyz"},
	}

	// With credentials → completes and writes the full file.
	dl := &engine.Download{ID: "auth-ok", URL: srv.URL + "/f.bin", Auth: auth}
	got, err := e.Run(context.Background(), dl)
	if err != nil {
		t.Fatalf("Run with auth: %v", err)
	}
	if got.Status != engine.StatusCompleted {
		t.Fatalf("status = %s, want completed", got.Status)
	}
	data, err := os.ReadFile(filepath.Join(dir, "f.bin"))
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("downloaded file mismatch (err %v, %d bytes)", err, len(data))
	}

	// Without credentials → the probe gets a 401 and the run fails.
	dl2 := &engine.Download{ID: "auth-missing", URL: srv.URL + "/f2.bin"}
	if _, err := e.Run(context.Background(), dl2); err == nil {
		t.Fatal("Run without auth succeeded, want failure (401)")
	}
}
