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
// optional overrides (empty string / 0 means "let the daemon decide"); auth is
// optional request credentials, sent only when non-empty, so the quick-add path
// can pass zero values and behave exactly as the bare-URL form did.
func (b *Bridge) Add(url, dir, filename string, segments int, priority api.Priority, auth api.Credentials) (string, error) {
	add := api.Add{
		Priority: priority,
		Dir:      dir,
		Filename: filename,
		Segments: segments,
	}
	if !auth.IsZero() {
		add.Auth = &auth
	}
	resp, err := b.client.Do(context.Background(), api.NewAddRequestWithOptions(url, add))
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

// SetRate caps a single download to bps bytes/sec; 0 removes the per-download cap
// (the download then inherits the daemon default). Applied live by the daemon if
// the download is running.
func (b *Bridge) SetRate(id string, bps int) error {
	return b.ack(api.NewSetRateRequest(id, bps))
}

// GetConfig returns the daemon's current runtime settings (download dir, default
// segments/priority, and the global + per-download speed caps).
func (b *Bridge) GetConfig() (api.ConfigView, error) {
	resp, err := b.client.Do(context.Background(), api.NewGetConfigRequest())
	if err != nil {
		return api.ConfigView{}, err
	}
	if !resp.OK || resp.Config == nil {
		return api.ConfigView{}, respErr(resp)
	}
	return resp.Config.Config, nil
}

// SetConfig changes the daemon's runtime settings from the Settings form. The
// desktop sends the fully-populated form (it pre-fills from GetConfig), so every
// field is set; rates are bytes/sec with 0 = unlimited. It returns the now-current
// settings so the UI reflects any normalization the daemon applied.
func (b *Bridge) SetConfig(downloadDir string, segments int, priority api.Priority, maxRate, perDownloadMaxRate int) (api.ConfigView, error) {
	sc := api.SetConfig{
		DownloadDir:         &downloadDir,
		SegmentsPerDownload: &segments,
		DefaultPriority:     &priority,
		MaxRate:             &maxRate,
		PerDownloadMaxRate:  &perDownloadMaxRate,
	}
	resp, err := b.client.Do(context.Background(), api.NewSetConfigRequest(sc))
	if err != nil {
		return api.ConfigView{}, err
	}
	if !resp.OK || resp.Config == nil {
		return api.ConfigView{}, respErr(resp)
	}
	return resp.Config.Config, nil
}

// SetAuth replaces a download's request credentials so a 401'd or hotlink-blocked
// download can be fixed and resumed. keepPassword=true keeps the stored password
// and applies only auth's other fields — the wire never echoes a password back,
// so the properties editor leaves it blank when unchanged.
func (b *Bridge) SetAuth(id string, auth api.Credentials, keepPassword bool) error {
	return b.ack(api.NewSetAuthRequest(id, auth, keepPassword))
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
