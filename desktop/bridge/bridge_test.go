package bridge_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/desktop/bridge"
	"github.com/K-RED90/gidm/desktop/internal/client"
)

func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gb")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

// newBridge wires a Bridge to a fake daemon that answers every connection with
// handler(req).
func newBridge(t *testing.T, handler func(api.Request) api.Response) *bridge.Bridge {
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
	return bridge.New(client.New(sock, time.Second))
}

func TestBridgeAdd(t *testing.T) {
	b := newBridge(t, func(api.Request) api.Response { return api.AddResponse("abc") })
	if id, err := b.Add("https://x/y"); err != nil || id != "abc" {
		t.Fatalf("Add = (%q, %v), want (abc, nil)", id, err)
	}
}

func TestBridgeListAndSnapshot(t *testing.T) {
	views := []api.DownloadView{{ID: "d1", Status: api.StatusActive, TotalSize: 100, Downloaded: 40, SpeedBps: 10, EtaSecs: 6}}
	b := newBridge(t, func(api.Request) api.Response { return api.ListResponse(views) })

	got, err := b.List()
	if err != nil || len(got) != 1 || got[0].ID != "d1" || got[0].SpeedBps != 10 {
		t.Fatalf("List = (%+v, %v)", got, err)
	}
	dl, reachable := b.Snapshot()
	if !reachable || len(dl) != 1 {
		t.Fatalf("Snapshot = (%+v, %v), want (1 row, true)", dl, reachable)
	}
}

func TestBridgeErrorMessageSurfaced(t *testing.T) {
	b := newBridge(t, func(api.Request) api.Response {
		return api.ErrorResponse(api.CodeNotFound, "download %q not found", "d9")
	})
	_, err := b.Status("d9")
	if err == nil || err.Error() != `download "d9" not found` {
		t.Fatalf("Status err = %v, want the daemon's not-found message", err)
	}
}

func TestBridgeHealth(t *testing.T) {
	up := newBridge(t, func(api.Request) api.Response { return api.PingResponse() })
	if !up.Health() {
		t.Error("Health = false against a live fake daemon, want true")
	}
	down := bridge.New(client.New(filepath.Join(t.TempDir(), "absent.sock"), time.Second))
	if down.Health() {
		t.Error("Health = true with no daemon, want false")
	}
}

func TestBridgeSnapshotUnreachable(t *testing.T) {
	b := bridge.New(client.New(filepath.Join(t.TempDir(), "absent.sock"), 200*time.Millisecond))
	if dl, reachable := b.Snapshot(); reachable || dl != nil {
		t.Fatalf("Snapshot = (%+v, %v), want (nil, false)", dl, reachable)
	}
}
