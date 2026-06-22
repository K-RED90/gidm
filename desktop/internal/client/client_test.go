package client_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/desktop/internal/client"
)

// shortSocketPath returns a socket path short enough to stay under the ~104-byte
// AF_UNIX sun_path limit on macOS (t.TempDir() can exceed it).
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gc")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

// startFakeDaemon answers each one-shot connection with handler(req), mimicking
// gidmd's one-request-per-connection model.
func startFakeDaemon(t *testing.T, handler func(api.Request) api.Response) string {
	t.Helper()
	sock := shortSocketPath(t)
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
				_ = api.WriteMessage(conn, handler(req))
			}()
		}
	}()
	return sock
}

func TestDoRoundTrip(t *testing.T) {
	sock := startFakeDaemon(t, func(r api.Request) api.Response {
		if r.Op != api.OpAdd || r.Add == nil {
			return api.ErrorResponse(api.CodeBadRequest, "want add")
		}
		return api.AddResponse("id-1")
	})
	resp, err := client.New(sock, time.Second).Do(context.Background(), api.NewAddRequest("https://x/y"))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !resp.OK || resp.Add == nil || resp.Add.ID != "id-1" {
		t.Fatalf("resp = %+v, want add id-1", resp)
	}
}

func TestDoDaemonNotRunning(t *testing.T) {
	c := client.New(filepath.Join(t.TempDir(), "absent.sock"), time.Second)
	if _, err := c.Do(context.Background(), api.NewPingRequest()); !errors.Is(err, client.ErrDaemonNotRunning) {
		t.Fatalf("err = %v, want ErrDaemonNotRunning", err)
	}
}

func TestDoTimeout(t *testing.T) {
	sock := shortSocketPath(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		<-done // accept but never reply, so the client's deadline fires
	}()
	c := client.New(sock, 100*time.Millisecond)
	if _, err := c.Do(context.Background(), api.NewListRequest()); !errors.Is(err, client.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}
