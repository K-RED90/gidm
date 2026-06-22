package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// partSuffix names the in-progress file; it is renamed to the final destination
// only after every segment completes and integrity passes.
const partSuffix = ".part"

// Download fetches url into the engine's download directory and returns the
// resulting Download record. It probes the URL, plans segments, transfers them
// concurrently (bounded by SegmentsPerDownload), checkpoints resume progress,
// verifies any configured checksum, and atomically renames the .part file into
// place. It resumes a matching persisted download whose validators still match,
// after reconciling the persisted progress against the actual .part file.
func (e *Engine) Download(ctx context.Context, url string) (*Download, error) {
	probe, err := e.fetcher.Probe(ctx, url, RequestOptions{})
	if err != nil {
		return nil, fmt.Errorf("engine: probe %q: %w", url, err)
	}

	fetchURL := probe.FinalURL
	if fetchURL == "" {
		fetchURL = url
	}

	dest := e.destPath(probe, fetchURL)
	dir := e.dlDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("engine: create download dir %q: %w", dir, err)
	}

	dl, fresh, err := e.prepareDownload(ctx, url, fetchURL, dest, probe)
	if err != nil {
		return nil, err
	}

	return e.transfer(ctx, dl, fresh)
}

// Run drives a download for a record the caller already minted and persisted
// (with its own ID), which is how Manager controls IDs and lifecycle. It probes
// dl.URL, resolves the destination, reconciles dl against the fresh probe
// (planning segments on a first run and restarting cleanly if the remote file
// changed), persists the record active, then runs the same transfer pipeline as
// Download. The passed dl is mutated in place and the persisted snapshot is
// returned. Unlike Download, Run never matches by destination — the record's ID
// is authoritative — so two callers must not Run records targeting the same
// destination concurrently (Manager serializes that via its worker pool).
func (e *Engine) Run(ctx context.Context, dl *Download) (*Download, error) {
	probe, err := e.fetcher.Probe(ctx, dl.URL, requestOptions(dl))
	if err != nil {
		return nil, fmt.Errorf("engine: probe %q: %w", dl.URL, err)
	}

	fetchURL := probe.FinalURL
	if fetchURL == "" {
		fetchURL = dl.URL
	}

	// Honor a caller-resolved destination (set by Submit from add options);
	// otherwise derive it from the probe, which can use the server-suggested name.
	dest := dl.Destination
	if dest == "" {
		dest = e.destPath(probe, fetchURL)
	}
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("engine: create download dir %q: %w", dir, err)
	}

	fresh := e.resolveRecord(dl, fetchURL, dest, probe)
	dl.Status = StatusActive
	dl.UpdatedAt = time.Now().UTC()
	if err := e.store.SaveDownload(ctx, dl); err != nil {
		return nil, fmt.Errorf("engine: persist run %q: %w", dl.ID, err)
	}

	return e.transfer(ctx, dl, fresh)
}

// resolveRecord reconciles a caller-owned record against a fresh probe in place,
// reporting whether the .part file should be (re)created from scratch. A record
// with no segments is a first run (plan from the probe, fresh). A record whose
// validators no longer match the remote (the file changed) is restarted: its
// segments are re-planned and its progress discarded. Otherwise the persisted
// segments are kept and the run resumes from their checkpoints.
func (e *Engine) resolveRecord(dl *Download, fetchURL, dest string, probe ProbeInfo) (fresh bool) {
	dl.URL = fetchURL
	dl.Destination = dest

	if len(dl.Segments) == 0 {
		dl.TotalSize = probe.Size
		dl.ETag = probe.ETag
		dl.LastModified = probe.LastModified
		dl.Segments = e.planFor(e.segmentsFor(dl), probe)
		return true
	}

	if !validatorsMatch(dl, probe) {
		dl.TotalSize = probe.Size
		dl.ETag = probe.ETag
		dl.LastModified = probe.LastModified
		dl.Segments = e.planFor(e.segmentsFor(dl), probe)
		return true
	}

	return false
}

// transfer runs the shared download pipeline for a record already resolved and
// persisted active: open the .part, reconcile progress against the file, fan out
// the segments, then sync/checksum/finalize, mapping the outcome onto a terminal
// persisted record. It is the common tail of both Download and Run.
func (e *Engine) transfer(ctx context.Context, dl *Download, fresh bool) (*Download, error) {
	dest := dl.Destination
	partPath := dest + partSuffix
	part, err := openPart(partPath, dl, fresh)
	if err != nil {
		return nil, err
	}

	// Reconcile persisted segment progress with what the .part file actually backs
	// before trusting any checkpoint. A missing, truncated, or desynced .part would
	// otherwise leave skipped Done() regions as zero-filled holes in the renamed
	// file (silent corruption on resume). prog becomes the live source of truth.
	if err := reconcileProgress(dl, part, fresh); err != nil {
		_ = part.Close()
		return nil, err
	}
	// Pre-size the live counters to the work-stealing slot ceiling (just the segment
	// count when stealing is off) so steals consume already-allocated slots and the
	// observer's backing array is never reallocated under it.
	prog := newSegProgressSized(dl.Segments, e.maxSlots(dl))
	e.observeStart(dl.ID, prog)
	defer e.observeStop(dl.ID)

	// Per-download bandwidth cap: one bucket shared by this download's segments,
	// independent of the engine-wide globalLimiter. The bucket lives behind an atomic
	// pointer so Manager.SetRate can swap it live (nil = no cap, a true no-op at the
	// flush boundary). It is registered for the run's duration so SetDownloadRate can
	// find it, and threaded down exactly like prog.
	limiter := new(atomic.Pointer[rateLimiter])
	if rate := int64(e.effectiveDownloadRate(dl.MaxRate)); rate > 0 {
		limiter.Store(newRateLimiter(rate, rateBurst(rate, e.bufSize(), int64(e.cfg.RateBurst))))
	}
	e.activeLimiters.Store(dl.ID, limiter)
	defer e.activeLimiters.Delete(dl.ID)

	runErr := e.runSegments(ctx, dl, part, prog, limiter)

	if runErr != nil {
		_ = part.Close()
		return e.finishFailed(ctx, dl, prog, runErr)
	}

	if err := part.Sync(); err != nil {
		_ = part.Close()
		return e.finishFailed(ctx, dl, prog, fmt.Errorf("engine: sync %q: %w", partPath, err))
	}
	if err := part.Close(); err != nil {
		return e.finishFailed(ctx, dl, prog, fmt.Errorf("engine: close %q: %w", partPath, err))
	}

	if _, hexSum, ok := parseChecksum(dl.Checksum); ok {
		if err := verifyChecksum(ctx, partPath, hexSum); err != nil {
			_ = os.Remove(partPath)
			return e.finishFailed(ctx, dl, prog, err)
		}
	}

	if err := finalizeRename(partPath, dest); err != nil {
		return e.finishFailed(ctx, dl, prog, err)
	}

	return e.finishCompleted(ctx, dl, prog)
}

// prepareDownload resolves the Download record to run: it reuses a persisted one
// whose validators still match the fresh probe (resuming its segments), and
// otherwise builds a new one and persists it active. fresh reports whether the
// .part file should be created/truncated from scratch.
func (e *Engine) prepareDownload(ctx context.Context, url, fetchURL, dest string, probe ProbeInfo) (dl *Download, fresh bool, err error) {
	if existing := e.loadResumable(ctx, dest, probe); existing != nil {
		existing.URL = fetchURL
		existing.Status = StatusActive
		existing.UpdatedAt = time.Now().UTC()
		if err := e.store.SaveDownload(ctx, existing); err != nil {
			return nil, false, fmt.Errorf("engine: persist resume %q: %w", existing.ID, err)
		}
		return existing, false, nil
	}

	id, err := newID()
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC()
	dl = &Download{
		ID:           id,
		URL:          fetchURL,
		Destination:  dest,
		TotalSize:    probe.Size,
		Status:       StatusActive,
		ETag:         probe.ETag,
		LastModified: probe.LastModified,
		Segments:     e.planFor(e.defaultSegmentCount(), probe),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := e.store.SaveDownload(ctx, dl); err != nil {
		return nil, false, fmt.Errorf("engine: persist download %q: %w", dl.ID, err)
	}
	return dl, true, nil
}

// loadResumable returns a persisted, still-valid download for dest, or nil to
// start fresh. A changed ETag/Last-Modified (the remote file changed) discards
// the persisted progress so the download restarts cleanly.
func (e *Engine) loadResumable(ctx context.Context, dest string, probe ProbeInfo) *Download {
	list, err := e.store.ListDownloads(ctx)
	if err != nil {
		return nil
	}
	for _, d := range list {
		if d.Destination != dest {
			continue
		}
		if d.Status == StatusCompleted {
			continue
		}
		if !validatorsMatch(d, probe) {
			return nil
		}
		return d
	}
	return nil
}

// validatorsMatch reports whether a persisted download still describes the same
// remote bytes. An empty validator on either side is treated conservatively as a
// match only when both validators are empty AND the size is unchanged, so a
// server that drops validators does not force needless restarts.
func validatorsMatch(d *Download, probe ProbeInfo) bool {
	if d.ETag != "" || probe.ETag != "" {
		return d.ETag == probe.ETag
	}
	if d.LastModified != "" || probe.LastModified != "" {
		return d.LastModified == probe.LastModified
	}
	return d.TotalSize == probe.Size
}

// reconcileProgress builds the live progress counters for dl after clamping every
// persisted Completed to what the .part file on disk actually backs. The store
// checkpoint and the file can desync after a crash (UpdateSegment may persist
// bytes whose fsync never landed, or the file may be externally truncated/removed),
// and openPart's O_CREATE silently substitutes an empty file for a missing one. If
// the engine trusted the checkpoint blindly, the skipped Done() regions would be
// zero-filled holes in the renamed output.
//
// For a fresh run there is nothing on disk to honor, so all counters start at zero.
// For a resume, each segment's Completed is capped so Start+Completed never exceeds
// the on-disk size, and for a known-size run the file is re-grown to at least
// TotalSize so every WriteAt offset lands in an allocated region. It mutates
// dl.Segments in place; the caller builds the live counters from the reconciled set.
func reconcileProgress(dl *Download, part *os.File, fresh bool) error {
	if fresh {
		for i := range dl.Segments {
			dl.Segments[i].Completed = 0
		}
		return nil
	}

	info, err := part.Stat()
	if err != nil {
		return fmt.Errorf("engine: stat part: %w", err)
	}
	onDisk := info.Size()

	// Known-size ranged path: ensure the file is at least TotalSize so per-segment
	// WriteAt offsets always land in allocated space. A short file (external
	// truncation, partial prior run) is grown back; the clamp below makes any region
	// past onDisk be re-fetched rather than trusted as zero-filled.
	if dl.TotalSize > 0 && onDisk < dl.TotalSize {
		if err := part.Truncate(dl.TotalSize); err != nil {
			return fmt.Errorf("engine: resize part on resume: %w", err)
		}
	}

	// Heal any non-tiling segment set a torn work-stealing checkpoint may have left
	// (a gap or overlap between a shrunk donor and its tail) so resume always
	// reconstructs a clean cover of [0,TotalSize). A no-op for an untouched layout.
	repairLayout(dl)

	for i := range dl.Segments {
		seg := dl.Segments[i]
		if seg.End < 0 {
			// Whole-body fallback: no resume, the file streams from 0 each run.
			dl.Segments[i].Completed = 0
			continue
		}
		// Cap Completed so Start+Completed never exceeds the bytes the file actually
		// backed before any re-truncate. Bytes beyond that point are not durable, so
		// they are re-fetched instead of skipped as zero-filled holes.
		backed := onDisk - seg.Start
		if backed < 0 {
			backed = 0
		}
		if seg.Completed > backed {
			dl.Segments[i].Completed = backed
		}
	}
	return nil
}

// repairLayout rewrites a resumed download's segments into a clean tiling of
// [0,TotalSize): segments are sorted by Start, each End is re-derived from the next
// segment's Start (the last from TotalSize), indices are renumbered densely, and
// Completed is clamped to the repaired size. Work-stealing persists a split as two
// separate segment checkpoints (the shrunk donor and the new tail); a crash between
// them — or a stale donor checkpoint racing the steal — could leave the store with a
// gap or overlap that would otherwise wedge resume forever. Re-deriving the cover
// from the segment starts heals it while re-fetching only the affected bytes. It is
// a no-op for an already-tiling layout and is skipped for the whole-body fallback
// and unknown sizes, which have no fixed ranges to reconcile.
func repairLayout(dl *Download) {
	if dl.TotalSize <= 0 || len(dl.Segments) < 2 {
		return
	}
	for _, s := range dl.Segments {
		if s.End < 0 {
			return // an open-ended segment has no boundary to tile against
		}
	}
	sort.Slice(dl.Segments, func(i, j int) bool { return dl.Segments[i].Start < dl.Segments[j].Start })
	for i := range dl.Segments {
		end := dl.TotalSize - 1
		if i+1 < len(dl.Segments) {
			end = dl.Segments[i+1].Start - 1
		}
		dl.Segments[i].Index = i
		dl.Segments[i].End = end
		if size := end - dl.Segments[i].Start + 1; dl.Segments[i].Completed > size {
			dl.Segments[i].Completed = size
		}
		if dl.Segments[i].Completed < 0 {
			dl.Segments[i].Completed = 0
		}
	}
}

// runSegments transfers a download's segments and returns the first error. A
// whole-body fallback (a single open-ended segment) runs on its own path; otherwise
// it dispatches to the static fan-out or the work-stealing pool per config. After a
// clean run it asserts every ranged segment is fully backed (and the bytes sum to a
// known TotalSize) so no truncated .part is ever renamed.
func (e *Engine) runSegments(ctx context.Context, dl *Download, part *os.File, prog *segProgress, limiter *atomic.Pointer[rateLimiter]) error {
	// Single open-ended segment: unknown size or no range support.
	if len(dl.Segments) == 1 && dl.Segments[0].End < 0 {
		runCtx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		return e.runWholeBody(runCtx, dl, part, prog, limiter)
	}
	if e.cfg.WorkStealing {
		return e.runStealing(ctx, dl, part, prog, limiter)
	}
	return e.runStatic(ctx, dl, part, prog, limiter)
}

// runStatic is the fixed segmentation: one goroutine per incomplete segment,
// bounded by SegmentsPerDownload, ranges immutable for the run. On any error all
// siblings are cancelled via the cause context.
func (e *Engine) runStatic(ctx context.Context, dl *Download, part *os.File, prog *segProgress, limiter *atomic.Pointer[rateLimiter]) error {
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	sem := make(chan struct{}, segLimit(e.segmentsFor(dl)))

	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	fail := func(err error) {
		once.Do(func() {
			first = err
			cancel(err)
		})
	}

	for i := range dl.Segments {
		if isDone(dl.Segments[i], prog.load(i)) {
			continue
		}

		select {
		case <-runCtx.Done():
			fail(context.Cause(runCtx))
		case sem <- struct{}{}:
		}
		if runCtx.Err() != nil {
			break
		}

		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := e.runSegment(runCtx, dl, idx, part, prog, nil, limiter); err != nil {
				fail(err)
			}
		}(i) // nil coord: static, immutable ranges
	}

	wg.Wait()
	if first != nil {
		return first
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Completeness gate: a clean wait is not proof of a whole file. Assert every
	// ranged segment is fully backed and, for a known size, that the bytes sum to
	// TotalSize before the caller is allowed to rename the .part into place.
	return verifyComplete(dl, prog)
}

// runStealing fans out a fixed pool of SegmentsPerDownload workers over a live,
// rebalancing segmentation. A worker that finishes its segment steals the unfetched
// tail of the in-flight segment with the most bytes left, so fast connections
// absorb a straggler's remainder instead of idling. The plan and counters are
// pre-sized to the slot ceiling so a steal never reallocates the arrays the
// Manager's observer reads. After the pool drains, the durable segment slice is
// rebuilt from the final layout for the completeness gate and the persisted record.
func (e *Engine) runStealing(ctx context.Context, dl *Download, part *os.File, prog *segProgress, limiter *atomic.Pointer[rateLimiter]) error {
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	n := len(dl.Segments)
	maxSlots := len(prog.completed)
	plan := newLivePlan(dl.Segments, maxSlots)
	margin := int64(e.cfg.BufferSize)
	coord := newStealCoord(plan, prog, n, maxSlots, e.stealFloor(), margin)

	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	fail := func(err error) {
		once.Do(func() {
			first = err
			cancel(err)
		})
	}

	for w := 0; w < segLimit(e.segmentsFor(dl)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for runCtx.Err() == nil {
				idx, steal, ok := coord.next()
				if !ok {
					return // no work and nothing worth stealing; remainders only shrink
				}
				if steal != nil {
					// Persist the split off the coordinator mutex so a slow store never
					// stalls dispatch; a torn write is healed by repairLayout on resume.
					// Detach from runCtx so a cancel racing these writes cannot abort one
					// mid-query and wedge the connection; the cap bounds a stuck store.
					persistCtx, pc := context.WithTimeout(context.WithoutCancel(runCtx), checkpointWriteTimeout)
					_ = e.store.UpdateSegment(persistCtx, dl.ID, steal.donor)
					_ = e.store.UpdateSegment(persistCtx, dl.ID, steal.tail)
					pc()
				}
				if err := e.runSegment(runCtx, dl, idx, part, prog, coord, limiter); err != nil {
					fail(err)
					return
				}
			}
		}()
	}

	wg.Wait()

	// All workers have stopped: rebuild the durable shape from the final live plan
	// (no concurrency now) so verifyComplete and the persisted record reflect every
	// steal's donor shrink and new tail.
	dl.Segments = coord.activeSegments()

	if first != nil {
		return first
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return verifyComplete(dl, prog)
}

// segLimit clamps the configured per-download parallelism to at least one worker.
func segLimit(segmentsPerDownload int) int {
	if segmentsPerDownload < 1 {
		return 1
	}
	return segmentsPerDownload
}

// maxSlots is the upper bound on a download's live segment count: the segment count
// when work-stealing is off, otherwise the configured ceiling (never below the
// initial count). It sizes the pre-allocated, never-reallocated counter and plan
// arrays.
func (e *Engine) maxSlots(dl *Download) int {
	n := len(dl.Segments)
	if !e.cfg.WorkStealing || (n == 1 && dl.Segments[0].End < 0) {
		return n
	}
	if e.cfg.MaxSegments > n {
		return e.cfg.MaxSegments
	}
	return n
}

// stealFloor is the minimum piece size a split may produce. It floors the
// configured MinStealSize at 2*BufferSize+1 so that, even at the floor, the split
// point sits a full safety margin (one transfer buffer) beyond a donor's frontier;
// this keeps the steal-safety margin strictly positive regardless of BufferSize.
func (e *Engine) stealFloor() int64 {
	floor := int64(e.cfg.MinStealSize)
	if lo := 2*int64(e.cfg.BufferSize) + 1; floor < lo {
		floor = lo
	}
	return floor
}

// verifyComplete asserts the transfer actually covered the whole file: every
// ranged segment reached its end and, when the size is known, the summed bytes
// equal TotalSize. It guards against a body that ends in a clean io.EOF short of
// the requested range, which would otherwise rename a truncated file.
func verifyComplete(dl *Download, prog *segProgress) error {
	var total int64
	for i := range dl.Segments {
		seg := dl.Segments[i]
		done := prog.load(i)
		total += done
		if seg.End >= 0 && done < seg.Size() {
			return fmt.Errorf("engine: segment %d incomplete: %d of %d bytes", i, done, seg.Size())
		}
	}
	if dl.TotalSize > 0 && total != dl.TotalSize {
		return fmt.Errorf("engine: incomplete transfer: %d of %d bytes", total, dl.TotalSize)
	}
	return nil
}

// finishCompleted marks the download completed and persists it, folding the live
// segment counters into the snapshot so the persisted record reflects real
// Completed values.
func (e *Engine) finishCompleted(ctx context.Context, dl *Download, prog *segProgress) (*Download, error) {
	dl.Status = StatusCompleted
	dl.UpdatedAt = time.Now().UTC()
	snapshot := cloneForPersist(dl, prog)

	if err := e.store.SaveDownload(ctx, snapshot); err != nil {
		return nil, fmt.Errorf("engine: persist completion %q: %w", dl.ID, err)
	}
	return snapshot, nil
}

// finishFailed marks the download failed, persists it (best effort) carrying live
// progress, and returns the originating error. The .part file is left in place so
// a later run can resume; no final file exists.
func (e *Engine) finishFailed(ctx context.Context, dl *Download, prog *segProgress, cause error) (*Download, error) {
	dl.Status = StatusFailed
	dl.UpdatedAt = time.Now().UTC()
	snapshot := cloneForPersist(dl, prog)

	// Persist failure with a fresh context so an aborted parent ctx does not also
	// drop the bookkeeping; cap it so a wedged store cannot hang the caller.
	persistCtx, pc := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer pc()
	_ = e.store.SaveDownload(persistCtx, snapshot)
	return nil, cause
}

// cloneForPersist copies the download (and its segment slice) with the live
// progress counters folded into each segment's Completed, so the persisted
// snapshot carries real progress and is immune to further mutation by workers.
func cloneForPersist(dl *Download, prog *segProgress) *Download {
	cp := *dl
	cp.Segments = append([]Segment(nil), dl.Segments...)
	prog.snapshotInto(cp.Segments)
	return &cp
}

// planFor chooses the segment layout for a probe. When the server supports
// ranges and discloses a size, it splits into segments ranged pieces (the
// per-download override or the configured default, resolved by the caller);
// otherwise it returns one open-ended segment (End == -1) that the whole-body
// Get path streams sequentially, since ranged GETs are unusable.
func (e *Engine) planFor(segments int, probe ProbeInfo) []Segment {
	if !probe.SupportsRanges || probe.Size <= 0 {
		return []Segment{{Index: 0, Start: 0, End: -1}}
	}
	return planSegments(probe.Size, segments)
}

// openPart opens (and, when fresh and sized, preallocates) the .part file. For a
// known total size it reserves TotalSize up front so each segment can WriteAt its
// own offset into a pre-sized file — reserving real blocks where the platform
// supports it, so a disk that is too small fails here rather than mid-download.
// For an unknown size it grows sequentially.
func openPart(path string, dl *Download, fresh bool) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("engine: open part %q: %w", path, err)
	}
	if fresh && dl.TotalSize > 0 {
		if err := preallocate(f, dl.TotalSize); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("engine: size part %q: %w", path, err)
		}
	}
	return f, nil
}

// finalizeRename atomically moves the completed .part into place and fsyncs the
// parent directory so the rename survives a crash.
func finalizeRename(partPath, dest string) error {
	if err := os.Rename(partPath, dest); err != nil {
		return fmt.Errorf("engine: rename %q -> %q: %w", partPath, dest, err)
	}
	if err := syncDir(filepath.Dir(dest)); err != nil {
		return fmt.Errorf("engine: sync dest dir: %w", err)
	}
	return nil
}

// newID returns a random 16-byte hex download identifier.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("engine: generate id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
