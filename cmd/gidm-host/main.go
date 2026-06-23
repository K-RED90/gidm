// Command gidm-host is the Chrome native-messaging host. The browser launches it
// and exchanges length-prefixed JSON over stdio; the host translates each capture
// request from the extension into an api Add and forwards it to the running daemon
// over its Unix socket, then frames the result back. One message in, one out — the
// browser uses chrome.runtime.sendNativeMessage, which spawns the host per call.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/internal/config"
)

const maxMessageSize = 64 << 20 // Chrome's extension->host limit

// captureRequest is the message the extension sends. It is intentionally
// decoupled from the api package: the extension speaks this small shape, and the
// host maps it onto an api Add. Referer/Cookie carry the page context that makes
// hotlink- and session-gated downloads work. Ping is a reachability probe (no
// URL) the popup uses to tell "daemon down" apart from "host not installed".
type captureRequest struct {
	URL      string `json:"url,omitempty"`
	Referrer string `json:"referrer,omitempty"`
	Cookie   string `json:"cookie,omitempty"`
	Ping     bool   `json:"ping,omitempty"`
}

// captureReply is the host's answer to the extension. The extension cancels the
// browser's own download only when OK is true, so a forwarding failure never
// loses the download.
type captureReply struct {
	OK    bool   `json:"ok"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error,omitempty"`
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gidm-host: load config:", err)
		os.Exit(1)
	}
	socket := cfg.Daemon.SocketPath
	timeout := cfg.Daemon.DialTimeout.Duration()

	if err := run(os.Stdin, os.Stdout, socket, timeout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, "gidm-host:", err)
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer, socket string, timeout time.Duration) error {
	for {
		msg, err := readMessage(in)
		if err != nil {
			return err
		}
		reply := handle(msg, socket, timeout)
		raw, err := json.Marshal(reply)
		if err != nil {
			return err
		}
		if err := writeMessage(out, raw); err != nil {
			return err
		}
	}
}

// handle decodes one capture request and forwards it to the daemon. It never
// returns an error: any failure becomes a captureReply{OK:false} so the extension
// keeps the browser download rather than cancelling into a black hole.
func handle(msg []byte, socket string, timeout time.Duration) captureReply {
	var req captureRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return captureReply{Error: fmt.Sprintf("bad request: %v", err)}
	}
	if req.Ping {
		if err := ping(socket, timeout); err != nil {
			return captureReply{Error: err.Error()}
		}
		return captureReply{OK: true}
	}
	if !isHTTPURL(req.URL) {
		return captureReply{Error: fmt.Sprintf("unsupported url scheme: %q", req.URL)}
	}

	id, err := forward(socket, timeout, req)
	if err != nil {
		return captureReply{Error: err.Error()}
	}
	return captureReply{OK: true, ID: id}
}

// isHTTPURL reports whether raw is an absolute http/https URL. blob:, data:, and
// filesystem downloads cannot be re-fetched by the daemon, so the host declines
// them and the extension lets the browser handle them.
func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// forward maps a capture onto an Add and round-trips it to the daemon. Filename
// is left unset: the browser hands a full local path, and the daemon derives a
// safe name itself.
func forward(socket string, timeout time.Duration, req captureRequest) (string, error) {
	add := api.Add{}
	if req.Referrer != "" || req.Cookie != "" {
		add.Auth = &api.Credentials{Referer: req.Referrer, Cookie: req.Cookie}
	}
	resp, err := roundTrip(socket, timeout, api.NewAddRequestWithOptions(req.URL, add))
	if err != nil {
		return "", err
	}
	if !resp.OK {
		if resp.Error != nil {
			return "", errors.New(resp.Error.Message)
		}
		return "", errors.New("daemon rejected add")
	}
	if resp.Add == nil {
		return "", errors.New("daemon reply missing id")
	}
	return resp.Add.ID, nil
}

// ping probes that the daemon is up and accepting requests.
func ping(socket string, timeout time.Duration) error {
	resp, err := roundTrip(socket, timeout, api.NewPingRequest())
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New("daemon not ready")
	}
	return nil
}

// roundTrip dials the daemon and exchanges one request/response frame, mirroring
// the CLI's one-request-per-connection model (cmd/gidm/client.go).
func roundTrip(socket string, timeout time.Duration, req api.Request) (api.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return api.Response{}, fmt.Errorf("daemon unreachable on %q: %w", socket, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if err := api.WriteMessage(conn, req); err != nil {
		return api.Response{}, fmt.Errorf("send request: %w", err)
	}
	var resp api.Response
	if err := api.NewDecoder(conn).Decode(&resp); err != nil {
		return api.Response{}, fmt.Errorf("read reply: %w", err)
	}
	return resp, nil
}

func readMessage(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.NativeEndian.Uint32(lenBuf[:])
	if n > maxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeMessage(w io.Writer, msg []byte) error {
	var lenBuf [4]byte
	binary.NativeEndian.PutUint32(lenBuf[:], uint32(len(msg)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(msg)
	return err
}
