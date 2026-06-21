package apiserver_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/api"
	"github.com/K-RED90/gidm/internal/apiserver"
	"github.com/K-RED90/gidm/internal/config"
	"github.com/K-RED90/gidm/internal/engine"
)

// The production Manager must satisfy the server's facade. Asserting it here (in
// the apiserver test) rather than in package engine keeps the engine free of any
// reference to apiserver, preserving its import-clean invariant.
var _ apiserver.Manager = (*engine.Manager)(nil)

// fakeManager is an in-memory Manager facade for fast, deterministic verb and
// error-code coverage without the engine/store/http stack.
type fakeManager struct {
	mu        sync.Mutex
	downloads map[string]*engine.Download
	nextID    int

	submitErr      error
	getErr         error
	listErr        error
	pauseErr       error
	resumeErr      error
	cancelErr      error
	setPriorityErr error
}

func newFakeManager() *fakeManager {
	return &fakeManager{downloads: make(map[string]*engine.Download)}
}

func (f *fakeManager) Submit(_ context.Context, url string, priority engine.Priority) (string, error) {
	if f.submitErr != nil {
		return "", f.submitErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := "id-" + string(rune('a'+f.nextID-1))
	f.downloads[id] = &engine.Download{ID: id, URL: url, Status: engine.StatusQueued, Priority: priority}
	return id, nil
}

func (f *fakeManager) List(context.Context) ([]*engine.Download, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*engine.Download, 0, len(f.downloads))
	for _, d := range f.downloads {
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeManager) Get(_ context.Context, id string) (*engine.Download, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.downloads[id]
	if !ok {
		return nil, engine.ErrNotFound
	}
	return d, nil
}

func (f *fakeManager) setStatus(id string, s engine.Status, override error) error {
	if override != nil {
		return override
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.downloads[id]
	if !ok {
		return engine.ErrNotFound
	}
	d.Status = s
	return nil
}

func (f *fakeManager) Pause(_ context.Context, id string) error {
	return f.setStatus(id, engine.StatusPaused, f.pauseErr)
}

func (f *fakeManager) Resume(_ context.Context, id string) error {
	return f.setStatus(id, engine.StatusQueued, f.resumeErr)
}

func (f *fakeManager) Cancel(_ context.Context, id string) error {
	return f.setStatus(id, engine.StatusCanceled, f.cancelErr)
}

func (f *fakeManager) SetPriority(_ context.Context, id string, p engine.Priority) error {
	if f.setPriorityErr != nil {
		return f.setPriorityErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.downloads[id]
	if !ok {
		return engine.ErrNotFound
	}
	d.Priority = p
	return nil
}

// startServer spins up a real Server on a socket in t.TempDir and returns the
// socket path. It blocks until the listener is bound so tests never race the
// Accept loop.
func startServer(t *testing.T, mgr apiserver.Manager) (*apiserver.Server, string) {
	t.Helper()
	sock := tempSocketPath(t)
	cfg := config.Daemon{SocketPath: sock} // zero timeouts: New must floor them
	srv := apiserver.New(mgr, testLogger(), cfg, engine.PriorityNormal)

	served := make(chan error, 1)
	go func() { served <- srv.Serve(context.Background()) }()
	waitListeningOrServeErr(t, sock, served)
	t.Cleanup(func() {
		_ = srv.Close()
		<-served
	})
	return srv, sock
}

func waitListening(t *testing.T, sock string) {
	t.Helper()
	waitListeningOrServeErr(t, sock, nil)
}

// tempSocketPath returns a short, owner-only socket path. It deliberately avoids
// t.TempDir(), whose long, test-name-derived paths can exceed the ~104-byte
// AF_UNIX sun_path limit on macOS and fail bind with "invalid argument".
func tempSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gs")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

// waitListeningOrServeErr blocks until the socket accepts a connection, fails
// fast if Serve returned an error (surfacing the real bind failure), or times
// out. served may be nil when the caller has no channel to watch.
func waitListeningOrServeErr(t *testing.T, sock string, served <-chan error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if served != nil {
			select {
			case err := <-served:
				t.Fatalf("Serve returned before listening: %v", err)
			default:
			}
		}
		conn, err := net.DialTimeout("unix", sock, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("server did not start listening on %s", sock)
}

// roundTrip dials the socket, sends one request, and decodes one response. One
// request per connection matches the server's model.
func roundTrip(t *testing.T, sock string, req api.Request) api.Response {
	t.Helper()
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := api.WriteMessage(conn, req); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp api.Response
	if err := api.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestRoundTripAllVerbs(t *testing.T) {
	mgr := newFakeManager()
	_, sock := startServer(t, mgr)

	// add -> id
	addResp := roundTrip(t, sock, api.NewAddRequest("https://example.com/file.bin"))
	if !addResp.OK || addResp.Add == nil || addResp.Add.ID == "" {
		t.Fatalf("add: got %+v", addResp)
	}
	id := addResp.Add.ID

	// list reflects the submitted state
	listResp := roundTrip(t, sock, api.NewListRequest())
	if !listResp.OK || listResp.List == nil || len(listResp.List.Downloads) != 1 {
		t.Fatalf("list: got %+v", listResp)
	}
	if got := listResp.List.Downloads[0]; got.ID != id || got.Status != api.StatusQueued {
		t.Fatalf("list view = %+v, want id %q queued", got, id)
	}

	// status returns the download
	statusResp := roundTrip(t, sock, api.NewStatusRequest(id))
	if !statusResp.OK || statusResp.Status == nil || statusResp.Status.Download.ID != id {
		t.Fatalf("status: got %+v", statusResp)
	}

	// pause -> ok, status reflects paused
	if r := roundTrip(t, sock, api.NewPauseRequest(id)); !r.OK {
		t.Fatalf("pause: got %+v", r)
	}
	if r := roundTrip(t, sock, api.NewStatusRequest(id)); r.Status.Download.Status != api.StatusPaused {
		t.Fatalf("after pause status = %q, want paused", r.Status.Download.Status)
	}

	// resume -> ok, status reflects queued
	if r := roundTrip(t, sock, api.NewResumeRequest(id)); !r.OK {
		t.Fatalf("resume: got %+v", r)
	}
	if r := roundTrip(t, sock, api.NewStatusRequest(id)); r.Status.Download.Status != api.StatusQueued {
		t.Fatalf("after resume status = %q, want queued", r.Status.Download.Status)
	}

	// rm -> Cancel; engine canceled maps to api paused on the wire
	if r := roundTrip(t, sock, api.NewRmRequest(id)); !r.OK {
		t.Fatalf("rm: got %+v", r)
	}
	if r := roundTrip(t, sock, api.NewStatusRequest(id)); r.Status.Download.Status != api.StatusPaused {
		t.Fatalf("after rm status = %q, want paused (canceled->paused drift)", r.Status.Download.Status)
	}

	// ping -> version
	pingResp := roundTrip(t, sock, api.NewPingRequest())
	if !pingResp.OK || pingResp.Ping == nil || pingResp.Ping.Version != api.Version {
		t.Fatalf("ping: got %+v", pingResp)
	}
}

func TestErrorCodes(t *testing.T) {
	mgr := newFakeManager()
	_, sock := startServer(t, mgr)

	tests := []struct {
		name string
		req  api.Request
		code api.Code
	}{
		{"bad url", api.NewAddRequest("not-a-url"), api.CodeBadRequest},
		{"empty id status", api.NewStatusRequest("   "), api.CodeBadRequest},
		{"not found status", api.NewStatusRequest("missing"), api.CodeNotFound},
		{"not found pause", api.NewPauseRequest("missing"), api.CodeNotFound},
		{"unknown op", api.Request{Version: api.Version, Op: "frobnicate"}, api.CodeBadRequest},
		{"bad version", api.Request{Version: 999, Op: api.OpPing}, api.CodeUnsupportedVersion},
		{"missing add payload", api.Request{Version: api.Version, Op: api.OpAdd}, api.CodeBadRequest},
		{"set-priority bad value", api.NewSetPriorityRequest("d1", "urgent"), api.CodeBadRequest},
		{"set-priority empty id", api.NewSetPriorityRequest("  ", api.PriorityHigh), api.CodeBadRequest},
		{"set-priority missing payload", api.Request{Version: api.Version, Op: api.OpSetPriority}, api.CodeBadRequest},
		{"set-priority not found", api.NewSetPriorityRequest("missing", api.PriorityHigh), api.CodeNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := roundTrip(t, sock, tc.req)
			if resp.OK || resp.Error == nil {
				t.Fatalf("got OK response, want error: %+v", resp)
			}
			if resp.Error.Code != tc.code {
				t.Fatalf("code = %q, want %q (msg %q)", resp.Error.Code, tc.code, resp.Error.Message)
			}
		})
	}
}

// TestPriorityEndToEnd drives add-with-priority and set-priority over the wire and
// asserts the priority is carried on add, reflected in the status/list views, and
// updated by set-priority. An add that omits a priority shows the server default.
func TestPriorityEndToEnd(t *testing.T) {
	mgr := newFakeManager()
	_, sock := startServer(t, mgr) // default priority is PriorityNormal

	// add high -> status view shows high
	addResp := roundTrip(t, sock, api.NewAddRequestWithPriority("https://example.com/f.bin", api.PriorityHigh))
	if !addResp.OK || addResp.Add == nil {
		t.Fatalf("add: %+v", addResp)
	}
	id := addResp.Add.ID
	if got := roundTrip(t, sock, api.NewStatusRequest(id)); got.Status.Download.Priority != api.PriorityHigh {
		t.Fatalf("after add, priority = %q, want high", got.Status.Download.Priority)
	}

	// set-priority low -> view shows low
	if r := roundTrip(t, sock, api.NewSetPriorityRequest(id, api.PriorityLow)); !r.OK {
		t.Fatalf("set-priority: %+v", r)
	}
	if got := roundTrip(t, sock, api.NewStatusRequest(id)); got.Status.Download.Priority != api.PriorityLow {
		t.Fatalf("after set-priority, priority = %q, want low", got.Status.Download.Priority)
	}

	// add WITHOUT a priority -> the server applies its configured default (normal).
	plain := roundTrip(t, sock, api.NewAddRequest("https://example.com/g.bin"))
	if got := roundTrip(t, sock, api.NewStatusRequest(plain.Add.ID)); got.Status.Download.Priority != api.PriorityNormal {
		t.Fatalf("default-priority add = %q, want normal", got.Status.Download.Priority)
	}
}

// TestServerAppliesConfiguredDefaultPriority constructs a server whose default is
// high and asserts an add that omits a priority is stored at that default — proving
// the default is config-driven, not hardcoded to normal.
func TestServerAppliesConfiguredDefaultPriority(t *testing.T) {
	mgr := newFakeManager()
	sock := tempSocketPath(t)
	srv := apiserver.New(mgr, testLogger(), config.Daemon{SocketPath: sock}, engine.PriorityHigh)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(context.Background()) }()
	waitListeningOrServeErr(t, sock, served)
	t.Cleanup(func() { _ = srv.Close(); <-served })

	resp := roundTrip(t, sock, api.NewAddRequest("https://example.com/f.bin"))
	if !resp.OK || resp.Add == nil {
		t.Fatalf("add: %+v", resp)
	}
	if got := roundTrip(t, sock, api.NewStatusRequest(resp.Add.ID)); got.Status.Download.Priority != api.PriorityHigh {
		t.Fatalf("add without priority = %q, want high (configured default)", got.Status.Download.Priority)
	}
}

// TestInternalErrorIsGeneric proves an engine error string never leaks to the
// client: a Submit failure surfaces only as a generic internal message.
func TestInternalErrorIsGeneric(t *testing.T) {
	mgr := newFakeManager()
	mgr.submitErr = context.DeadlineExceeded // stand-in for any non-sentinel error
	_, sock := startServer(t, mgr)

	resp := roundTrip(t, sock, api.NewAddRequest("https://example.com/x"))
	if resp.OK || resp.Error == nil {
		t.Fatalf("want error, got %+v", resp)
	}
	if resp.Error.Code != api.CodeInternal {
		t.Fatalf("code = %q, want internal", resp.Error.Code)
	}
	if resp.Error.Message != "internal error" {
		t.Fatalf("message = %q, want generic 'internal error'", resp.Error.Message)
	}
}

// TestMalformedRequestsKeepServerAlive sends garbage and oversized frames and
// then proves the server still answers a well-formed request.
func TestMalformedRequestsKeepServerAlive(t *testing.T) {
	mgr := newFakeManager()
	_, sock := startServer(t, mgr)

	// Truncated JSON: open brace, no close, then half-close.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = conn.Write([]byte("{\"version\":1,\"op\":\"add\""))
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	var resp api.Response
	if err := api.NewDecoder(conn).Decode(&resp); err == nil {
		if resp.OK || resp.Error == nil || resp.Error.Code != api.CodeBadRequest {
			t.Fatalf("truncated frame: want bad_request, got %+v", resp)
		}
	}
	_ = conn.Close()

	// Server still serves the next connection.
	if r := roundTrip(t, sock, api.NewPingRequest()); !r.OK {
		t.Fatalf("server died after malformed input: %+v", r)
	}
}

// TestOversizedRequestRejected sends a frame larger than MaxRequestBytes and
// expects a clean bad_request, with the server staying up.
func TestOversizedRequestRejected(t *testing.T) {
	mgr := newFakeManager()
	sock := tempSocketPath(t)
	cfg := config.Daemon{SocketPath: sock, MaxRequestBytes: 64} // tiny cap
	srv := apiserver.New(mgr, testLogger(), cfg, engine.PriorityNormal)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(context.Background()) }()
	waitListening(t, sock)
	t.Cleanup(func() { _ = srv.Close(); <-served })

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	// A URL far longer than 64 bytes pushes the frame past the cap.
	long := "https://example.com/" + string(make([]byte, 4096))
	_ = api.WriteMessage(conn, api.NewAddRequest(long))
	var resp api.Response
	if err := api.NewDecoder(conn).Decode(&resp); err == nil {
		if resp.OK || resp.Error.Code != api.CodeBadRequest {
			t.Fatalf("oversized frame: want bad_request, got %+v", resp)
		}
	}
	_ = conn.Close()

	if r := roundTrip(t, sock, api.NewPingRequest()); !r.OK {
		t.Fatalf("server died after oversized input")
	}
}
