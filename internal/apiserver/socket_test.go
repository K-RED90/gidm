package apiserver_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/api"
	"github.com/K-RED90/gidm/internal/apiserver"
	"github.com/K-RED90/gidm/internal/config"
	"github.com/K-RED90/gidm/internal/engine"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestSocketMode0600 asserts the bound socket is owner-only on POSIX. The check
// is skipped on Windows, where unix-socket files do not carry 0600 semantics.
func TestSocketMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("0600 socket permissions are not enforced on Windows")
	}
	mgr := newFakeManager()
	_, sock := startServer(t, mgr)

	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode = %o, want 0600", perm)
	}
}

// TestListenerIsUnixNotTCP asserts the server binds a unix socket and never a
// TCP port.
func TestListenerIsUnixNotTCP(t *testing.T) {
	mgr := newFakeManager()
	srv, _ := startServer(t, mgr)

	addr := srv.Addr()
	if addr == nil {
		t.Fatal("Addr() is nil after Serve")
	}
	if addr.Network() != "unix" {
		t.Fatalf("listener network = %q, want unix", addr.Network())
	}
}

// TestStaleSocketCleanup pre-creates a dead socket file at the path and confirms
// the server unlinks it and binds successfully.
func TestStaleSocketCleanup(t *testing.T) {
	sock := tempSocketPath(t)

	// A stale socket: a real unix listener whose owner has gone away. We create one
	// then close it without removing the file, leaving a dead socket node behind.
	dead, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("create stale socket: %v", err)
	}
	// Closing a *net.UnixListener removes its socket file, so detach the file first
	// by disabling unlink-on-close, leaving a stale node that no longer accepts.
	dead.SetUnlinkOnClose(false)
	_ = dead.Close()
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("stale socket file should exist: %v", err)
	}

	mgr := newFakeManager()
	srv := apiserver.New(mgr, testLogger(), config.Daemon{SocketPath: sock}, engine.PriorityNormal)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(context.Background()) }()
	waitListening(t, sock)
	t.Cleanup(func() { _ = srv.Close(); <-served })

	if r := roundTrip(t, sock, api.NewPingRequest()); !r.OK {
		t.Fatalf("server did not bind after stale-socket cleanup")
	}
}

// TestSingleInstanceRefused starts one server, then a second on the same path,
// and expects the second to refuse with ErrAlreadyRunning (its ping probe sees
// the first daemon answer).
func TestSingleInstanceRefused(t *testing.T) {
	mgr := newFakeManager()
	_, sock := startServer(t, mgr)

	second := apiserver.New(newFakeManager(), testLogger(), config.Daemon{SocketPath: sock}, engine.PriorityNormal)
	err := second.Serve(context.Background())
	if err == nil || err.Error() != apiserver.ErrAlreadyRunning.Error() {
		t.Fatalf("second Serve err = %v, want ErrAlreadyRunning", err)
	}

	// The refused instance must not delete the live daemon's socket on Close.
	_ = second.Close()
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("refused instance clobbered the live socket: %v", err)
	}
	if r := roundTrip(t, sock, api.NewPingRequest()); !r.OK {
		t.Fatalf("first daemon stopped answering after refusal")
	}
}

// TestGracefulShutdownNoLeaks confirms Close removes the socket file and leaks
// no goroutines. goleak is not a dependency, so we compare runtime.NumGoroutine
// before and after with a short settle loop.
func TestGracefulShutdownNoLeaks(t *testing.T) {
	before := runtime.NumGoroutine()

	mgr := newFakeManager()
	sock := tempSocketPath(t)
	srv := apiserver.New(mgr, testLogger(), config.Daemon{SocketPath: sock}, engine.PriorityNormal)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(context.Background()) }()
	waitListening(t, sock)

	// Drive a couple of requests so connection goroutines spin up and complete.
	if r := roundTrip(t, sock, api.NewPingRequest()); !r.OK {
		t.Fatal("ping failed")
	}
	if r := roundTrip(t, sock, api.NewListRequest()); !r.OK {
		t.Fatal("list failed")
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-served; err != nil {
		t.Fatalf("Serve returned %v, want nil", err)
	}

	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket file should be gone after shutdown, stat err = %v", err)
	}

	// Allow lingering goroutines to wind down before comparing.
	settled := false
	for i := 0; i < 50; i++ {
		if runtime.NumGoroutine() <= before {
			settled = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !settled {
		t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
	}
}

// TestShutdownTimeoutForceClosesConnections exercises the drain-timeout path: a
// client opens a connection but never sends a frame, so its handler parks in the
// read. With a tiny ShutdownTimeout, Close must hit the timeout, force-close the
// open connection, and still return having drained — leaving no goroutine alive.
// This is the regression guard for the prior leak where the drain monitor
// goroutine survived Close when the timeout fired.
func TestShutdownTimeoutForceClosesConnections(t *testing.T) {
	before := runtime.NumGoroutine()

	mgr := newFakeManager()
	sock := tempSocketPath(t)
	cfg := config.Daemon{
		SocketPath:      sock,
		ShutdownTimeout: config.Duration(50 * time.Millisecond),
	}
	srv := apiserver.New(mgr, testLogger(), cfg, engine.PriorityNormal)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(context.Background()) }()
	waitListening(t, sock)

	// Open a connection but send nothing: the handler blocks reading the frame
	// until its ReadTimeout, which outlasts our short ShutdownTimeout.
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	// Give the server a moment to accept and enter the handler's read.
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close blocked too long (%v): handler was not force-closed", elapsed)
	}
	if err := <-served; err != nil {
		t.Fatalf("Serve returned %v, want nil", err)
	}

	settled := false
	for i := 0; i < 100; i++ {
		if runtime.NumGoroutine() <= before {
			settled = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !settled {
		t.Fatalf("goroutine leak after timeout shutdown: before=%d after=%d", before, runtime.NumGoroutine())
	}
}
