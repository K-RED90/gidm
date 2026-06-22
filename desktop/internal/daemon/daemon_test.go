package daemon_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/desktop/internal/daemon"
)

func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gd")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

// startFakePingDaemon answers a single ping per connection, like gidmd.
func startFakePingDaemon(t *testing.T, sock string) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				var req api.Request
				if err := api.NewDecoder(conn).Decode(&req); err != nil {
					return
				}
				_ = api.WriteMessage(conn, api.PingResponse())
			}()
		}
	}()
}

func TestPing(t *testing.T) {
	sock := shortSocketPath(t)
	if daemon.Ping(sock) {
		t.Fatal("Ping = true with no daemon, want false")
	}
	startFakePingDaemon(t, sock)
	if !daemon.Ping(sock) {
		t.Fatal("Ping = false against a live fake daemon, want true")
	}
}

// EnsureRunning must not spawn when a daemon already answers.
func TestEnsureRunningAlreadyUp(t *testing.T) {
	sock := shortSocketPath(t)
	startFakePingDaemon(t, sock)
	spawned, err := daemon.EnsureRunning(sock)
	if err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if spawned {
		t.Fatal("spawned a daemon although one was already running")
	}
}
