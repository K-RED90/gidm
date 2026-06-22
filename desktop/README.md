# gidm desktop (M5)

A modern Wails v3 + Svelte + TypeScript desktop app for gidm. It is a **thin
client of the `gidmd` daemon**: every action is one round-trip over the daemon's
Unix socket using the public `api` NDJSON protocol — the same contract the CLI
speaks. No engine logic lives here; the daemon stays the single source of truth,
so downloads survive the window closing and app restarts.

This is a **separate Go module** (`github.com/K-RED90/gidm/desktop`) joined to the
repo via a root `go.work`. It must stay separate: Wails pulls in CGO + a webview
dependency tree that must never contaminate the core's pure-Go, no-CGO build.
The core `make` targets set `GOWORK=off` so they ignore this module.

## Prerequisites

- **Go** 1.25+ and the core repo checked out.
- **Wails v3 CLI**: `go install github.com/wailsapp/wails/v3/cmd/wails3@latest`
  (ensure `$(go env GOPATH)/bin` is on `PATH`). Run `wails3 doctor` to verify.
- **Bun**: the frontend toolchain (`brew install bun` or https://bun.sh). It is
  build-time only — nothing from it ships in the binary.
- macOS: Xcode Command Line Tools (WebKit). Linux/Windows: see the Wails v3 docs.

## Develop & build

From the repo root:

```sh
make desktop-dev       # wails3 dev — hot-reloading dev build
make desktop-build     # wails3 build — production binary in desktop/bin
make desktop-test      # go test -race over the bridge + internal packages
make desktop-generate  # regenerate the TS bindings from the Go services
```

(`wails3` embeds its task runner, so no separate `task` install is needed.)

The desktop module resolves the local `api` package via a `replace` directive in
`go.mod`, so it builds on a fresh clone without a `go.work`. The root `go.work`
is a local dev convenience and is gitignored.

## How it fits together

- `internal/client` — dials the daemon socket and does one request/response per
  call (ports the CLI's proven dial + error-classification logic).
- `internal/socketpath` — resolves the socket path (explicit setting → env
  `GIDM_DAEMON_SOCKET_PATH` → the daemon's default `~/.config/gidm/gidmd.sock`).
- `internal/daemon` — **auto-starts gidmd** if it isn't already running (locates
  the binary beside the app or on `PATH`), so the app works without a hand-launched
  daemon. It never stops gidmd — downloads keep running in the background.
- `bridge` — the Wails-bound service (`Add`/`List`/`Status`/`Pause`/`Resume`/
  `Remove`/`Health`). GUI-free, so it's unit-tested without CGO.
- `main.go` — the Wails wiring: window, native app menu (gives the URL field
  Cmd+C/V/A), single-instance lock, system tray (close-to-tray), and the event
  pump that polls a snapshot each second, pushes it to the UI, and fires
  completion **notifications**.
- `frontend/src` — Svelte 5 UI: `lib/tokens.css` (system-adaptive design tokens),
  `lib/bridge.ts` (the single seam to the generated bindings), `lib/store.svelte.ts`
  (runes-based shared state), and components (sidebar, add bar, dense downloads
  table, status bar, offline banner).

## Notes

- The window UI needs a desktop session to run; `wails3 dev`/`build` won't render
  in a headless environment.
- Native notifications on macOS only display from a packaged, authorized build.
- A toolbar Settings panel edits the daemon's runtime config (download folder,
  default connections/priority, and the global + per-download speed caps) over the
  `get-config`/`set-config` protocol; the right-click "Limit speed" submenu caps a
  single download live (`set-rate`). Changes persist in the store and survive a
  restart. The Chrome extension (M4) is the remaining future work.
