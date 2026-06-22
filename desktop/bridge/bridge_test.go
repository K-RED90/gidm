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
	// A channel carries the decoded request out of the daemon goroutine so the
	// assertion is synchronized (clean under -race) rather than racing a field.
	reqs := make(chan api.Request, 1)
	b := newBridge(t, func(r api.Request) api.Response {
		reqs <- r
		return api.AddResponse("abc")
	})
	id, err := b.Add("https://x/y", "/srv/dl", "movie.mkv", 8, api.PriorityHigh)
	if err != nil || id != "abc" {
		t.Fatalf("Add = (%q, %v), want (abc, nil)", id, err)
	}
	got := <-reqs
	if got.Op != api.OpAdd || got.Add == nil {
		t.Fatalf("request op = %q, add = %+v", got.Op, got.Add)
	}
	if got.Add.URL != "https://x/y" || got.Add.Dir != "/srv/dl" ||
		got.Add.Filename != "movie.mkv" || got.Add.Segments != 8 || got.Add.Priority != api.PriorityHigh {
		t.Errorf("forwarded add = %+v, want url/dir/filename/segments/priority all set", got.Add)
	}
}

// TestBridgeAddQuick guards the quick-add path: zero overrides reproduce the
// bare-URL form (empty dir/filename, no segment override, default priority).
func TestBridgeAddQuick(t *testing.T) {
	reqs := make(chan api.Request, 1)
	b := newBridge(t, func(r api.Request) api.Response {
		reqs <- r
		return api.AddResponse("q1")
	})
	if _, err := b.Add("https://x/y", "", "", 0, ""); err != nil {
		t.Fatalf("Add (quick): %v", err)
	}
	got := <-reqs
	if got.Add == nil || got.Add.URL != "https://x/y" ||
		got.Add.Dir != "" || got.Add.Filename != "" || got.Add.Segments != 0 || got.Add.Priority != "" {
		t.Errorf("quick add = %+v, want only URL set", got.Add)
	}
}

func TestBridgeSetPriority(t *testing.T) {
	reqs := make(chan api.Request, 1)
	b := newBridge(t, func(r api.Request) api.Response {
		reqs <- r
		return api.OKResponse()
	})
	if err := b.SetPriority("d1", api.PriorityLow); err != nil {
		t.Fatalf("SetPriority: %v", err)
	}
	got := <-reqs
	if got.Op != api.OpSetPriority || got.SetPriority == nil ||
		got.SetPriority.ID != "d1" || got.SetPriority.Priority != api.PriorityLow {
		t.Errorf("forwarded set-priority = %+v (op %q), want {d1 low}", got.SetPriority, got.Op)
	}
}

func TestBridgeSetRate(t *testing.T) {
	reqs := make(chan api.Request, 1)
	b := newBridge(t, func(r api.Request) api.Response {
		reqs <- r
		return api.OKResponse()
	})
	if err := b.SetRate("d1", 1<<20); err != nil {
		t.Fatalf("SetRate: %v", err)
	}
	got := <-reqs
	if got.Op != api.OpSetRate || got.SetRate == nil || got.SetRate.ID != "d1" || got.SetRate.MaxRate != 1<<20 {
		t.Errorf("forwarded set-rate = %+v (op %q), want {d1 1MiB}", got.SetRate, got.Op)
	}
}

func TestBridgeGetConfig(t *testing.T) {
	want := api.ConfigView{
		DownloadDir:         "/srv/dl",
		SegmentsPerDownload: 8,
		DefaultPriority:     api.PriorityHigh,
		MaxRate:             1 << 20,
		PerDownloadMaxRate:  512 << 10,
	}
	b := newBridge(t, func(r api.Request) api.Response {
		if r.Op != api.OpGetConfig {
			t.Errorf("op = %q, want get-config", r.Op)
		}
		return api.ConfigResponse(want)
	})
	got, err := b.GetConfig()
	if err != nil || got != want {
		t.Fatalf("GetConfig = (%+v, %v), want %+v", got, err, want)
	}
}

func TestBridgeSetConfig(t *testing.T) {
	reqs := make(chan api.Request, 1)
	echoed := api.ConfigView{DownloadDir: "/new", SegmentsPerDownload: 6, DefaultPriority: api.PriorityNormal, MaxRate: 2 << 20}
	b := newBridge(t, func(r api.Request) api.Response {
		reqs <- r
		return api.ConfigResponse(echoed)
	})
	got, err := b.SetConfig("/new", 6, api.PriorityNormal, 2<<20, 0)
	if err != nil || got != echoed {
		t.Fatalf("SetConfig = (%+v, %v), want %+v", got, err, echoed)
	}
	req := <-reqs
	if req.Op != api.OpSetConfig || req.SetConfig == nil {
		t.Fatalf("op = %q, set_config = %+v", req.Op, req.SetConfig)
	}
	sc := req.SetConfig
	// The desktop sends a fully-populated form: every field is a non-nil pointer.
	if sc.DownloadDir == nil || *sc.DownloadDir != "/new" || sc.SegmentsPerDownload == nil || *sc.SegmentsPerDownload != 6 ||
		sc.DefaultPriority == nil || *sc.DefaultPriority != api.PriorityNormal ||
		sc.MaxRate == nil || *sc.MaxRate != 2<<20 || sc.PerDownloadMaxRate == nil || *sc.PerDownloadMaxRate != 0 {
		t.Errorf("forwarded set-config = %+v, want all fields populated", sc)
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
