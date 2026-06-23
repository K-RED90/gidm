# gidm

A fast, open-source download manager written in Go — a free alternative to
Internet Download Manager (IDM). **The goal is simple: beat IDM on download
performance**, through segmented (parallel) downloading and a zero-allocation
transfer path, with no CGO and a minimal, audited dependency set.

> **Status: M0–M3 complete; M5 (desktop app) in progress.** The engine
> (segmented downloads, resume, retries, integrity), the SQLite store, the HTTP
> layer, the daemon + CLI, and work-stealing segmentation have all landed. The
> Wails + Svelte desktop app is being built on top. See the roadmap below.

## Why it's fast

gidm is built to saturate the link and stay out of its own way:

- **Segmented parallel downloads.** Every file is split into byte ranges fetched
  concurrently over separate connections, so a single slow stream never caps
  throughput. Work-stealing segmentation (M3) keeps fast workers busy instead of
  stalling on a straggler.
- **Zero-allocation hot path.** The per-chunk read/write loop allocates zero
  bytes per iteration (pooled buffers + `io.CopyBuffer`), proven by a `-benchmem`
  benchmark — the garbage collector never sees the bytes you download.
- **Pooled, keep-alive connections.** A single tuned `net/http` transport reuses
  connections (HTTP keep-alive / HTTP/2) across segments and downloads, so the
  TCP + TLS handshake is paid once, not per request.
- **Persistence off the hot path.** Segment progress is checkpointed to SQLite
  periodically, never per chunk, so resume support never throttles the transfer.
- **No shared write cursor.** Each segment is one goroutine writing to its own
  file offset via `WriteAt` — no lock contention between workers.
- **Pure Go, no CGO** (including the SQLite store), so binaries cross-compile
  cleanly and ship as a single self-contained file.

## Architecture

A pure-Go **engine** library is wrapped by a background **daemon** that exposes a
control API over a Unix domain socket. The **CLI**, **desktop app** (Wails +
Svelte), and **Chrome extension** are all clients of that daemon; the extension
reaches it through a thin native-messaging host. The engine fetches over HTTP
through the `httpx` adapter and persists only through the `engine.Store`
interface — it never imports the daemon or its clients.

```
extension ─▶ gidm-host ─┐
cli ────────────────────┼─▶ gidmd (daemon) ─▶ engine ─▶ httpx (HTTP) + store (SQLite)
desktop ────────────────┘
```

## Layout

| Path | Purpose |
|------|---------|
| `internal/engine` | core download engine + contracts (`Store`, `Download`, `Segment`) |
| `internal/config` | layered configuration (defaults → file → env → flags) |
| `internal/httpx` | ranged GETs, retry/backoff, secure bounded redirects, URL probe |
| `internal/store/sqlite` | SQLite persistence behind `engine.Store` |
| `api` | public daemon control API / wire protocol (M2) |
| `cmd/gidmd` | daemon |
| `cmd/gidm` | CLI client (`add`, `list`, `status`/`get`, `pause`, `resume`, `rm`) |
| `cmd/gidm-host` | Chrome native-messaging host |
| `desktop/` | Wails + Svelte desktop app (M5) |
| `extensions/chrome/` | Chrome extension (M4) |
| `configs/gidm.example.toml` | documented configuration reference |

## Build & test

```sh
make build   # binaries into ./bin
make test    # go test -race
make bench   # benchmarks with -benchmem
make lint    # golangci-lint
make vuln    # govulncheck
make check   # vet + test
```

## Configuration

Nothing is hardcoded. Settings resolve in order (later wins): built-in defaults →
TOML config file → `GIDM_*` environment variables → command-line flags. Copy
`configs/gidm.example.toml` to your OS config dir, or point `$GIDM_CONFIG` at it.

## CLI

`gidm` drives a running `gidmd` over its Unix socket — it speaks only the
`api` protocol and never links the engine, store, or HTTP layers.

```sh
gidmd &                         # start the daemon
gidm add https://example/f.bin  # prints the new download id
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

## Roadmap

- **M0** — foundation: structure, config, tooling, CI, engine contracts ✅
- **M1** — core engine: segmented download, resume, retries, integrity, SQLite
  store, HTTP layer ✅
- **M2** — daemon + CLI ✅
- **M3** — dynamic segmentation (work-stealing), rate limiting, scheduler ✅
- **M5** — Wails v3 + Svelte desktop app *(in progress — brought ahead of M4;
  foundation landed, see [`desktop/`](desktop/README.md))*
- **M4** — Chrome extension + native-messaging host

## License

[Apache-2.0](LICENSE).
