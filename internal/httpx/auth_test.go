package httpx

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
)

func partialResponse(r *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusPartialContent,
		Header:     http.Header{"Content-Range": {"bytes 0-0/100"}},
		Body:       io.NopCloser(strings.NewReader("x")),
		Request:    r,
	}
}

func TestRequestOptionsApplied(t *testing.T) {
	var captured *http.Request
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured = r
		return partialResponse(r), nil
	})
	c := newTestClient(t, config.Network{}, testDownload(0, time.Millisecond), WithRoundTripper(rt))

	opts := RequestOptions{
		Username: "neo", Password: "trinity",
		Referer: "https://ref.example/", Cookie: "sid=abc",
		Headers: map[string]string{"X-Token": "bearer-xyz"},
	}
	if _, err := c.Probe(context.Background(), "https://example.com/f", opts); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if captured == nil {
		t.Fatal("request not captured")
	}
	if u, p, ok := captured.BasicAuth(); !ok || u != "neo" || p != "trinity" {
		t.Errorf("basic auth = %q/%q/%v, want neo/trinity/true", u, p, ok)
	}
	if got := captured.Header.Get("Referer"); got != "https://ref.example/" {
		t.Errorf("Referer = %q", got)
	}
	if got := captured.Header.Get("Cookie"); got != "sid=abc" {
		t.Errorf("Cookie = %q", got)
	}
	if got := captured.Header.Get("X-Token"); got != "bearer-xyz" {
		t.Errorf("X-Token = %q", got)
	}
	if got := captured.Header.Get("Range"); got != "bytes=0-0" {
		t.Errorf("Range = %q, want bytes=0-0 (engine header preserved over opts)", got)
	}
}

func TestRequestOptionsZeroAddsNothing(t *testing.T) {
	var captured *http.Request
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured = r
		return partialResponse(r), nil
	})
	c := newTestClient(t, config.Network{}, testDownload(0, time.Millisecond), WithRoundTripper(rt))

	if _, err := c.Probe(context.Background(), "https://example.com/f", RequestOptions{}); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if _, _, ok := captured.BasicAuth(); ok {
		t.Error("zero options set basic auth")
	}
	if captured.Header.Get("Referer") != "" || captured.Header.Get("Cookie") != "" {
		t.Error("zero options set referer/cookie")
	}
}

// Basic auth must not ride a cross-host redirect (the CDN a download page sends
// you to), matching IDM's per-host model. The standard client strips the
// Authorization header on a cross-domain hop; this guards that we rely on it.
func TestBasicAuthStrippedOnCrossHostRedirect(t *testing.T) {
	var second *http.Request
	hop := 0
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hop++
		if hop == 1 {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": {"https://cdn.other.example/f"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		}
		second = r
		return partialResponse(r), nil
	})
	c := newTestClient(t, config.Network{}, testDownload(0, time.Millisecond), WithRoundTripper(rt))

	opts := RequestOptions{Username: "u", Password: "p", Referer: "https://origin.example/"}
	if _, err := c.Probe(context.Background(), "https://origin.example/f", opts); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if second == nil {
		t.Fatal("redirect not followed")
	}
	if _, _, ok := second.BasicAuth(); ok {
		t.Error("Authorization leaked across a cross-host redirect")
	}
}
