// Package engine is gidm's core download engine: a pure library that probes
// URLs, plans byte-range segments, transfers them concurrently, resumes from
// persisted progress, and verifies integrity. It depends on the outside world
// only through the Store interface.
//
// Hot-path invariant: the per-chunk read/write loop allocates zero bytes per
// iteration (pooled buffers + io.CopyBuffer), enforced by a -benchmem benchmark.
package engine
