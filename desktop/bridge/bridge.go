// Package bridge exposes a running gidmd daemon to the desktop frontend. It is a
// thin Wails service: every bound method is one api round-trip through the daemon
// client, so the daemon stays the single source of truth and no engine logic
// lives here. It imports no Wails/GUI code — the event pump that pushes snapshots
// to the UI lives in main.go — so the bridge stays a fast, race-tested leaf.
package bridge

import (
	"context"
	"errors"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/desktop/internal/client"
)

// Event names the bridge's snapshots are published under (emitted from main.go).
const (
	EventDownloads = "downloads:update" // payload: []api.DownloadView
	EventDaemon    = "daemon:status"    // payload: bool (daemon reachable)
)

// Bridge is the Wails-bound service. Its exported methods are callable from the
// frontend; each maps a failed (!OK) response to a plain error so Wails rejects
// the JS promise with the daemon's human message.
type Bridge struct {
	client *client.Client
}

// New builds a Bridge over a daemon client.
func New(c *client.Client) *Bridge { return &Bridge{client: c} }

// Add submits a download and returns its new id. dir, filename, and segments are
// optional overrides (empty string / 0 means "let the daemon decide"), so the
// frontend's quick-add path can pass ("", "", 0, "") and behave exactly as the
// bare-URL form did.
func (b *Bridge) Add(url, dir, filename string, segments int, priority api.Priority) (string, error) {
	req := api.NewAddRequestWithOptions(url, api.Add{
		Priority: priority,
		Dir:      dir,
		Filename: filename,
		Segments: segments,
	})
	resp, err := b.client.Do(context.Background(), req)
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", respErr(resp)
	}
	return resp.Add.ID, nil
}

// List returns every download. It never returns a nil slice on success so the
// frontend can bind it directly.
func (b *Bridge) List() ([]api.DownloadView, error) {
	resp, err := b.client.Do(context.Background(), api.NewListRequest())
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, respErr(resp)
	}
	if resp.List == nil {
		return []api.DownloadView{}, nil
	}
	return resp.List.Downloads, nil
}

// Status returns one download by id.
func (b *Bridge) Status(id string) (api.DownloadView, error) {
	resp, err := b.client.Do(context.Background(), api.NewStatusRequest(id))
	if err != nil {
		return api.DownloadView{}, err
	}
	if !resp.OK {
		return api.DownloadView{}, respErr(resp)
	}
	return resp.Status.Download, nil
}

// Pause pauses a running download.
func (b *Bridge) Pause(id string) error { return b.ack(api.NewPauseRequest(id)) }

// Resume re-queues a paused/failed/completed download.
func (b *Bridge) Resume(id string) error { return b.ack(api.NewResumeRequest(id)) }

// Remove deletes a download.
func (b *Bridge) Remove(id string) error { return b.ack(api.NewRmRequest(id)) }

// SetPriority changes a download's scheduling priority (low/normal/high).
func (b *Bridge) SetPriority(id string, priority api.Priority) error {
	return b.ack(api.NewSetPriorityRequest(id, priority))
}

// OpenFile opens a downloaded file with the OS default application. The path is
// the download's destination (the frontend already has it), so no daemon lookup
// is needed.
func (b *Bridge) OpenFile(path string) error { return openPath(path) }

// RevealInFolder shows a file in the OS file manager, selecting it where the
// platform supports it.
func (b *Bridge) RevealInFolder(path string) error { return revealPath(path) }

// Health reports whether gidmd answers a ping. Any transport error or a
// non-pong response reads as "not running".
func (b *Bridge) Health() bool {
	resp, err := b.client.Do(context.Background(), api.NewPingRequest())
	return err == nil && resp.OK && resp.Ping != nil
}

// Snapshot is the event pump's read: the current downloads plus whether the
// daemon was reachable. A transport error yields (nil, false) so the UI can show
// a "daemon unreachable" state without distinguishing error kinds.
func (b *Bridge) Snapshot() (downloads []api.DownloadView, reachable bool) {
	dl, err := b.List()
	if err != nil {
		return nil, false
	}
	return dl, true
}

// ack runs a request whose success is a bare acknowledgement (pause/resume/rm).
func (b *Bridge) ack(req api.Request) error {
	resp, err := b.client.Do(context.Background(), req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return respErr(resp)
	}
	return nil
}

// respErr turns a failed response into an error carrying the daemon's human
// message (Wails surfaces it as the JS promise rejection reason).
func respErr(resp api.Response) error {
	if resp.Error != nil {
		return errors.New(resp.Error.Message)
	}
	return errors.New("daemon returned an error")
}
