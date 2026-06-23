package main

import (
	"embed"
	"log"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/desktop/bridge"
	"github.com/K-RED90/gidm/desktop/internal/client"
	"github.com/K-RED90/gidm/desktop/internal/daemon"
	"github.com/K-RED90/gidm/desktop/internal/socketpath"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/trayicon.png
var trayIcon []byte

const (
	// dialTimeout bounds each daemon round-trip; the control protocol replies
	// immediately, so a few seconds is ample.
	dialTimeout = 10 * time.Second
	// pollInterval is how often the UI is refreshed, notifications are checked, and
	// a freshly-captured download is surfaced. Kept short so a browser capture pops
	// the window near-instantly (the daemon's Add returns before the download
	// starts, so the only lag is one tick) and progress bars update smoothly. The
	// snapshot is a cheap local-socket round-trip.
	// ponytail: flat 250ms poll; if download counts get large enough that
	// ListDownloads-per-tick shows up in a profile, switch to a daemon-pushed
	// event stream instead of polling faster.
	pollInterval = 250 * time.Millisecond
	// respawnBackoff throttles auto-restart attempts while the daemon is down.
	respawnBackoff = 15 * time.Second
)

func main() {
	socket := socketpath.Resolve("")

	// Best-effort auto-start so the GUI works without a hand-launched daemon. A
	// failure is non-fatal: the UI shows an offline banner and the pump retries.
	if _, err := daemon.EnsureRunning(socket); err != nil {
		log.Printf("gidm: could not auto-start daemon: %v", err)
	}

	br := bridge.New(client.New(socket, dialTimeout))
	notifier := notifications.New()

	app := application.New(application.Options{
		Name:        "gidm",
		Description: "gidm — open-source download accelerator",
		// One window per machine; a relaunch focuses the running instance instead of
		// opening a duplicate that would fight over the daemon.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "io.github.k_red90.gidm",
		},
		Services: []application.Service{
			application.NewService(br),
			application.NewService(notifier),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// Closing the window leaves the app alive in the tray (downloads keep
			// running in the daemon); Quit is explicit via the menu/tray.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	// Native application menu. The Edit role gives the URL field Cmd+C/V/A — the
	// app's primary action is pasting a URL, so this is essential, not cosmetic.
	app.Menu.Set(application.DefaultApplicationMenu())

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "gidm",
		Width:     1100,
		Height:    720,
		MinWidth:  900,
		MinHeight: 560,
		Mac: application.MacWindow{
			// MacTitleBarHidden (not HiddenInset) gives a full-size content window
			// with floating traffic lights and NO native NSToolbar — HiddenInset
			// sets UseToolbar:true, which added a tall native toolbar band above our
			// in-app toolbar. Our toolbar is the drag region via --wails-draggable.
			Backdrop: application.MacBackdropTranslucent,
			TitleBar: application.MacTitleBarHidden,
		},
		// Matches the frontend's --bg (#0e0e0f) so the native window and the webview
		// canvas read as one deep, neutral near-black surface.
		BackgroundColour: application.NewRGB(14, 14, 15),
		URL:              "/",
	})

	// Close-to-tray: hide instead of destroying the window so the tray can bring
	// it back and the app keeps running.
	window.OnWindowEvent(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		window.Hide()
	})

	// A capture progress pop-up can be the only visible window; this lets its
	// "Open gidm" button surface the main manager (the pop-up emits ui:show-main).
	app.Event.On("ui:show-main", func(*application.CustomEvent) {
		window.Show()
		window.Focus()
	})

	setupTray(app, window)

	go pump(app, br, notifier, socket)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// setupTray adds a menu-bar/tray icon: click to reopen the window, with a menu to
// show or quit.
func setupTray(app *application.App, window *application.WebviewWindow) {
	tray := app.SystemTray.New()
	// A full-colour glyph (transparent background, padded). SetIcon — not
	// SetTemplateIcon — so macOS keeps the brand colour instead of tinting it
	// monochrome. No label: the icon stands alone, compact and native.
	if len(trayIcon) > 0 {
		tray.SetIcon(trayIcon)
	}

	menu := application.NewMenu()
	menu.Add("Show gidm").OnClick(func(*application.Context) { window.Show() })
	menu.AddSeparator()
	menu.Add("Quit gidm").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)

	tray.OnClick(func() { window.Show() })
}

// pump is the single bridge between the daemon and the UI: it polls a snapshot,
// emits it to the frontend, fires completion notifications, and (while the daemon
// is unreachable) makes throttled, non-blocking restart attempts.
func pump(app *application.App, br *bridge.Bridge, notifier *notifications.NotificationService, socket string) {
	// Best-effort permission prompt; harmless (and a no-op) on platforms/builds
	// where notifications aren't available.
	go func() { _, _ = notifier.RequestNotificationAuthorization() }()

	prev := map[string]api.DownloadStatus{}
	known := map[string]struct{}{}
	seeded := false
	var lastRespawn time.Time

	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for range t.C {
		downloads, reachable := br.Snapshot()
		app.Event.Emit(bridge.EventDaemon, reachable)
		if !reachable {
			if time.Since(lastRespawn) > respawnBackoff {
				lastRespawn = time.Now()
				go func() { _, _ = daemon.EnsureRunning(socket) }()
			}
			continue
		}
		app.Event.Emit(bridge.EventDownloads, downloads)
		surfaceNew(app, br, known, seeded, downloads)
		seeded = true
		notifyTransitions(notifier, prev, downloads)
	}
}

// surfaceNew opens a floating progress window (IDM-style, separate from the main
// manager) for any download that just appeared — a browser capture or a CLI add.
// The first snapshot only seeds the known-set so existing downloads on launch
// don't pop, and the desktop's own Adds are skipped so they don't double-pop.
// known is rebuilt each tick from the current ids, so removed downloads drop out.
func surfaceNew(app *application.App, br *bridge.Bridge, known map[string]struct{}, seeded bool, downloads []api.DownloadView) {
	for _, id := range freshIDs(known, seeded, downloads) {
		if br.ConsumeLocalAdd(id) {
			continue
		}
		openPopup(app, id)
	}
}

// popup window dimensions. Width is fixed; height is just the initial value —
// the webview resizes the window to fit its content on load (ProgressPopup), so
// this is only the brief pre-paint size and a fallback.
const (
	popupWidth  = 480
	popupHeight = 300
)

// openPopup shows a small floating progress window for one download. It reuses
// the window registry by name, so a repeat capture of the same id just refocuses
// the existing window instead of stacking duplicates. Show/Focus marshal to the
// main thread internally, so calling them from the pump goroutine is safe.
func openPopup(app *application.App, id string) {
	name := "popup:" + id
	if w, ok := app.Window.GetByName(name); ok {
		w.Show()
		w.Focus()
		return
	}
	w := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:          name,
		Title:         "Download",
		Width:         popupWidth,
		Height:        popupHeight,
		DisableResize: true,
		// A fixed-size progress box: no maximise/fullscreen (both target the green
		// zoom button on macOS), so it can't be blown up to a useless empty window.
		MaximiseButtonState:   application.ButtonDisabled,
		FullscreenButtonState: application.ButtonDisabled,
		URL:                   "/?popup=" + url.QueryEscape(id),
		Mac: application.MacWindow{
			Backdrop: application.MacBackdropTranslucent,
			TitleBar: application.MacTitleBarHidden,
		},
		// matches the frontend's --surface so the window chrome and the webview
		// read as one panel (and there's no dark flash before the webview paints)
		BackgroundColour: application.NewRGB(22, 22, 24),
	})
	w.Show()
	w.Focus()
}

// freshIDs returns the ids in downloads not seen in known, then rebuilds known
// from the current ids (so removed downloads drop out). The first pass
// (seeded=false) only seeds and returns none, so downloads already present at
// launch don't surface.
func freshIDs(known map[string]struct{}, seeded bool, downloads []api.DownloadView) []string {
	var fresh []string
	for _, d := range downloads {
		if _, ok := known[d.ID]; !ok && seeded {
			fresh = append(fresh, d.ID)
		}
	}
	for id := range known {
		delete(known, id)
	}
	for _, d := range downloads {
		known[d.ID] = struct{}{}
	}
	return fresh
}

// notifyTransitions fires a native notification when a download newly reaches a
// terminal state. It tracks the previous status per id; downloads already
// terminal on the first tick (existed == false) are not re-announced.
func notifyTransitions(n *notifications.NotificationService, prev map[string]api.DownloadStatus, downloads []api.DownloadView) {
	seen := make(map[string]struct{}, len(downloads))
	for _, d := range downloads {
		seen[d.ID] = struct{}{}
		was, existed := prev[d.ID]
		prev[d.ID] = d.Status
		if !existed || was == d.Status {
			continue
		}
		switch d.Status {
		case api.StatusCompleted:
			send(n, d.ID, "Download complete", displayName(d))
		case api.StatusFailed:
			send(n, d.ID, "Download failed", displayName(d))
		}
	}
	for id := range prev {
		if _, ok := seen[id]; !ok {
			delete(prev, id)
		}
	}
}

func send(n *notifications.NotificationService, id, title, body string) {
	// Best-effort: unbundled/unauthorized builds simply won't display it.
	_ = n.SendNotification(notifications.NotificationOptions{ID: id, Title: title, Body: body})
}

// displayName derives a human label from the destination path, falling back to
// the URL.
func displayName(d api.DownloadView) string {
	if d.Destination != "" {
		if base := filepath.Base(d.Destination); base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	if i := strings.LastIndexByte(strings.TrimRight(d.URL, "/"), '/'); i >= 0 {
		if seg := d.URL[i+1:]; seg != "" {
			return seg
		}
	}
	return d.URL
}
