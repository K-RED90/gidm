package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	probe, err := e.fetcher.Probe(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("engine: probe %q: %w", url, err)
	}

	fetchURL := probe.FinalURL
	if fetchURL == "" {
		fetchURL = url
	}

	dest := e.destPath(probe, fetchURL)
	if err := os.MkdirAll(e.downloadDir, 0o755); err != nil {
		return nil, fmt.Errorf("engine: create download dir %q: %w", e.downloadDir, err)
	}

	dl, fresh, err := e.prepareDownload(ctx, url, fetchURL, dest, probe)
	if err != nil {
		return nil, err
	}

	partPath := dest + partSuffix
	part, err := openPart(partPath, dl, fresh)
	if err != nil {
		return nil, err
	}

	// Reconcile persisted segment progress with what the .part file actually backs
	// before trusting any checkpoint. A missing, truncated, or desynced .part would
	// otherwise leave skipped Done() regions as zero-filled holes in the renamed
	// file (silent corruption on resume). prog becomes the live source of truth.
	prog, err := reconcileProgress(dl, part, fresh)
	if err != nil {
		_ = part.Close()
		return nil, err
	}

	runErr := e.runSegments(ctx, dl, part, prog)

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
		Segments:     e.planFor(probe),
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
// TotalSize so every WriteAt offset lands in an allocated region.
func reconcileProgress(dl *Download, part *os.File, fresh bool) (*segProgress, error) {
	if fresh {
		for i := range dl.Segments {
			dl.Segments[i].Completed = 0
		}
		return newSegProgress(dl.Segments), nil
	}

	info, err := part.Stat()
	if err != nil {
		return nil, fmt.Errorf("engine: stat part: %w", err)
	}
	onDisk := info.Size()

	// Known-size ranged path: ensure the file is at least TotalSize so per-segment
	// WriteAt offsets always land in allocated space. A short file (external
	// truncation, partial prior run) is grown back; the clamp below makes any region
	// past onDisk be re-fetched rather than trusted as zero-filled.
	if dl.TotalSize > 0 && onDisk < dl.TotalSize {
		if err := part.Truncate(dl.TotalSize); err != nil {
			return nil, fmt.Errorf("engine: resize part on resume: %w", err)
		}
	}

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
	return newSegProgress(dl.Segments), nil
}

// runSegments fans out one goroutine per incomplete segment, bounded by
// SegmentsPerDownload, and returns the first error. A whole-body fallback (a
// single open-ended segment) runs on its own path. On any error all siblings are
// cancelled via the cause context. After a clean wait it asserts every ranged
// segment is fully backed (and the summed bytes equal a known TotalSize) so no
// truncated .part is ever renamed.
func (e *Engine) runSegments(ctx context.Context, dl *Download, part *os.File, prog *segProgress) error {
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	// Single open-ended segment: unknown size or no range support.
	if len(dl.Segments) == 1 && dl.Segments[0].End < 0 {
		return e.runWholeBody(runCtx, dl, part, prog)
	}

	limit := e.cfg.SegmentsPerDownload
	if limit < 1 {
		limit = 1
	}
	sem := make(chan struct{}, limit)

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
			if err := e.runSegment(runCtx, dl, idx, part, prog); err != nil {
				fail(err)
			}
		}(i)
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
// ranges and discloses a size, it splits into SegmentsPerDownload ranged
// segments; otherwise it returns one open-ended segment (End == -1) that the
// whole-body Get path streams sequentially, since ranged GETs are unusable.
func (e *Engine) planFor(probe ProbeInfo) []Segment {
	if !probe.SupportsRanges || probe.Size <= 0 {
		return []Segment{{Index: 0, Start: 0, End: -1}}
	}
	return planSegments(probe.Size, e.cfg.SegmentsPerDownload)
}

// openPart opens (and, when fresh and sized, truncates) the .part file. For a
// known total size it is truncated to TotalSize so each segment can WriteAt its
// own offset into a pre-sized file; for an unknown size it grows sequentially.
func openPart(path string, dl *Download, fresh bool) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("engine: open part %q: %w", path, err)
	}
	if fresh && dl.TotalSize > 0 {
		if err := f.Truncate(dl.TotalSize); err != nil {
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
	dir, err := os.Open(filepath.Dir(dest))
	if err != nil {
		return fmt.Errorf("engine: open dest dir: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
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
