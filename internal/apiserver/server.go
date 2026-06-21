// Package apiserver is the daemon's Unix-socket control server. It owns a
// unix-only net.Listener (never tcp), speaks the internal/api NDJSON wire
// protocol, and dispatches each verb to a Manager facade. It sits above the
// engine: it may import internal/engine (for the Download type and sentinel
// errors it translates), but the engine never imports this package, keeping the
// engine a pure library.
//
// Security model: the socket is created under the configured data directory with
// 0600 permissions (POSIX; degraded with a debug log on Windows), the parent
// directory is refused if world-writable, and a single-instance guard probes any
// pre-existing socket with an api ping before unlinking a stale one — a live
// daemon answering the probe causes New/Serve to refuse to start.
package apiserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/K-RED90/gidm/internal/api"
	"github.com/K-RED90/gidm/internal/config"
	"github.com/K-RED90/gidm/internal/engine"
)

// Manager is the facade the server needs from engine.Manager. Accepting an
// interface (rather than *engine.Manager) keeps the server testable with a fake
// and documents exactly the verbs it dispatches. *engine.Manager satisfies it;
// the satisfaction assertion lives in the apiserver test, never in package
// engine, so the engine's import-clean invariant is preserved.
type Manager interface {
	Submit(ctx context.Context, url string, priority engine.Priority) (string, error)
	List(ctx context.Context) ([]*engine.Download, error)
	Get(ctx context.Context, id string) (*engine.Download, error)
	Pause(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
	Cancel(ctx context.Context, id string) error
	SetPriority(ctx context.Context, id string, priority engine.Priority) error
}

// ErrAlreadyRunning is returned by Serve when a live daemon already owns the
// configured socket (single-instance guard). The caller should exit non-zero.
var ErrAlreadyRunning = errors.New("apiserver: another daemon is already listening on the socket")

// Server is the concrete control server. Construct it with New, then call Serve
// (blocking) and Close (graceful stop).
type Server struct {
	mgr    Manager
	logger *slog.Logger
	cfg    config.Daemon
	// defaultPriority is applied to an add request that omits a priority. It is
	// resolved from config by the daemon, so the default is a tunable, not hardcoded.
	defaultPriority engine.Priority

	mu       sync.Mutex
	listener net.Listener
	baseCtx  context.Context
	cancel   context.CancelFunc
	conns    sync.WaitGroup
	// active tracks every accepted connection still being handled so Close can
	// force-close them when the shutdown deadline fires. Forcing the close
	// unblocks any handler stuck in a Read/Write, which lets conns.Wait return —
	// so the drain goroutine always terminates and never leaks past Close.
	active map[net.Conn]struct{}
}

// New builds a Server over mgr, applying defaultPriority to add requests that
// omit one (the daemon resolves it from config). A nil logger defaults to
// slog.Default(). Each non-positive timeout/limit in cfg is floored to its
// package default so a hand-built config.Daemon{} (as in some tests) never yields
// a zero deadline.
func New(mgr Manager, logger *slog.Logger, cfg config.Daemon, defaultPriority engine.Priority) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = config.Duration(30 * time.Second)
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = config.Duration(10 * time.Second)
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = config.Duration(10 * time.Second)
	}
	if cfg.MaxRequestBytes < 1 {
		cfg.MaxRequestBytes = 1 << 20
	}
	if !defaultPriority.Valid() {
		defaultPriority = engine.PriorityNormal
	}
	return &Server{mgr: mgr, logger: logger, cfg: cfg, defaultPriority: defaultPriority, active: make(map[net.Conn]struct{})}
}

// Serve binds the configured socket and accepts connections until Close is
// called or ctx is cancelled. It blocks. It returns nil on a clean shutdown
// (listener closed), ErrAlreadyRunning if a live daemon already owns the socket,
// or a wrapped error if binding fails.
func (s *Server) Serve(ctx context.Context) error {
	l, err := listen(s.cfg.SocketPath, s.logger)
	if err != nil {
		return err
	}

	baseCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.listener = l
	s.baseCtx = baseCtx
	s.cancel = cancel
	s.mu.Unlock()

	for {
		conn, err := l.Accept()
		if err != nil {
			// A closed listener is the normal Close path; report it as a clean stop.
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			// The base context being done also means we are shutting down.
			if baseCtx.Err() != nil {
				return nil
			}
			s.logger.Warn("apiserver: accept failed", "err", err)
			continue
		}
		s.conns.Add(1)
		s.trackConn(conn)
		go s.handleConn(baseCtx, conn)
	}
}

// Close stops accepting, cancels in-flight handler contexts, waits (bounded by
// ShutdownTimeout) for connection goroutines to drain, and removes the socket
// file. It is safe to call once after Serve; Manager.Shutdown is the caller's
// responsibility (the server never drains the Manager, so there is no double
// drain).
func (s *Server) Close() error {
	s.mu.Lock()
	l := s.listener
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if l == nil {
		// Serve never bound (e.g. single-instance refusal): the socket on disk belongs
		// to another daemon. Do not touch it.
		return nil
	}
	_ = l.Close() // unblocks Accept with net.ErrClosed

	// Wait for connection goroutines to drain, bounded by ShutdownTimeout. The
	// monitor goroutine only signals completion; on timeout we force-close every
	// still-open connection, which unblocks its handler's Read/Write so conns.Wait
	// returns and the monitor goroutine exits — no goroutine survives Close.
	done := make(chan struct{})
	go func() {
		s.conns.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(s.cfg.ShutdownTimeout.Duration()):
		s.logger.Warn("apiserver: shutdown timed out; force-closing open connections")
		s.closeActive()
		// Now that every connection is closed, the handlers unblock and conns.Wait
		// returns; block until the monitor goroutine confirms it to avoid leaking it.
		<-done
	}

	// Closing a *net.UnixListener already unlinks the socket file; this is a
	// best-effort backstop for any listener that does not, and a no-op otherwise.
	removeSocket(s.cfg.SocketPath, s.logger)
	return nil
}

// Addr reports the bound listener address, or nil before Serve binds. Tests use
// it to assert the network is "unix" and never "tcp".
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// trackConn records an accepted connection so Close can force it shut on a
// shutdown-timeout. untrackConn drops it once its handler is done.
func (s *Server) trackConn(conn net.Conn) {
	s.mu.Lock()
	s.active[conn] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) untrackConn(conn net.Conn) {
	s.mu.Lock()
	delete(s.active, conn)
	s.mu.Unlock()
}

// closeActive force-closes every still-open connection. Closing the conn
// unblocks a handler parked in Read or Write, so the per-connection goroutine
// returns promptly and conns.Wait can complete.
func (s *Server) closeActive() {
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.active))
	for c := range s.active {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// handleConn reads exactly one request frame, dispatches it, and writes one
// response frame. One request per connection keeps the model simple and bounds
// resource use; the read is deadline-bounded and size-capped so a slow or
// oversized client cannot pin the goroutine or exhaust memory.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer s.conns.Done()
	defer func() {
		s.untrackConn(conn)
		_ = conn.Close()
	}()

	start := time.Now()

	_ = conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout.Duration()))
	limited := newLimitedReader(conn, int64(s.cfg.MaxRequestBytes))

	var req api.Request
	if err := api.NewDecoder(limited).Decode(&req); err != nil {
		s.respond(conn, api.ErrorResponse(api.CodeBadRequest, "malformed request"))
		s.logger.Warn("request", "verb", "?", "id", "", "outcome", string(api.CodeBadRequest), "duration", time.Since(start))
		return
	}

	resp, verb, id := s.dispatch(ctx, &req)

	_ = conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout.Duration()))
	s.respond(conn, resp)

	outcome := "ok"
	if !resp.OK && resp.Error != nil {
		outcome = string(resp.Error.Code)
	}
	s.logger.Info("request", "verb", verb, "id", id, "outcome", outcome, "duration", time.Since(start))
}

func (s *Server) respond(conn net.Conn, resp api.Response) {
	if err := api.WriteMessage(conn, resp); err != nil {
		// Best-effort: the peer is gone or slow; the connection is about to close.
		s.logger.Debug("apiserver: write response failed", "err", err)
	}
}
