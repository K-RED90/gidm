package httpx

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
)

func TestGetStreamsFullBody(t *testing.T) {
	content := []byte("0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "16")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	resp, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(body, content) {
		t.Errorf("body = %q, want %q", body, content)
	}
}

func TestGetRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	resp, err := c.Get(context.Background(), srv.URL)
	if err == nil {
		closeBody(t, resp)
		t.Fatal("Get: expected error for non-2xx status")
	}
}

// A 206 with an unsolicited range is tolerated: the fallback reads from offset 0
// and a server that volunteers a range starting at 0 is still serving the whole
// body sequentially.
func TestGetToleratesUnsolicited206(t *testing.T) {
	content := []byte("hello world")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-10/11")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	resp, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer closeBody(t, resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(body, content) {
		t.Errorf("body = %q, want %q", body, content)
	}
}
