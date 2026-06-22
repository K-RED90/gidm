// Package daemon lets the desktop app ensure a gidmd is running, so the GUI works
// without the user starting a daemon by hand. It only starts gidmd; it never
// stops it — the daemon owns all downloads and should outlive the window so
// transfers keep running in the background.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/desktop/internal/client"
)

const (
	pingTimeout  = 500 * time.Millisecond
	readyTimeout = 5 * time.Second
	readyPoll    = 100 * time.Millisecond
)

// ErrBinaryNotFound is returned when the gidmd executable can't be located.
var ErrBinaryNotFound = errors.New("gidmd not found beside the app or on PATH")

// EnsureRunning makes sure a gidmd is listening on socket. If a ping already
// succeeds it returns (false, nil) without spawning. Otherwise it locates the
// gidmd binary, starts it bound to the same socket, and waits until it answers a
// ping. gidmd's own single-instance guard makes a redundant spawn harmless (the
// extra process exits on ErrAlreadyRunning), so this is safe to call on every
// reconnect attempt.
func EnsureRunning(socket string) (spawned bool, err error) {
	if Ping(socket) {
		return false, nil
	}
	bin, err := locate()
	if err != nil {
		return false, err
	}
	// Bind gidmd to the exact socket the desktop dials, so an env/default mismatch
	// can't leave them talking past each other.
	cmd := exec.Command(bin, "-socket", socket)
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("daemon: start %q: %w", bin, err)
	}
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		if Ping(socket) {
			return true, nil
		}
		time.Sleep(readyPoll)
	}
	return true, errors.New("daemon: gidmd did not become ready in time")
}

// Ping reports whether a gidmd answers on socket.
func Ping(socket string) bool {
	resp, err := client.New(socket, pingTimeout).Do(context.Background(), api.NewPingRequest())
	return err == nil && resp.OK && resp.Ping != nil
}

// locate finds the gidmd executable: first beside this app's binary (a packaged
// build ships its own daemon next to the GUI), then on PATH (a dev build).
func locate() (string, error) {
	name := binaryName()
	if exe, err := os.Executable(); err == nil {
		if cand := filepath.Join(filepath.Dir(exe), name); isExecutable(cand) {
			return cand, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", ErrBinaryNotFound
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "gidmd.exe"
	}
	return "gidmd"
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}
