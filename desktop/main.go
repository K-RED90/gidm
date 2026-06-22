package main

import (
	"embed"
	"log"
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

//go:embed build/appicon.png
var trayIcon []byte

const (
	// dialTimeout bounds each daemon round-trip; the control protocol replies
	// immediately, so a few seconds is ample.
	dialTimeout = 10 * time.Second
	// pollInterval is how often the UI is refreshed and notifications are checked.
	pollInterval = time.Second
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
			UniqueID: "com.github.k-red90.gidm",
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
		Title:  "gidm",
		Width:  1100,
		Height: 720,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(15, 17, 21),
		URL:              "/",
	})

	// Close-to-tray: hide instead of destroying the window so the tray can bring
	// it back and the app keeps running.
	window.OnWindowEvent(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		window.Hide()
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
	tray.SetLabel("gidm")
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
		notifyTransitions(notifier, prev, downloads)
	}
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
