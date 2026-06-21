# CLAUDE.md — gidm

Working guide for Claude on this repo. Read before making changes.

## What this is

`gidm` is an open-source, high-performance download manager in Go — a free
alternative to Internet Download Manager (IDM). The goal is to **beat IDM on
speed and efficiency**. Module path: `github.com/K-RED90/gidm`.

## Core principles (non-negotiable)

1. **Efficiency first.** This is an I/O-bound app; choose fast, lightweight
   frameworks and avoid unnecessary work. Measure, don't guess.
2. **Zero-alloc hot path.** The per-chunk read/write loop must allocate zero
   bytes per iteration (pooled buffers + `io.CopyBuffer`), verified by a
   `-benchmem` benchmark. Allocate freely in cold paths (setup, config, UI).
3. **Std-lib first, minimal dependencies.** Every dependency is audited and
   justified. The whole engine currently has one third-party dep
   (`BurntSushi/toml`, used only by config).
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
via a thin native-messaging host (`gidm-host`). The engine never imports the
daemon/clients and persists only through the `engine.Store` interface.

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
- **M1** core engine: segmented download, resume, retries, integrity, SQLite store.
- **M2** daemon + CLI.
- **M3** dynamic segmentation (work-stealing), rate limiting, scheduler.
- **M4** Chrome extension + native-messaging host.
- **M5** Wails + Svelte desktop app (`go.work` + `desktop/` module added here).
