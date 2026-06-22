package httpx

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/K-RED90/gidm/internal/config"
)

// Sentinel errors returned by the client. Match them with errors.Is: redirect
// errors arrive wrapped in *url.Error from the standard http.Client.
var (
	ErrTooManyRedirects  = errors.New("httpx: too many redirects")
	ErrInsecureRedirect  = errors.New("httpx: insecure redirect")
	ErrRangeNotSatisfied = errors.New("httpx: server did not honor range request")
)

const (
	defaultMaxRedirects   = 10
	dialKeepAlive         = 30 * time.Second
	idleConnTimeout       = 90 * time.Second
	expectContinueTimeout = time.Second
	maxIdleConns          = 100
	maxIdleConnsPerHost   = 64 // bumped for the HTTP/1.1 segmented-download path
	maxRetryBackoff       = 30 * time.Second
	drainLimit            = 8 << 10 // bytes drained from a stale body before close
)

// Client is gidm's HTTP adapter: one pooled *http.Client wrapped with a secure
// redirect policy, ranged requests, and retry with backoff. It is safe for
// concurrent use. Build it with New; the zero value is not usable.
type Client struct {
	hc           *http.Client
	userAgent    string
	maxRetries   int
	retryBackoff time.Duration
	maxRedirects int
	// jitter returns a value in [0,1) used to spread retry backoff; it is a field
	// (not a package global) so tests can make backoff deterministic.
	jitter func() float64
}

// Option customizes a Client at construction time.
type Option func(*Client)

// WithRoundTripper replaces the client's transport so tests can inject an
// in-memory RoundTripper and never touch the network. The redirect policy and
// User-Agent still apply, as both live on the wrapping *http.Client.
func WithRoundTripper(rt http.RoundTripper) Option {
	return func(c *Client) { c.hc.Transport = rt }
}

// WithMaxRedirects caps the redirect chain; 0 refuses all redirects. Negative
// values are ignored, keeping the default.
func WithMaxRedirects(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.maxRedirects = n
		}
	}
}

// New builds a Client from the network and download config. Transport timeouts
// derive from dl.Timeout and retries from dl.MaxRetries/RetryBackoff. It returns
// an error only when netCfg.ProxyURL is set but unparseable.
//
// The client deliberately sets no overall http.Client.Timeout, which would abort
// long streaming downloads; only connection setup is bounded. Detecting a body
// that stalls mid-stream is the caller's responsibility (via its context).
func New(netCfg config.Network, dl config.Download, opts ...Option) (*Client, error) {
	proxy, err := proxyFunc(netCfg.ProxyURL)
	if err != nil {
		return nil, err
	}

	timeout := dl.Timeout.Duration()
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: dialKeepAlive,
	}
	if netCfg.TCPRecvBuf > 0 || netCfg.TCPSendBuf > 0 {
		dialer.Control = tcpBufControl(netCfg.TCPRecvBuf, netCfg.TCPSendBuf)
	}

	transport := &http.Transport{
		Proxy:       proxy,
		DialContext: dialer.DialContext,
		// HTTP/2 is opt-in (netCfg.HTTP2): an H2 origin multiplexes every segment
		// onto one TCP connection, collapsing the parallel ranges into a single
		// congestion window and the server's per-connection rate limit. The default
		// (false) forces HTTP/1.1 below so each segment is its own connection.
		ForceAttemptHTTP2:   netCfg.HTTP2,
		MaxIdleConns:        maxIdleConns,
		MaxIdleConnsPerHost: maxIdleConnsPerHost,
		IdleConnTimeout:     idleConnTimeout,
		// MaxConnsPerHost is left at 0 (unlimited) on purpose: capping it would
		// serialize concurrent segments behind a connection limit.
		TLSHandshakeTimeout:   timeout,
		ExpectContinueTimeout: expectContinueTimeout,
		ResponseHeaderTimeout: timeout,
		// A download manager wants the file's raw bytes: transparent gzip would
		// burn CPU decompressing and muddy Content-Length/Content-Range range math.
		DisableCompression: true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: netCfg.TLSSkipVerify,
			MinVersion:         tls.VersionTLS12,
		},
	}
	if netCfg.SocketBufferSize > 0 {
		transport.ReadBufferSize = netCfg.SocketBufferSize
		transport.WriteBufferSize = netCfg.SocketBufferSize
	}
	if !netCfg.HTTP2 {
		// A non-nil (empty) TLSNextProto disables net/http's automatic HTTP/2
		// upgrade even on an h2-advertising origin, so segments stay on separate
		// HTTP/1.1 connections. NextProtos pins the ALPN offer to http/1.1 to match.
		transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
		transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	}

	c := &Client{
		userAgent:    netCfg.UserAgent,
		maxRetries:   dl.MaxRetries,
		retryBackoff: dl.RetryBackoff.Duration(),
		maxRedirects: defaultMaxRedirects,
		jitter:       rand.Float64,
	}
	c.hc = &http.Client{
		Transport:     transport,
		CheckRedirect: c.checkRedirect,
	}

	// Options last, so test injection can override the config-derived setup.
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// proxyFunc resolves the transport's proxy: an explicit URL if configured, else
// the standard environment proxy (HTTP_PROXY/HTTPS_PROXY/NO_PROXY).
func proxyFunc(rawURL string) (func(*http.Request) (*url.URL, error), error) {
	if rawURL == "" {
		return http.ProxyFromEnvironment, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("httpx: parse proxy url %q: %w", rawURL, err)
	}
	return http.ProxyURL(u), nil
}

// newRequest builds a request and applies the User-Agent. The standard client
// copies non-sensitive headers (User-Agent, Range) onto every redirect hop, so
// setting them once here is enough.
func (c *Client) newRequest(ctx context.Context, method, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("httpx: new request %q: %w", rawURL, err)
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	return req, nil
}
