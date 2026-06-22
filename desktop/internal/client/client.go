// Package client speaks the gidm api NDJSON protocol to a running gidmd over its
// Unix socket. It mirrors the CLI's client (cmd/gidm/client.go): each call dials,
// writes one request frame, reads one response frame, and closes — matching the
// daemon's one-request-per-connection model. It holds no connection and imports
// no Wails/GUI code, so it stays a small, race-tested leaf the bridge wraps.
package client

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"time"

	"github.com/K-RED90/gidm/api"
)

var (
	// ErrDaemonNotRunning means the socket is absent or refuses the connection —
	// gidmd is almost certainly not running.
	ErrDaemonNotRunning = errors.New("daemon not running")
	// ErrTimeout means the dial or round-trip exceeded the configured timeout.
	ErrTimeout = errors.New("request timed out")
)

// Client dials a gidmd control socket. It owns no connection: each Do dials,
// round-trips one frame, and closes.
type Client struct {
	socket  string
	timeout time.Duration
}

// New builds a Client for the given socket path and per-call dial/round-trip
// timeout.
func New(socket string, timeout time.Duration) *Client {
	return &Client{socket: socket, timeout: timeout}
}

// Socket returns the path this client dials (for surfacing in UI hints).
func (c *Client) Socket() string { return c.socket }

// Do performs one request/response round-trip over a fresh connection. It bounds
// the whole exchange by the configured timeout (set as the connection deadline)
// and maps transport failures to the ErrDaemonNotRunning / ErrTimeout sentinels.
func (c *Client) Do(ctx context.Context, req api.Request) (api.Response, error) {
	dialCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", c.socket)
	if err != nil {
		return api.Response{}, c.classifyDialErr(err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if err := api.WriteMessage(conn, req); err != nil {
		return api.Response{}, c.classifyIOErr(dialCtx, "write request", err)
	}

	var resp api.Response
	if err := api.NewDecoder(conn).Decode(&resp); err != nil {
		return api.Response{}, c.classifyIOErr(dialCtx, "read response", err)
	}
	return resp, nil
}

// classifyDialErr maps a dial failure to a sentinel: a missing socket file or a
// refused connection both mean gidmd is not listening; a deadline means a timeout.
func (c *Client) classifyDialErr(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist), isConnRefused(err):
		return fmt.Errorf("dial %q: %w: %w", c.socket, ErrDaemonNotRunning, err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("dial %q: %w: %w", c.socket, ErrTimeout, err)
	default:
		return fmt.Errorf("dial %q: %w", c.socket, err)
	}
}

// classifyIOErr maps a post-dial read/write failure to a sentinel; a fired
// connection deadline surfaces as both a net timeout and a context deadline, so
// both are checked.
func (c *Client) classifyIOErr(ctx context.Context, op string, err error) error {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil ||
		(errors.As(err, &netErr) && netErr.Timeout()) {
		return fmt.Errorf("%s on %q: %w: %w", op, c.socket, ErrTimeout, err)
	}
	return fmt.Errorf("%s on %q: %w", op, c.socket, err)
}
