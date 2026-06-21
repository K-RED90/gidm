# gidm

A fast, open-source download manager written in Go — a free alternative to
Internet Download Manager (IDM). **The goal is simple: beat IDM on download
performance**, through segmented (parallel) downloading and a zero-allocation
transfer path, with no CGO and a minimal, audited dependency set.

> **Status: M1 — core engine (in progress).** The foundation, layered
> configuration, the engine contracts, the SQLite store, and the HTTP layer have
> landed. End-to-end segmented downloading is being assembled on top of them. See
> the roadmap below.

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
| `internal/api` | daemon control API (M2) |
| `cmd/gidmd` | daemon |
| `cmd/gidm` | CLI client |
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

## Roadmap

- **M0** — foundation: structure, config, tooling, CI, engine contracts ✅
- **M1** — core engine: segmented download, resume, retries, integrity, SQLite
  store, HTTP layer *(in progress — SQLite store and HTTP layer landed)*
- **M2** — daemon + CLI
- **M3** — dynamic segmentation (work-stealing), rate limiting, scheduler
- **M4** — Chrome extension + native-messaging host
- **M5** — Wails + Svelte desktop app

## License

[Apache-2.0](LICENSE).
