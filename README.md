# gidm

**A fast, open-source download manager in Go — a free alternative to Internet
Download Manager (IDM).** It saturates your connection with parallel, segmented
downloads and a zero-allocation transfer path. One static binary, no CGO,
runs on macOS, Linux, and Windows.

[![Release](https://img.shields.io/github/v/release/K-RED90/gidm?sort=semver)](https://github.com/K-RED90/gidm/releases/latest)
[![CI](https://github.com/K-RED90/gidm/actions/workflows/ci.yml/badge.svg)](https://github.com/K-RED90/gidm/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/K-RED90/gidm)](go.mod)
[![License](https://img.shields.io/github/license/K-RED90/gidm)](LICENSE)

<!-- TODO(demo): record `gidm add <url>` then `gidm list` and drop the GIF here:
     ![gidm in action](docs/demo.gif) -->

## Features

- **Faster downloads.** Every file is split into byte ranges fetched
  concurrently over separate connections, so one slow stream never caps your
  bandwidth — and fast workers steal a straggler's leftover ranges instead of
  waiting.
- **Resume, pause & retry.** Interrupted transfers pick up where they left off;
  progress is checkpointed and failed segments retry automatically.
- **Browser capture.** A Chrome extension hands your browser's downloads — even
  ones behind buttons, JS, or cookie/referer-gated links — to gidm to
  accelerate.
- **Rate limits & scheduling.** Per-download throttling and scheduling, driven
  over a local daemon by a scriptable CLI (a desktop GUI is in progress).
- **Single binary.** Pure Go, no CGO — download one file and run it.

## Install

No Go toolchain required — these install prebuilt binaries.

**macOS / Linux — one line:**

```sh
curl -fsSL https://raw.githubusercontent.com/K-RED90/gidm/main/scripts/install.sh | sh
```

It detects your OS and CPU, downloads the matching `gidm` + `gidmd` from the
[latest release][releases], and installs them to `/usr/local/bin` (using `sudo`
only if needed). Override the location with `GIDM_BIN_DIR=~/.local/bin`, or pin a
version with `GIDM_VERSION=v0.1.0`. Then check it:

```sh
gidm --version
```

[releases]: https://github.com/K-RED90/gidm/releases/latest

**Windows — one line** (PowerShell):

```powershell
irm https://raw.githubusercontent.com/K-RED90/gidm/main/scripts/install.ps1 | iex
```

Same idea: it picks the right build from the latest release, installs `gidm.exe`
and `gidmd.exe` to `%LOCALAPPDATA%\Programs\gidm`, and adds that to your `PATH`.
Override with `$env:GIDM_BIN_DIR`, or pin a version with `$env:GIDM_VERSION`.

Both installers resolve the latest version for you — there's no release number to
look up or paste.

### Desktop app (GUI)

Prefer a window over the terminal? Download the desktop app for your OS from the
[latest release][releases] — it bundles the daemon, so it's self-contained:

| OS | Asset | Run |
|----|-------|-----|
| Windows | `gidm-desktop_<ver>_windows_amd64.zip` | unzip, run `gidm-desktop.exe` (needs the [WebView2 runtime][webview2], preinstalled on Windows 11) |
| macOS | `gidm-desktop_<ver>_darwin_arm64.zip` | unzip, move `gidm-desktop.app` to Applications; first launch: right-click → **Open** (it's unsigned) |
| Linux | `gidm-desktop_<ver>_linux_amd64.tar.gz` | extract and run `./gidm-desktop` (needs GTK4 + WebKitGTK 6.0 installed) |

[webview2]: https://developer.microsoft.com/microsoft-edge/webview2/

The GUI is **early/beta** and unsigned, so the OS will warn on first run. It
starts and talks to its own `gidmd`, so it works without the CLI installed.

<details>
<summary>Manual download</summary>

Don't want to pipe to a shell? Download the archive for your platform from the
[latest release][releases] (assets are named `gidm_<version>_<os>_<arch>`, e.g.
`gidm_…_darwin_arm64.tar.gz` for Apple Silicon, `gidm_…_windows_amd64.zip`),
unpack it, and put `gidm`/`gidmd` on your `PATH`:

```sh
tar -xzf gidm_*_*.tar.gz                       # Windows: just unzip it
sudo install -m 0755 gidm gidmd /usr/local/bin/
```

Optionally verify against the published checksums
(`sha256sum -c checksums.txt`; macOS `shasum -a 256 -c`). If you downloaded
through a browser on macOS, clear the quarantine flag once:
`xattr -d com.apple.quarantine /usr/local/bin/gidm /usr/local/bin/gidmd`.

</details>

## Quick start

```sh
gidmd &                         # start the daemon (once)
gidm add https://example/f.bin  # queue a download — prints its id
gidm list                       # id, status, progress, size, destination
gidm status <id>                # one download (alias: gidm get <id>)
gidm pause <id>                 # pause / resume / cancel
gidm resume <id>
gidm rm <id>
```

Add `--json` to any command for machine-readable output. `--socket`, `--config`,
and `--timeout` override the configured socket path and client round-trip budget
(`daemon.dial_timeout`). Exit codes: `0` ok, `2` bad request, `3` daemon not
running, `4` timeout, `5` not found.

## Configuration

Nothing is hardcoded. Settings resolve in order (later wins): built-in defaults →
TOML config file → `GIDM_*` environment variables → command-line flags. Copy
[`configs/gidm.example.toml`](configs/gidm.example.toml) to your OS config dir,
or point `$GIDM_CONFIG` at it.

## Browser extension

A Manifest V3 Chrome extension captures browser downloads — including ones behind
buttons/JS and cookie- or referer-gated links — and hands them to the daemon to
accelerate, reaching it through the `gidm-host` native-messaging host.

It isn't on the Web Store yet, so you load it unpacked from this repo and
register the host with one script. Full steps are in
[`extensions/chrome/README.md`](extensions/chrome/README.md): build, start
`gidmd`, **Load unpacked** `extensions/chrome/` at `chrome://extensions`, then
`./scripts/install-chrome-host.sh <EXTENSION_ID>`.

## How it works

A pure-Go **engine** library is wrapped by a background **daemon** that exposes a
control API over a Unix domain socket. The **CLI**, **desktop app**, and **Chrome
extension** are all clients of that daemon; the extension reaches it through a
thin native-messaging host. The engine fetches over HTTP through the `httpx`
adapter and persists only through the `engine.Store` interface — it never imports
the daemon or its clients.

```
extension ─▶ gidm-host ─┐
cli ────────────────────┼─▶ gidmd (daemon) ─▶ engine ─▶ httpx (HTTP) + store (SQLite)
desktop ────────────────┘
```

What keeps it fast:

- **Segmented, work-stealing transfers** — concurrent byte-range connections,
  rebalanced so a straggler can't stall the download.
- **Zero-allocation hot path** — the per-chunk read/write loop allocates zero
  bytes per iteration (pooled buffers + `io.CopyBuffer`), proven by a `-benchmem`
  benchmark, so the garbage collector never touches downloaded bytes.
- **Pooled keep-alive connections** — one tuned `net/http` transport reuses
  connections (HTTP keep-alive / HTTP/2) across segments, paying the TCP + TLS
  handshake once.
- **Persistence off the hot path** — segment progress is checkpointed to SQLite
  periodically, never per chunk, so resume support never throttles the transfer.
- **No shared write cursor** — each segment is one goroutine writing to its own
  file offset via `WriteAt`, so workers never contend for a lock.

## Status

**Working today:** the segmented engine (parallel ranges, resume, retries,
integrity checks, work-stealing rebalancing), per-download rate limiting and
scheduling, a SQLite-backed history, the `gidmd` daemon, the `gidm` CLI, and the
Chrome download-capture extension.

**Beta:** a Wails v3 + Svelte desktop app, downloadable per OS (see
[Desktop app](#desktop-app-gui)) — see [`desktop/`](desktop/README.md).

**Planned:** code-signed desktop installers (`.dmg` / NSIS / AppImage), a Chrome
Web Store listing, and Homebrew / Scoop packages.

## Development

Building from source needs Go (the CLI and daemon are pure Go, no CGO); the
desktop app additionally needs Bun and Wails.

```sh
make build   # binaries into ./bin
make test    # go test -race
make bench   # benchmarks with -benchmem
make lint    # golangci-lint
make vuln    # govulncheck
make check   # vet + test
```

| Path | Purpose |
|------|---------|
| `internal/engine` | core download engine + contracts (`Store`, `Download`, `Segment`) |
| `internal/config` | layered configuration (defaults → file → env → flags) |
| `internal/httpx` | ranged GETs, retry/backoff, secure bounded redirects, URL probe |
| `internal/store/sqlite` | SQLite persistence behind `engine.Store` |
| `api` | public daemon control API / wire protocol |
| `cmd/gidmd` | daemon |
| `cmd/gidm` | CLI client |
| `cmd/gidm-host` | Chrome native-messaging host |
| `desktop/` | Wails + Svelte desktop app |
| `extensions/chrome/` | Chrome extension |

## License

[Apache-2.0](LICENSE).
