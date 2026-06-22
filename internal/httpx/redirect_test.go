package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
)

func TestRedirectCapExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond), WithMaxRedirects(3))
	_, err := c.Probe(context.Background(), srv.URL, RequestOptions{})
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("err = %v, want ErrTooManyRedirects", err)
	}
}

func TestRedirectDowngradeRejected(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusFound) // https -> http downgrade
	}))
	defer secure.Close()

	c := newServerClient(t, config.Network{TLSSkipVerify: true}, testDownload(0, time.Millisecond))
	_, err := c.Probe(context.Background(), secure.URL, RequestOptions{})
	if !errors.Is(err, ErrInsecureRedirect) {
		t.Fatalf("err = %v, want ErrInsecureRedirect", err)
	}
}

func TestRedirectNonHTTPSchemeRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://example.com/file", http.StatusFound)
	}))
	defer srv.Close()

	c := newServerClient(t, config.Network{}, testDownload(0, time.Millisecond))
	_, err := c.Probe(context.Background(), srv.URL, RequestOptions{})
	if !errors.Is(err, ErrInsecureRedirect) {
		t.Fatalf("err = %v, want ErrInsecureRedirect", err)
	}
}

func TestRedirectUpgradeAllowed(t *testing.T) {
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
	}))
	defer secure.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secure.URL, http.StatusFound) // http -> https upgrade
	}))
	defer plain.Close()

	c := newServerClient(t, config.Network{TLSSkipVerify: true}, testDownload(0, time.Millisecond))
	res, err := c.Probe(context.Background(), plain.URL, RequestOptions{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !strings.HasPrefix(res.URL, "https://") {
		t.Errorf("final URL = %q, want an https:// upgrade", res.URL)
	}
}
