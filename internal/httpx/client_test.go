package httpx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
)

const testUA = "gidm-test/1.0"

// roundTripFunc adapts a function into an http.RoundTripper so tests can drive
// the client without touching the network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// respFor builds a response bound to the request, as a real transport would.
func respFor(r *http.Request, code int, hdr http.Header, body string) *http.Response {
	if hdr == nil {
		hdr = make(http.Header)
	}
	return &http.Response{
		StatusCode:    code,
		Status:        fmt.Sprintf("%d %s", code, http.StatusText(code)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        hdr,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       r,
	}
}

// closeBody closes a response body and reports a failure on error; it tolerates
// a nil response so error-path tests can call it unconditionally.
func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()
	if resp == nil {
		return
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
}

func testDownload(maxRetries int, backoff time.Duration) config.Download {
	return config.Download{
		Timeout:      config.Duration(5 * time.Second),
		MaxRetries:   maxRetries,
		RetryBackoff: config.Duration(backoff),
	}
}

// newTestClient builds a client and pins jitter to 0 so backoff is deterministic.
func newTestClient(t *testing.T, netCfg config.Network, dl config.Download, opts ...Option) *Client {
	t.Helper()
	c, err := New(netCfg, dl, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.jitter = func() float64 { return 0 }
	return c
}

// newServerClient builds a client for httptest servers: jitter pinned and the
// proxy forced off so ambient *_PROXY env can't divert the loopback request.
func newServerClient(t *testing.T, netCfg config.Network, dl config.Download, opts ...Option) *Client {
	t.Helper()
	if netCfg.UserAgent == "" {
		netCfg.UserAgent = testUA
	}
	c := newTestClient(t, netCfg, dl, opts...)
	if tr, ok := c.hc.Transport.(*http.Transport); ok {
		tr.Proxy = nil
	}
	return c
}

func TestNewRejectsBadProxyURL(t *testing.T) {
	if _, err := New(config.Network{ProxyURL: "http://%zz"}, testDownload(0, time.Millisecond)); err == nil {
		t.Fatal("New: expected error for unparseable proxy url, got nil")
	}
}

func TestNewAppliesUserAgentToEveryRequest(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{UserAgent: "gidm-ua/9.9"}, testDownload(0, time.Millisecond))
	if _, err := c.Probe(context.Background(), srv.URL, RequestOptions{}); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got != "gidm-ua/9.9" {
		t.Errorf("User-Agent = %q, want %q", got, "gidm-ua/9.9")
	}
}
