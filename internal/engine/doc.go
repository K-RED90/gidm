// Package engine is gidm's core download engine: a pure library that probes
// URLs, plans byte-range segments, transfers them concurrently, resumes from
// persisted progress, and verifies integrity. It depends on the outside world
// only through two interfaces — the Fetcher HTTP port and the Store — so it
// imports no adapter (and never internal/httpx, internal/store, or any client).
//
// Hot-path invariant: the per-chunk read/write loop allocates zero bytes per
// iteration (pooled buffers + io.CopyBuffer over an offsetWriter, never *os.File
// directly), enforced by a -benchmem benchmark. The loop is also lock-free: a
// worker advances its segment's progress through a per-index atomic counter, so
// the N segment goroutines never contend on a shared mutex on the hot path.
//
// State-ownership invariant: a per-segment atomic counter (segProgress, indexed
// like Download.Segments) is the single source of truth for live progress. Each
// worker owns one index and writes only that counter; both the periodic
// checkpoint and the terminal SaveDownload fold those live values into the
// persisted segments, so persistence never clobbers checkpointed progress to zero.
//
// Durability invariant: on resume the persisted checkpoint is reconciled against
// the actual .part file before any byte is trusted. A missing, truncated, or
// desynced .part clamps each segment's Completed to what the file really backs (a
// missing file restarts from scratch), so a skipped Done() region can never become
// a zero-filled hole in the renamed output. After a clean transfer the engine
// asserts every ranged segment reached its end (and, for a known size, that the
// bytes sum to TotalSize) before the .part is renamed, so a body ending in a clean
// EOF short of its range fails the download instead of renaming a truncated file.
package engine
