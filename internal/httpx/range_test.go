package httpx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
)

func TestRangeGetReturnsRequestedBytes(t *testing.T) {
	content := []byte("0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(content))
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	resp, err := c.RangeGet(context.Background(), srv.URL, 4, 9)
	if err != nil {
		t.Fatalf("RangeGet: %v", err)
	}
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "456789" {
		t.Errorf("body = %q, want 456789", body)
	}
}

func TestRangeGetRejects200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // ignores the Range header
		_, _ = w.Write([]byte("the whole file"))
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	resp, err := c.RangeGet(context.Background(), srv.URL, 0, 5)
	if !errors.Is(err, ErrRangeNotSatisfied) {
		closeBody(t, resp)
		t.Fatalf("err = %v, want ErrRangeNotSatisfied", err)
	}
}

func TestRangeGetValidatesContentRangeStart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes 99-200/1000") // wrong start
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("xxxx"))
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	resp, err := c.RangeGet(context.Background(), srv.URL, 4, 9)
	if err == nil {
		closeBody(t, resp)
		t.Fatal("RangeGet: expected error on mismatched Content-Range start")
	}
}

func TestRangeGetRejectsInvalidArgs(t *testing.T) {
	c := newTestClient(t, config.Network{}, testDownload(0, time.Millisecond))
	if _, err := c.RangeGet(context.Background(), "http://x/", -1, 5); err == nil {
		t.Error("RangeGet(-1, 5): expected error")
	}
	if _, err := c.RangeGet(context.Background(), "http://x/", 10, 5); err == nil {
		t.Error("RangeGet(10, 5): expected error")
	}
}

func TestValidateContentRange(t *testing.T) {
	if err := validateContentRange("bytes 4-9/16", 4); err != nil {
		t.Errorf("validateContentRange matching start: unexpected error %v", err)
	}
	if err := validateContentRange("bytes 5-9/16", 4); err == nil {
		t.Error("validateContentRange mismatched start: expected error")
	}
	if err := validateContentRange("nonsense", 4); err == nil {
		t.Error("validateContentRange malformed: expected error")
	}
}
