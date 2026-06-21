package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"time"

	"github.com/K-RED90/gidm/internal/api"
)

// errDaemonNotRunning is returned when the socket is absent or refuses the
// connection — the daemon is almost certainly not running. errTimeout is
// returned when the configured DialTimeout elapses before the round-trip
// completes. Both are sentinels so main can map them to distinct exit codes and
// hints without string-matching.
var (
	errDaemonNotRunning = errors.New("daemon not running")
	errTimeout          = errors.New("request timed out")
)

// client speaks the api NDJSON protocol to gidmd over a Unix socket. It holds no
// connection: each call dials, sends one frame, reads one frame, and closes —
// matching the daemon's one-request-per-connection model.
type client struct {
	socket  string
	timeout time.Duration
}

// do performs one request/response round-trip. It derives a child context
// bounded by the configured timeout, dials the socket (never tcp) so a hung dial
// aborts on the deadline, sets the same deadline on the connection so a daemon
// that accepts but never replies cannot wedge the read, writes the request frame
// and decodes the single response frame. Dial and read errors are classified
// into the errDaemonNotRunning / errTimeout sentinels using cross-platform
// errors.Is checks (no raw POSIX errno assumptions), so the same code behaves on
// Linux, macOS, and Windows.
func (c *client) do(ctx context.Context, req api.Request) (api.Response, error) {
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

// classifyDialErr maps a dial failure to a sentinel. A missing socket file
// (fs.ErrNotExist) and a refused connection (isConnRefused, which spells the
// errno differently per OS) both mean the daemon is not listening; a deadline
// means the dial timed out.
func (c *client) classifyDialErr(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist), isConnRefused(err):
		return fmt.Errorf("dial %q: %w: %w", c.socket, errDaemonNotRunning, err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("dial %q: %w: %w", c.socket, errTimeout, err)
	default:
		return fmt.Errorf("dial %q: %w", c.socket, err)
	}
}

// classifyIOErr maps a post-dial read/write failure to a sentinel. When the
// connection deadline fires mid-I/O the net error is a timeout and the context
// deadline is exceeded, so both signals are checked.
func (c *client) classifyIOErr(ctx context.Context, op string, err error) error {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil ||
		(errors.As(err, &netErr) && netErr.Timeout()) {
		return fmt.Errorf("%s on %q: %w: %w", op, c.socket, errTimeout, err)
	}
	return fmt.Errorf("%s on %q: %w", op, c.socket, err)
}
