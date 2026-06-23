package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/K-RED90/gidm/api"
)

// fakeDaemon listens on a unix socket, accepts one connection, decodes a single
// api.Request, replies with AddResponse(id), and hands the decoded request back
// over got. It mimics the daemon's one-request-per-connection framing.
func fakeDaemon(t *testing.T, sock, id string) <-chan api.Request {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	got := make(chan api.Request, 1)
	go func() {
		defer func() { _ = ln.Close() }()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req api.Request
		if err := api.NewDecoder(conn).Decode(&req); err != nil {
			return
		}
		got <- req
		_ = api.WriteMessage(conn, api.AddResponse(id))
	}()
	return got
}

// shortSocketPath keeps the path under the ~104-byte AF_UNIX sun_path limit on
// macOS (t.TempDir() can exceed it).
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gh")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

func TestHandleForwardsCredentials(t *testing.T) {
	sock := shortSocketPath(t)
	got := fakeDaemon(t, sock, "x1")

	msg, _ := json.Marshal(captureRequest{
		URL:      "https://example.com/file.bin",
		Referrer: "https://example.com/page",
		Cookie:   "sid=abc",
	})
	reply := handle(msg, sock, time.Second)

	if !reply.OK || reply.ID != "x1" {
		t.Fatalf("reply = %+v, want OK with id x1", reply)
	}
	req := <-got
	if req.Op != api.OpAdd || req.Add == nil {
		t.Fatalf("forwarded op = %v, add = %v", req.Op, req.Add)
	}
	if req.Add.URL != "https://example.com/file.bin" {
		t.Errorf("url = %q", req.Add.URL)
	}
	if req.Add.Auth == nil || req.Add.Auth.Referer != "https://example.com/page" || req.Add.Auth.Cookie != "sid=abc" {
		t.Errorf("auth = %+v, want referer+cookie", req.Add.Auth)
	}
}

func TestHandleRejectsNonHTTP(t *testing.T) {
	// A bogus socket that nothing listens on: a reject must not dial it.
	msg, _ := json.Marshal(captureRequest{URL: "blob:https://example.com/uuid"})
	reply := handle(msg, "/nonexistent/should-not-dial.sock", time.Second)
	if reply.OK || reply.Error == "" {
		t.Fatalf("reply = %+v, want failure for blob url", reply)
	}
}

func TestRunFramesRoundTrip(t *testing.T) {
	sock := shortSocketPath(t)
	fakeDaemon(t, sock, "y2")

	body, _ := json.Marshal(captureRequest{URL: "https://example.com/a.zip"})
	var in bytes.Buffer
	var lenBuf [4]byte
	binary.NativeEndian.PutUint32(lenBuf[:], uint32(len(body)))
	in.Write(lenBuf[:])
	in.Write(body)

	var out bytes.Buffer
	// run loops until the input is drained; EOF after one frame is the clean exit.
	_ = run(&in, &out, sock, time.Second)

	n := binary.NativeEndian.Uint32(out.Bytes()[:4])
	var reply captureReply
	if err := json.Unmarshal(out.Bytes()[4:4+n], &reply); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if !reply.OK || reply.ID != "y2" {
		t.Fatalf("reply = %+v, want OK with id y2", reply)
	}
}
