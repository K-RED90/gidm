# CLAUDE.md — gidm

Working guide for Claude on this repo. Read before making changes.

## What this is

`gidm` is an open-source, high-performance download manager in Go — a free
alternative to Internet Download Manager (IDM). The goal is to **beat IDM on
download performance**. Module path: `github.com/K-RED90/gidm`.

## Performance — how we beat IDM

Throughput is the whole point. The levers, in priority order:

1. **Segmented parallel downloads** — split each file into byte ranges fetched
   concurrently over separate connections; one slow stream never caps the link.
2. **Zero-alloc transfer loop** — pooled buffers + `io.CopyBuffer`, so the GC
   never touches downloaded bytes (principle 2 below).
3. **Connection reuse** — one tuned transport with keep-alive/HTTP2 shared across
   all segments; pay the TCP+TLS handshake once, not per request.
4. **Persistence off the hot path** — checkpoint resume progress periodically,
   never per chunk.
5. **Work-stealing segmentation (M3)** — rebalance ranges so fast workers absorb
   a straggler's remaining bytes.

Measure, don't guess: back every performance claim with a `-benchmem`/throughput
benchmark before trusting it.

## Core principles (non-negotiable)

1. **Efficiency first.** This is an I/O-bound app; choose fast, lightweight
   frameworks and avoid unnecessary work. Measure, don't guess.
2. **Zero-alloc hot path.** The per-chunk read/write loop must allocate zero
   bytes per iteration (pooled buffers + `io.CopyBuffer`), verified by a
   `-benchmem` benchmark. Allocate freely in cold paths (setup, config, UI).
3. **Std-lib first, minimal dependencies.** Every dependency is audited and
   justified. Two direct third-party deps: `BurntSushi/toml` (config) and
   `modernc.org/sqlite` (the store; pure Go, no CGO).
4. **Nothing hardcoded.** Every tunable lives in `internal/config` and is
   overridable: defaults → file → env → flags.
5. **Everything tested.** Tests run under `-race`. Cover failure modes, not just
   the happy path.
6. **Security by design.** Local-only daemon socket, validated URLs/filenames,
   bounded redirects, TLS verified by default.
7. **Comments only where necessary.** Prefer self-documenting code. Comment the
   non-obvious "why", invariants, and wire formats — not the obvious.

## Architecture

Pure-Go **engine** → **daemon** (`gidmd`, API over a Unix socket) → clients
(**CLI**, **desktop**, **Chrome extension**). The extension talks to the daemon
via a thin native-messaging host (`gidm-host`). The engine fetches over HTTP
through the `httpx` adapter, never imports the daemon/clients, and persists only
through the `engine.Store` interface.

## HTTP

All network I/O goes through `internal/httpx` (`net/http` only). It is a leaf
adapter — imports only the std lib and `internal/config` — exposing one pooled
`*Client`: `Probe` (size, range support, validators, suggested filename),
`RangeGet` (a streamed 206 for a single segment), retry with backoff, and a
bounded, secure redirect policy (http/https only, no downgrades). The transport
is reused for keep-alive; the response body is never read into memory here, and
no overall client timeout is set (only connection setup is bounded, so long
streaming downloads are governed by the caller's context).

## Storage

Embedded **SQLite** (`modernc.org/sqlite`, pure Go, no CGO) behind
`engine.Store`. Segment resume progress is checkpointed periodically — **never
written per-chunk** (that would put the database on the hot path).

## Commands

```sh
make build   # build binaries to ./bin
make test    # go test -race -count=1 ./...
make bench   # benchmarks with allocation stats
make vet     # go vet
make lint    # golangci-lint (config: .golangci.yml)
make vuln    # govulncheck
make tidy    # go mod tidy
```

## Conventions

- Wrap errors with `%w` and context: `fmt.Errorf("config: load %q: %w", path, err)`.
- Propagate `context.Context` through all I/O and store calls.
- Concurrency: each segment is one goroutine writing to its own file offset via
  `WriteAt` — no shared write cursor. Run the race detector.
- Don't enable lint rules that force doc comments on every exported symbol.

## Roadmap

- **M0** foundation (done): structure, config, tooling, CI, engine contracts.
- **M1** core engine (done): segmented download, resume, retries, integrity,
  SQLite store, HTTP layer.
- **M2** daemon + CLI (done).
- **M3** dynamic segmentation (work-stealing), rate limiting, scheduler (done).
- **M5** Wails v3 + Svelte desktop app (*in progress — brought ahead of M4*).
  Foundation landed: separate `desktop/` module via root `go.work`, a thin
  daemon-client bridge, the Svelte UI shell, daemon auto-start, app menu,
  single-instance, system tray, and notifications. See `desktop/README.md`.
- **M4** Chrome extension + native-messaging host.

The wire protocol now lives in the public `api/` package (promoted out of
`internal/api`) because the desktop is a separate module and the protocol is the
contract every client speaks. The desktop module must stay separate (it pulls in
Wails + CGO), so the core's `make` targets set `GOWORK=off` to keep building the
pure-Go engine/daemon/CLI alone.
