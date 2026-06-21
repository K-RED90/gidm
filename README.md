# gidm

A fast, open-source download manager written in Go — a free alternative to IDM,
built around segmented (parallel) downloading.

> **Status: M0 — foundation.** Project structure, configuration, and tooling are
> in place. The download engine is not implemented yet. See the roadmap below.

## Architecture

A pure-Go **engine** library is wrapped by a background **daemon** that exposes a
control API over a Unix domain socket. The **CLI**, **desktop app** (Wails +
Svelte), and **Chrome extension** are all clients of that daemon; the extension
reaches it through a thin native-messaging host.

```
extension ─▶ gidm-host ─┐
cli ────────────────────┼─▶ gidmd (daemon) ─▶ engine ─▶ store (SQLite) + httpx
desktop ────────────────┘
```

## Layout

| Path | Purpose |
|------|---------|
| `internal/engine` | core download engine (pure library) |
| `internal/config` | layered configuration (defaults → file → env → flags) |
| `internal/store/sqlite` | SQLite persistence (M1) |
| `internal/httpx` | ranged requests, retries, redirect policy (M1) |
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
make check   # vet + test
```

## Configuration

Nothing is hardcoded. Settings resolve in order (later wins): built-in defaults →
TOML config file → `GIDM_*` environment variables → command-line flags. Copy
`configs/gidm.example.toml` to your OS config dir, or point `$GIDM_CONFIG` at it.

## Roadmap

- **M0** — foundation (this) ✅
- **M1** — core engine: segmented download, resume, retries, integrity, SQLite store
- **M2** — daemon + CLI
- **M3** — dynamic segmentation, rate limiting, scheduler
- **M4** — Chrome extension + native-messaging host
- **M5** — Wails + Svelte desktop app

## License

[Apache-2.0](LICENSE).
