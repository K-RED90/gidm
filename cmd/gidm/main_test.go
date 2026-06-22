package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/K-RED90/gidm/api"
	cli "github.com/K-RED90/gidm/cmd/gidm"
	"github.com/K-RED90/gidm/internal/apiserver"
	"github.com/K-RED90/gidm/internal/config"
	"github.com/K-RED90/gidm/internal/engine"
	"github.com/K-RED90/gidm/internal/httpfetch"
	"github.com/K-RED90/gidm/internal/httpx"
	"github.com/K-RED90/gidm/internal/store/sqlite"
)

// testHarness owns a real in-process gidmd: an httptest range-file server, the
// production httpx -> httpfetch -> sqlite -> engine -> Manager -> apiserver chain
// wired to a unix socket. Tests drive the CLI's exported Run seam against it. The
// CLI itself imports only api + internal/config (asserted by
// deps_test.go); the heavy stack lives only here in the external test package.
type testHarness struct {
	sock    string
	fileURL string
	content []byte
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()

	content := bytes.Repeat([]byte("gidm-cli-"), 50000) // ~450 KiB, range-served
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Disposition", `attachment; filename="file.bin"`)
		http.ServeContent(w, r, "file.bin", time.Time{}, bytes.NewReader(content))
	}))
	t.Cleanup(httpSrv.Close)

	dl := config.Download{
		MaxConcurrent:       2,
		SegmentsPerDownload: 4,
		BufferSize:          32 * 1024,
		Timeout:             config.Duration(30 * time.Second),
		MaxRetries:          2,
		RetryBackoff:        config.Duration(10 * time.Millisecond),
	}
	client, err := httpx.New(config.Network{}, dl, httpx.WithRoundTripper(httpSrv.Client().Transport))
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	store, err := sqlite.New(filepath.Join(t.TempDir(), "gidm.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.Config{Download: dl, Paths: config.Paths{DownloadDir: t.TempDir()}}
	e := engine.New(cfg, httpfetch.New(client), store)
	mgr := engine.NewManager(e, store, dl.MaxConcurrent)
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("manager start: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })

	sock := shortSocketPath(t)
	srv := apiserver.New(mgr, nil, config.Daemon{SocketPath: sock})
	served := make(chan error, 1)
	go func() { served <- srv.Serve(context.Background()) }()
	waitListening(t, sock)
	t.Cleanup(func() { _ = srv.Close(); <-served })

	return &testHarness{sock: sock, fileURL: httpSrv.URL + "/file.bin", content: content}
}

// newSlowHarness is like newHarness but the file server drip-feeds the body so a
// download stays active long enough for a deterministic pause/resume, rather than
// racing to completion. It supports range requests so the engine still segments.
func newSlowHarness(t *testing.T) *testHarness {
	t.Helper()

	content := bytes.Repeat([]byte("gidm-slow-"), 50000) // ~500 KiB
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Disposition", `attachment; filename="file.bin"`)
		// Honor a range so each segment gets its slice; drip the slice with a small
		// per-chunk delay so the transfer stays in-flight while we pause it.
		start, end := 0, len(content)-1
		if hdr := r.Header.Get("Range"); strings.HasPrefix(hdr, "bytes=") {
			start, end = parseRange(hdr, len(content))
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(content)))
			w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
			w.WriteHeader(http.StatusPartialContent)
		}
		flusher, _ := w.(http.Flusher)
		buf := content[start : end+1]
		const chunk = 4096
		for off := 0; off < len(buf); off += chunk {
			hi := min(off+chunk, len(buf))
			if _, err := w.Write(buf[off:hi]); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(15 * time.Millisecond)
		}
	}))
	t.Cleanup(httpSrv.Close)

	dl := config.Download{
		MaxConcurrent:       2,
		SegmentsPerDownload: 4,
		BufferSize:          32 * 1024,
		Timeout:             config.Duration(30 * time.Second),
		MaxRetries:          2,
		RetryBackoff:        config.Duration(10 * time.Millisecond),
	}
	client, err := httpx.New(config.Network{}, dl, httpx.WithRoundTripper(httpSrv.Client().Transport))
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	store, err := sqlite.New(filepath.Join(t.TempDir(), "gidm.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.Config{Download: dl, Paths: config.Paths{DownloadDir: t.TempDir()}}
	e := engine.New(cfg, httpfetch.New(client), store)
	mgr := engine.NewManager(e, store, dl.MaxConcurrent)
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("manager start: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })

	sock := shortSocketPath(t)
	srv := apiserver.New(mgr, nil, config.Daemon{SocketPath: sock})
	served := make(chan error, 1)
	go func() { served <- srv.Serve(context.Background()) }()
	waitListening(t, sock)
	t.Cleanup(func() { _ = srv.Close(); <-served })

	return &testHarness{sock: sock, fileURL: httpSrv.URL + "/file.bin", content: content}
}

// parseRange is a minimal "bytes=start-end" parser for the slow test server.
func parseRange(hdr string, size int) (start, end int) {
	end = size - 1
	spec := strings.TrimPrefix(hdr, "bytes=")
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, end
	}
	if s := spec[:dash]; s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			start = v
		}
	}
	if e := spec[dash+1:]; e != "" {
		if v, err := strconv.Atoi(e); err == nil {
			end = v
		}
	}
	return start, end
}

// waitForStatus polls status until the rendered output reports want, returning
// false on timeout.
func (h *testHarness) waitForStatus(t *testing.T, id string, want api.DownloadStatus) bool {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, stdout, _ := h.runCLI("status", id)
		if strings.Contains(stdout, string(want)) {
			return true
		}
		time.Sleep(15 * time.Millisecond)
	}
	return false
}

// runCLI drives the CLI's exported entry seam with --socket bound to the harness,
// capturing stdout and stderr.
func (h *testHarness) runCLI(args ...string) (code int, stdout, stderr string) {
	full := append([]string{"--socket", h.sock}, args...)
	var out, errBuf bytes.Buffer
	code = cli.Run(full, &out, &errBuf)
	return code, out.String(), errBuf.String()
}

// shortSocketPath returns an owner-only socket path short enough to stay under
// the ~104-byte AF_UNIX sun_path limit on macOS (t.TempDir() can exceed it).
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gc")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

func decodeJSON(s string, v any) error {
	return json.Unmarshal([]byte(strings.TrimSpace(s)), v)
}

func waitListening(t *testing.T, sock string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", sock, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("daemon did not start listening on %s", sock)
}

// addOne queues a download and returns its id, asserting the human add output is
// exactly the id.
func (h *testHarness) addOne(t *testing.T) string {
	t.Helper()
	code, stdout, stderr := h.runCLI("add", h.fileURL)
	if code != 0 {
		t.Fatalf("add exit = %d, stderr=%q", code, stderr)
	}
	id := strings.TrimSpace(stdout)
	if id == "" {
		t.Fatalf("add printed no id; stdout=%q", stdout)
	}
	return id
}

func TestAddHumanAndJSON(t *testing.T) {
	h := newHarness(t)

	id := h.addOne(t)

	// --json variant returns the AddResult with the same id field present.
	code, stdout, stderr := h.runCLI("--json", "add", h.fileURL)
	if code != 0 {
		t.Fatalf("add --json exit = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"id"`) {
		t.Errorf("add --json output missing id field: %q", stdout)
	}
	if id == "" {
		t.Fatal("expected a non-empty id from human add")
	}
}

func TestListHumanAndJSON(t *testing.T) {
	h := newHarness(t)
	id := h.addOne(t)

	code, stdout, stderr := h.runCLI("list")
	if code != 0 {
		t.Fatalf("list exit = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "ID") || !strings.Contains(stdout, "STATUS") || !strings.Contains(stdout, "DESTINATION") {
		t.Errorf("list table missing header columns: %q", stdout)
	}
	if !strings.Contains(stdout, id) {
		t.Errorf("list does not contain added id %q: %q", id, stdout)
	}

	code, stdout, stderr = h.runCLI("--json", "list")
	if code != 0 {
		t.Fatalf("list --json exit = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"downloads"`) || !strings.Contains(stdout, id) {
		t.Errorf("list --json missing downloads/id: %q", stdout)
	}
}

func TestStatusHumanJSONAndAlias(t *testing.T) {
	h := newHarness(t)
	id := h.addOne(t)

	for _, cmd := range []string{"status", "get"} {
		code, stdout, stderr := h.runCLI(cmd, id)
		if code != 0 {
			t.Fatalf("%s exit = %d, stderr=%q", cmd, code, stderr)
		}
		if !strings.Contains(stdout, id) {
			t.Errorf("%s output missing id: %q", cmd, stdout)
		}
	}

	code, stdout, stderr := h.runCLI("--json", "status", id)
	if code != 0 {
		t.Fatalf("status --json exit = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"download"`) || !strings.Contains(stdout, id) {
		t.Errorf("status --json missing download/id: %q", stdout)
	}
}

func TestStatusUnknownIDNotFound(t *testing.T) {
	h := newHarness(t)
	code, _, stderr := h.runCLI("status", "does-not-exist")
	if code != 5 {
		t.Fatalf("status unknown exit = %d, want 5; stderr=%q", code, stderr)
	}
	if stderr == "" {
		t.Error("expected an error message on stderr for unknown id")
	}
}

func TestPauseResumeRmHumanAndJSON(t *testing.T) {
	// A slow server keeps the download in-flight so pause lands on an active
	// transfer and resume sees a settled (paused) state — deterministic, no race
	// against an instant completion.
	t.Run("human acks", func(t *testing.T) {
		h := newSlowHarness(t)
		id := h.addOne(t)

		code, stdout, stderr := h.runCLI("pause", id)
		if code != 0 {
			t.Fatalf("pause exit = %d, stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "paused") || !strings.Contains(stdout, id) {
			t.Errorf("pause ack = %q, want paused + id", stdout)
		}
		if !h.waitForStatus(t, id, api.StatusPaused) {
			t.Fatal("download never reached paused")
		}

		code, stdout, stderr = h.runCLI("resume", id)
		if code != 0 {
			t.Fatalf("resume exit = %d, stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "resumed") || !strings.Contains(stdout, id) {
			t.Errorf("resume ack = %q, want resumed + id", stdout)
		}

		code, stdout, stderr = h.runCLI("rm", id)
		if code != 0 {
			t.Fatalf("rm exit = %d, stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "removed") || !strings.Contains(stdout, id) {
			t.Errorf("rm ack = %q, want removed + id", stdout)
		}
	})

	t.Run("restart ack", func(t *testing.T) {
		// Pause first so restart lands on a settled download deterministically; the
		// ack word and id come from the request since the response carries no body.
		h := newSlowHarness(t)
		id := h.addOne(t)

		code, _, stderr := h.runCLI("pause", id)
		if code != 0 {
			t.Fatalf("pause exit = %d, stderr=%q", code, stderr)
		}
		if !h.waitForStatus(t, id, api.StatusPaused) {
			t.Fatal("download never reached paused")
		}

		code, stdout, stderr := h.runCLI("restart", id)
		if code != 0 {
			t.Fatalf("restart exit = %d, stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "restarting") || !strings.Contains(stdout, id) {
			t.Errorf("restart ack = %q, want restarting + id", stdout)
		}
	})

	t.Run("json acks", func(t *testing.T) {
		h := newSlowHarness(t)
		id := h.addOne(t)

		code, stdout, stderr := h.runCLI("--json", "pause", id)
		if code != 0 || !strings.Contains(stdout, `"ok":true`) {
			t.Fatalf("pause --json exit=%d out=%q stderr=%q", code, stdout, stderr)
		}
		if !h.waitForStatus(t, id, api.StatusPaused) {
			t.Fatal("download never reached paused")
		}
		for _, cmd := range []string{"resume", "rm"} {
			code, stdout, stderr := h.runCLI("--json", cmd, id)
			if code != 0 {
				t.Fatalf("%s --json exit = %d, stderr=%q", cmd, code, stderr)
			}
			if !strings.Contains(stdout, `"ok":true`) {
				t.Errorf("%s --json = %q, want ok:true", cmd, stdout)
			}
		}
	})
}

// TestPriorityCLI exercises the --priority flag on add and the set-priority
// subcommand end to end, asserting priority is surfaced in status/list and updated
// by set-priority, and that an invalid level is rejected as a bad request.
func TestPriorityCLI(t *testing.T) {
	h := newHarness(t)

	code, stdout, stderr := h.runCLI("add", "--priority", "high", h.fileURL)
	if code != 0 {
		t.Fatalf("add --priority exit = %d, stderr=%q", code, stderr)
	}
	id := strings.TrimSpace(stdout)
	if id == "" {
		t.Fatalf("add printed no id; stdout=%q", stdout)
	}

	// Human status shows the Priority line; JSON status carries the typed value.
	_, statusOut, _ := h.runCLI("status", id)
	if !strings.Contains(statusOut, "Priority") || !strings.Contains(statusOut, "high") {
		t.Errorf("status missing Priority/high: %q", statusOut)
	}
	_, jsonOut, _ := h.runCLI("--json", "status", id)
	var sr api.StatusResult
	if err := decodeJSON(jsonOut, &sr); err != nil {
		t.Fatalf("decode status json: %v (%q)", err, jsonOut)
	}
	if sr.Download.Priority != api.PriorityHigh {
		t.Errorf("status json priority = %q, want high", sr.Download.Priority)
	}

	// The list table gained a PRIORITY column.
	_, listOut, _ := h.runCLI("list")
	if !strings.Contains(listOut, "PRIORITY") || !strings.Contains(listOut, "high") {
		t.Errorf("list missing PRIORITY column/value: %q", listOut)
	}

	// set-priority low -> ack, then status reflects it.
	code, stdout, stderr = h.runCLI("set-priority", id, "low")
	if code != 0 {
		t.Fatalf("set-priority exit = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "priority set") || !strings.Contains(stdout, id) {
		t.Errorf("set-priority ack = %q, want 'priority set' + id", stdout)
	}
	if _, out, _ := h.runCLI("status", id); !strings.Contains(out, "low") {
		t.Errorf("status after set-priority = %q, want low", out)
	}

	// --json set-priority is a bare ok ack.
	if code, out, _ := h.runCLI("--json", "set-priority", id, "normal"); code != 0 || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("set-priority --json exit=%d out=%q", code, out)
	}

	// An invalid level is rejected by the daemon as a bad request (exit 2).
	if code, _, _ := h.runCLI("set-priority", id, "urgent"); code != 2 {
		t.Errorf("set-priority urgent exit = %d, want 2 (bad request)", code)
	}
}

func TestArityErrors(t *testing.T) {
	h := newHarness(t)
	cases := [][]string{
		{"add"},                  // missing url
		{"status"},               // missing id
		{"list", "extra"},        // list takes none
		{"set-priority"},         // missing id and level
		{"set-priority", "only"}, // missing level
		{"restart"},              // missing id
		{"bogus"},                // unknown command
	}
	for _, args := range cases {
		code, _, stderr := h.runCLI(args...)
		if code != 2 {
			t.Errorf("%v exit = %d, want 2 (bad request); stderr=%q", args, code, stderr)
		}
	}
}

func TestDaemonNotRunning(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "nope.sock")
	var out, errBuf bytes.Buffer
	code := cli.Run([]string{"--socket", absent, "list"}, &out, &errBuf)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (daemon not running); stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "gidmd") {
		t.Errorf("stderr missing 'start gidmd' hint: %q", errBuf.String())
	}
}

// TestTimeoutAgainstUnresponsiveDaemon points the CLI at a listener that accepts
// connections but never replies, and asserts the configured timeout aborts the
// call promptly with exit 4 (never wedging).
func TestTimeoutAgainstUnresponsiveDaemon(t *testing.T) {
	sock := shortSocketPath(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	// Accept and hold connections open without ever responding.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	done := make(chan int, 1)
	var errBuf bytes.Buffer
	go func() {
		var out bytes.Buffer
		done <- cli.Run([]string{"--socket", sock, "--timeout", "150ms", "list"}, &out, &errBuf)
	}()

	select {
	case code := <-done:
		if code != 4 {
			t.Fatalf("exit = %d, want 4 (timeout); stderr=%q", code, errBuf.String())
		}
		if !strings.Contains(strings.ToLower(errBuf.String()), "timed out") {
			t.Errorf("stderr missing timeout message: %q", errBuf.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CLI wedged on an unresponsive daemon; timeout did not fire")
	}
}

// TestEndToEndCompletes adds a real range download and polls list/status until the
// rendered output shows it completed with the correct size.
func TestEndToEndCompletes(t *testing.T) {
	h := newHarness(t)
	id := h.addOne(t)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		code, stdout, stderr := h.runCLI("status", id)
		if code != 0 {
			t.Fatalf("status exit = %d, stderr=%q", code, stderr)
		}
		if strings.Contains(stdout, string(api.StatusCompleted)) {
			// list must also reflect the completed download.
			_, listOut, _ := h.runCLI("list")
			if !strings.Contains(listOut, id) || !strings.Contains(listOut, string(api.StatusCompleted)) {
				t.Fatalf("list does not show completed download: %q", listOut)
			}
			// --json status carries the exact downloaded/total sizes.
			_, jsonOut, _ := h.runCLI("--json", "status", id)
			var resp api.StatusResult
			if err := decodeJSON(jsonOut, &resp); err != nil {
				t.Fatalf("decode status json: %v (%q)", err, jsonOut)
			}
			if resp.Download.TotalSize != int64(len(h.content)) {
				t.Fatalf("total size = %d, want %d", resp.Download.TotalSize, len(h.content))
			}
			if resp.Download.Downloaded != int64(len(h.content)) {
				t.Fatalf("downloaded = %d, want %d", resp.Download.Downloaded, len(h.content))
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("download did not reach completed within deadline")
}
