package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
)

func TestProbeWithRangeSupport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-0" {
			t.Errorf("Range header = %q, want bytes=0-0", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", "bytes 0-0/1048576")
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Content-Disposition", `attachment; filename="movie.mkv"`)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	res, err := c.Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !res.SupportsRanges {
		t.Error("SupportsRanges = false, want true")
	}
	if res.Size != 1048576 {
		t.Errorf("Size = %d, want 1048576", res.Size)
	}
	if res.ETag != `"v1"` {
		t.Errorf("ETag = %q, want %q", res.ETag, `"v1"`)
	}
	if res.LastModified == "" {
		t.Error("LastModified is empty")
	}
	if res.Filename != "movie.mkv" {
		t.Errorf("Filename = %q, want movie.mkv", res.Filename)
	}
}

func TestProbeWithoutRangeSupport(t *testing.T) {
	const body = "hello, world"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // ignores Range entirely
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	res, err := c.Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if res.SupportsRanges {
		t.Error("SupportsRanges = true, want false")
	}
	if res.Size != int64(len(body)) {
		t.Errorf("Size = %d, want %d", res.Size, len(body))
	}
}

func TestProbeAcceptRangesOn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	res, err := c.Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !res.SupportsRanges {
		t.Error("SupportsRanges = false, want true (Accept-Ranges: bytes on 200)")
	}
}

func TestProbeUnknownTotalSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-0/*")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	res, err := c.Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if res.Size != -1 {
		t.Errorf("Size = %d, want -1 for unknown total", res.Size)
	}
}

func TestProbeSanitizesContentDispositionFilename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="../../evil.sh"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	res, err := c.Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if res.Filename != "evil.sh" {
		t.Errorf("Filename = %q, want evil.sh (traversal neutralized)", res.Filename)
	}
}

func TestProbeDecodesAndSanitizesExtendedFilename(t *testing.T) {
	// RFC 5987 filename* whose decoded value contains traversal: %2e%2e%2f == "../".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''%2e%2e%2f%2e%2e%2fevil.bin")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	res, err := c.Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if res.Filename != "evil.bin" {
		t.Errorf("Filename = %q, want evil.bin (decoded then sanitized)", res.Filename)
	}
}

func TestProbeErrorsOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	if _, err := c.Probe(context.Background(), srv.URL); err == nil {
		t.Fatal("Probe: expected error on 403, got nil")
	}
}

func TestTotalFromContentRange(t *testing.T) {
	cases := map[string]int64{
		"bytes 0-0/1234": 1234,
		"bytes 0-0/*":    -1,
		"":               -1,
		"garbage":        -1,
		"bytes 0-0/":     -1,
	}
	for in, want := range cases {
		if got := totalFromContentRange(in); got != want {
			t.Errorf("totalFromContentRange(%q) = %d, want %d", in, got, want)
		}
	}
}
