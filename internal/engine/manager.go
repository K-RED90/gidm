package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Manager is the M2 supervisor: it turns the engine's one-shot, blocking
// Download into managed, concurrent, controllable background jobs. It lives in
// package engine so it reuses Engine/Store/Download/Status directly and keeps
// the engine's dependency invariant trivially intact (engine imports only the
// standard library and internal/config).
//
// Concurrency model. A bounded worker pool of MaxConcurrent goroutines pulls from
// a priority-ordered pending set; at most MaxConcurrent downloads are active at
// once while the rest wait, and a freeing slot always picks up the
// highest-priority queued download (ties broken by enqueue order — stable FIFO).
// (MaxConcurrent bounds downloads; the engine's SegmentsPerDownload independently
// bounds the segment goroutines within one download — the two knobs are
// distinct.) All mutable Manager state is guarded by a single mutex: the pending
// set and its FIFO sequence counter, the per-job cancel functions (exactly one
// per active job), the started/closed flags, and the live-progress registry;
// idle workers wait on a sync.Cond over that same mutex. No package-level mutable
// state exists; the engine's progressObserver hook is registered once at
// construction.
//
// Lifecycle state machine:
//
//	Submit              -> queued
//	worker picks up     -> active
//	transfer completes  -> completed
//	Pause               -> paused   (keeps the .part and its checkpoints)
//	Cancel              -> canceled (keeps the .part for a later Resume)
//	transfer errors     -> failed
//	Resume (from paused/canceled/failed/completed) -> queued
//
// Crash recovery. Start loads the store and re-enqueues every download left
// active or queued (a crash interrupted them mid-flight); the engine resumes
// each from its persisted checkpoints. If a record's validators no longer match
// on re-probe, the engine restarts that download cleanly (the remote changed) —
// expected, not data loss.
//
// Graceful shutdown. Shutdown stops intake (no new jobs are dequeued), cancels
// every in-flight transfer through its context, and waits for the workers to
// drain — bounded by the caller's context so a shutdown never hangs. Persisted
// state stays consistent and .part files are left intact, so a later Start (or
// Resume) picks up exactly where the transfers were interrupted. No goroutine or
// open file handle leaks: the engine closes its *os.File on every exit path and
// every worker returns once the manager is closed.
type Manager struct {
	engine *Engine
	store  Store
	max    int

	// logger records bookkeeping failures that the API cannot surface to a caller
	// — most importantly a failed status-override persist during shutdown, which
	// would otherwise leave a record stuck at failed and unrecoverable. It defaults
	// to slog.Default() (std-lib only, not a config tunable) and is set once at
	// construction, so it is read-only thereafter and needs no lock.
	logger *slog.Logger

	mu       sync.Mutex
	cond     *sync.Cond // signalled when work is enqueued or the manager closes
	pending  []pendingItem
	seq      uint64 // monotonic enqueue counter; the stable FIFO tiebreak
	started  bool
	closed   bool
	cancels  map[string]context.CancelFunc // one per active job
	intents  map[string]Status             // operator intent (paused/canceled) for an in-flight job
	live     map[string]*segProgress       // live counters for active jobs
	baseCtx  context.Context
	baseStop context.CancelFunc
	workers  sync.WaitGroup
}

// pendingItem is one queued download awaiting a free slot. seq orders items of
// equal priority by enqueue time, so the dispatch tiebreak is a stable FIFO.
type pendingItem struct {
	id       string
	priority Priority
	seq      uint64
}

// NewManager builds a supervisor over an engine and its store, bounding
// concurrent downloads by maxConcurrent (typically cfg.Download.MaxConcurrent).
// It registers the Manager as the engine's live-progress observer. It accepts
// interfaces and returns a concrete struct; it creates no goroutines and holds
// no package-level state until Start is called.
func NewManager(e *Engine, store Store, maxConcurrent int) *Manager {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	m := &Manager{
		engine:  e,
		store:   store,
		max:     maxConcurrent,
		logger:  slog.Default(),
		cancels: make(map[string]context.CancelFunc),
		intents: make(map[string]Status),
		live:    make(map[string]*segProgress),
	}
	m.cond = sync.NewCond(&m.mu)
	e.observer = m
	return m
}

// SetLogger overrides the logger used for bookkeeping failures the API cannot
// return to a caller (e.g. a failed shutdown status-override persist). It must be
// called before Start; passing nil is a no-op. Construction defaults to
// slog.Default(), so wiring a logger is optional.
func (m *Manager) SetLogger(l *slog.Logger) {
	if l != nil {
		m.logger = l
	}
}

// ErrManagerClosed is returned by API calls after Shutdown, so callers see a
// wrapped error instead of a panic on a closed queue.
var ErrManagerClosed = errors.New("engine: manager is shut down")

// Start performs crash recovery and then launches the worker pool. It derives the
// Manager's base context from ctx (cancelling it on Shutdown aborts every
// in-flight transfer), re-enqueues every persisted download still active or
// queued, and only then spawns the workers — so they pull from a fully seeded,
// priority-ordered pending set and recovery dispatch is deterministic by priority
// (rather than racing the recovery sweep). Calling Start more than once, or after
// Shutdown, returns an error.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	if m.started {
		m.mu.Unlock()
		return errors.New("engine: manager already started")
	}
	m.started = true
	m.baseCtx, m.baseStop = context.WithCancel(context.WithoutCancel(ctx))
	m.mu.Unlock()

	// Seed the pending set before any worker exists so the first slots go to the
	// highest-priority recovered downloads; a recover error leaves no idle workers.
	if err := m.recover(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	for i := 0; i < m.max; i++ {
		m.workers.Add(1)
		go m.worker()
	}
	m.mu.Unlock()
	return nil
}

// recover re-enqueues downloads a crash or prior shutdown left in flight so they
// resume from their checkpoints. Only active/queued records are re-enqueued;
// completed/paused/canceled/failed records wait for an explicit Resume.
func (m *Manager) recover(ctx context.Context) error {
	list, err := m.store.ListDownloads(ctx)
	if err != nil {
		return fmt.Errorf("engine: manager recover: %w", err)
	}
	for _, d := range list {
		if d.Status == StatusActive || d.Status == StatusQueued {
			if err := m.enqueue(d.ID, d.Priority); err != nil {
				return err
			}
		}
	}
	return nil
}

// Submit persists a new queued download at the given priority and enqueues it,
// returning its ID immediately without blocking on the transfer.
func (m *Manager) Submit(ctx context.Context, url string, priority Priority) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	dl := &Download{
		ID:        id,
		URL:       url,
		Status:    StatusQueued,
		Priority:  priority,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := m.store.SaveDownload(ctx, dl); err != nil {
		return "", fmt.Errorf("engine: manager submit %q: %w", url, err)
	}
	if err := m.enqueue(id, priority); err != nil {
		return "", err
	}
	return id, nil
}

// enqueue adds a job to the priority-ordered pending set and wakes one idle
// worker. The closed check, the append, and the signal all happen under the
// mutex that Shutdown also holds, so an enqueue can never race a close. It
// returns ErrManagerClosed after Shutdown. The set is unbounded, so a burst of
// Submits or a recovery sweep never blocks the caller.
func (m *Manager) enqueue(id string, priority Priority) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrManagerClosed
	}
	m.seq++
	m.pending = append(m.pending, pendingItem{id: id, priority: priority, seq: m.seq})
	m.cond.Signal()
	return nil
}

// List returns every persisted download with live in-memory progress folded in
// for any that are currently active.
func (m *Manager) List(ctx context.Context) ([]*Download, error) {
	list, err := m.store.ListDownloads(ctx)
	if err != nil {
		return nil, fmt.Errorf("engine: manager list: %w", err)
	}
	for _, d := range list {
		m.foldLiveProgress(d)
	}
	return list, nil
}

// Get returns one persisted download with live in-memory progress folded in if
// it is currently active.
func (m *Manager) Get(ctx context.Context, id string) (*Download, error) {
	d, err := m.store.LoadDownload(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("engine: manager get %q: %w", id, err)
	}
	m.foldLiveProgress(d)
	return d, nil
}

// foldLiveProgress overlays the live atomic counters of an active job onto a
// snapshot's segments, so a poll sees up-to-the-moment bytes without any store
// write. It is O(segments) and takes the mutex only briefly to fetch the
// counters pointer. A job not currently active keeps its persisted Completed.
//
// The counters are pre-sized to the work-stealing slot ceiling, so they can be
// longer than the persisted segment slice while a steal's appended tail is not yet
// checkpointed. snapshotInto reads only the snapshot's prefix, which always pairs
// index-for-index with the live counters (slots are append-only, never reordered),
// so it stays in bounds and coherent; the fold is skipped only if the counters are
// somehow shorter than the snapshot.
func (m *Manager) foldLiveProgress(d *Download) {
	m.mu.Lock()
	prog := m.live[d.ID]
	m.mu.Unlock()
	if prog == nil || len(prog.completed) < len(d.Segments) {
		return
	}
	prog.snapshotInto(d.Segments)
}

// Pause stops the running transfer (if any) but keeps the checkpointed .part
// progress, marking the download paused. A paused download is resumed by Resume.
// Pausing a download that is not running just records the paused status.
func (m *Manager) Pause(ctx context.Context, id string) error {
	return m.stop(ctx, id, StatusPaused)
}

// Cancel stops the running transfer (if any) and marks the download canceled,
// leaving the .part file in place for a later Resume. Cancel is distinct from a
// failure: it is an operator action, not a transfer error.
func (m *Manager) Cancel(ctx context.Context, id string) error {
	return m.stop(ctx, id, StatusCanceled)
}

// stop records the operator's intent (paused/canceled), cancels the in-flight
// context if the job is running, and persists the target status. The intent is
// always recorded under the mutex — even for a job not yet running — so a worker
// that registers its cancel concurrently observes it atomically and never starts
// (or restarts) a transfer the operator already stopped. Recording the intent
// before cancelling also lets the worker classify the cause when Run returns (a
// context cancellation must not be mismarked failed).
func (m *Manager) stop(ctx context.Context, id string, target Status) error {
	d, err := m.store.LoadDownload(ctx, id)
	if err != nil {
		return fmt.Errorf("engine: manager stop %q: %w", id, err)
	}

	m.mu.Lock()
	// Record the intent unconditionally. If a worker is already running this job it
	// will see the intent when Run returns; if a worker registers its cancel after
	// this point it will see the intent before starting the transfer (both checks
	// happen under this same mutex). The persist below covers the not-yet-running
	// case so Get/List reflect the stop immediately.
	m.intents[id] = target
	cancel := m.cancels[id]
	m.mu.Unlock()

	if cancel != nil {
		cancel() // worker will persist the target status when Run returns
		return nil
	}

	d.Status = target
	d.UpdatedAt = time.Now().UTC()
	if err := m.store.SaveDownload(ctx, d); err != nil {
		return fmt.Errorf("engine: manager stop persist %q: %w", id, err)
	}
	return nil
}

// ErrNotResumable is returned by Resume for a download that is not in a resumable
// (terminal) state — already queued, or active in a worker. Resuming an active
// download would risk two workers driving the same destination at once, so it is
// rejected; pause or cancel it first.
var ErrNotResumable = errors.New("engine: download is not resumable")

// Resume re-enqueues a download so the engine resumes it from Start+Completed. It
// is valid only from a settled state (paused, canceled, failed, or completed): an
// already queued or in-flight download is rejected with ErrNotResumable, which
// guarantees a single worker per download and rules out two concurrent runs
// against one destination.
func (m *Manager) Resume(ctx context.Context, id string) error {
	d, err := m.store.LoadDownload(ctx, id)
	if err != nil {
		return fmt.Errorf("engine: manager resume %q: %w", id, err)
	}
	switch d.Status {
	case StatusPaused, StatusCanceled, StatusFailed, StatusCompleted:
	default:
		return fmt.Errorf("%w: %q is %s", ErrNotResumable, id, d.Status)
	}
	// Clear any stale stop-intent so the worker that picks this up runs the
	// transfer instead of immediately honoring a prior pause/cancel.
	m.mu.Lock()
	delete(m.intents, id)
	m.mu.Unlock()

	d.Status = StatusQueued
	d.UpdatedAt = time.Now().UTC()
	if err := m.store.SaveDownload(ctx, d); err != nil {
		return fmt.Errorf("engine: manager resume persist %q: %w", id, err)
	}
	return m.enqueue(id, d.Priority)
}

// SetPriority changes a download's priority and persists it. A queued download is
// reordered immediately: its pending entry is updated, so the next freeing slot
// honors the new priority. An active download is NOT preempted — only its record
// is updated, so the new priority takes effect if it is later re-queued (a
// pause→resume or a crash recovery). Persisting before the in-memory reorder
// keeps the store authoritative even if the job settles concurrently.
func (m *Manager) SetPriority(ctx context.Context, id string, priority Priority) error {
	d, err := m.store.LoadDownload(ctx, id)
	if err != nil {
		return fmt.Errorf("engine: manager set-priority %q: %w", id, err)
	}
	d.Priority = priority
	d.UpdatedAt = time.Now().UTC()
	if err := m.store.SaveDownload(ctx, d); err != nil {
		return fmt.Errorf("engine: manager set-priority persist %q: %w", id, err)
	}

	m.mu.Lock()
	for i := range m.pending {
		if m.pending[i].id == id {
			m.pending[i].priority = priority // reordering is resolved lazily at pop time
			break
		}
	}
	m.mu.Unlock()
	return nil
}

// Shutdown stops intake, cancels every in-flight transfer through the base
// context, and waits for the workers to drain — bounded by ctx so the caller is
// never blocked indefinitely. It is idempotent. After Shutdown the API returns
// ErrManagerClosed.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.cond.Broadcast() // wake every idle worker so it observes closed and returns
	if m.baseStop != nil {
		m.baseStop() // abort in-flight transfers via their contexts
	}
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.workers.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("engine: manager shutdown: %w", ctx.Err())
	}
}

// worker runs one download at a time, always picking the highest-priority queued
// download when a slot frees. It waits on the condition variable while the
// pending set is empty and returns once the manager is closed. Because exactly
// m.max workers run, at most m.max downloads are active at once.
func (m *Manager) worker() {
	defer m.workers.Done()
	for {
		m.mu.Lock()
		for len(m.pending) == 0 && !m.closed {
			m.cond.Wait()
		}
		if m.closed {
			// Shutting down: leave any still-pending items in the store as
			// queued/active so a later Start re-enqueues them; do not drain them here.
			m.mu.Unlock()
			return
		}
		id := m.popHighest()
		m.mu.Unlock()
		m.run(id)
	}
}

// popHighest removes and returns the id of the highest-priority pending item,
// breaking ties by lowest seq (stable FIFO). The caller must hold m.mu and must
// have checked that pending is non-empty. The scan is O(len(pending)) but runs
// only when a slot frees — a cold path well off the per-chunk transfer loop — so
// a linear select beats a heap's per-mutation bookkeeping at this scale.
func (m *Manager) popHighest() string {
	best := 0
	for i := 1; i < len(m.pending); i++ {
		if m.pending[i].priority > m.pending[best].priority ||
			(m.pending[i].priority == m.pending[best].priority && m.pending[i].seq < m.pending[best].seq) {
			best = i
		}
	}
	id := m.pending[best].id
	last := len(m.pending) - 1
	m.pending[best] = m.pending[last]
	m.pending[last] = pendingItem{} // drop the reference so the id can be GC'd
	m.pending = m.pending[:last]
	return id
}

// run executes a single job: it loads the record, marks it active, derives a
// per-job context (a child of the base context, so both Shutdown and Pause/Cancel
// can abort it), registers its cancel func, runs the engine, and finally
// classifies the outcome — overriding the engine's status for a paused or
// canceled job, since Run reports a context cancellation as a failure.
func (m *Manager) run(id string) {
	d, err := m.store.LoadDownload(m.baseCtx, id)
	if err != nil {
		return // record vanished (deleted) or store gone; nothing to run
	}
	if d.Status == StatusCompleted {
		return // already done (e.g. a duplicate enqueue); do not refetch
	}

	jobCtx, cancel := context.WithCancel(m.baseCtx)
	m.mu.Lock()
	if m.closed {
		// Shutting down: do not start new work. Leave the record as-is so a later
		// Start re-enqueues it (it is still active/queued).
		m.mu.Unlock()
		cancel()
		return
	}
	if _, running := m.cancels[id]; running {
		// A worker already owns this ID (a redundant enqueue, e.g. recovery racing a
		// submit). Drop the duplicate so two workers never drive one destination; the
		// owning worker's cancel entry must not be clobbered.
		m.mu.Unlock()
		cancel()
		return
	}
	// Registering the cancel and reading the intent happen atomically under this
	// mutex, paired with stop() which records the intent under the same lock. That
	// pairing closes the race where a Pause/Cancel arriving while this worker is
	// starting up could otherwise be overwritten: either stop() recorded the intent
	// first (we see it here and never start the transfer) or we registered first
	// (stop() sees our cancel and aborts the run). The transfer can no longer run
	// after the operator stopped it.
	m.cancels[id] = cancel
	intent := m.intents[id]
	m.mu.Unlock()

	if intent == StatusPaused || intent == StatusCanceled {
		// Already stopped before we could begin: settle the intent and skip the run.
		// classify performs the persist on a detached, time-bounded context.
		m.finishJob(id, nil, intent, false)
		cancel()
		return
	}

	// Persist active on a detached, time-bounded context so an aborting base
	// context (Shutdown) does not silently drop the bookkeeping or leave the record
	// torn. A persist failure here aborts this run as a transient error (the record
	// stays queued/active for a later Start) rather than running with inconsistent
	// state. engine.Run re-persists active itself, so this is the early signal for
	// Get/List, not the source of truth.
	if err := m.persistActive(id, d); err != nil {
		// The store rejected the active marker; do not start the transfer against an
		// inconsistent record. Treat it as a shutdown-style abort so the record stays
		// recoverable (queued) instead of mismarked failed.
		m.finishJob(id, err, "", true)
		cancel()
		return
	}

	_, runErr := m.engine.Run(jobCtx, d)
	cancel()

	m.finishJob(id, runErr, intent, m.baseCtx.Err() != nil)
}

// persistActive marks d active and persists it on a detached, time-bounded
// context, so neither an aborting base context (Shutdown) nor a slow store can
// drop the bookkeeping or wedge the worker. The error is returned, not ignored,
// so run can decline to start a transfer against an inconsistent record.
func (m *Manager) persistActive(id string, d *Download) error {
	d.Status = StatusActive
	d.UpdatedAt = time.Now().UTC()
	ctx, stop := context.WithTimeout(context.WithoutCancel(m.baseCtx), 5*time.Second)
	defer stop()
	if err := m.store.SaveDownload(ctx, d); err != nil {
		return fmt.Errorf("engine: manager mark active %q: %w", id, err)
	}
	return nil
}

// finishJob removes the job's in-flight state and settles its persisted status.
// It re-reads the intent under the mutex (a stop() may have arrived during the
// run) and, when an intent set both here and at the start-of-run check disagree,
// honors whichever is now recorded. It always clears the cancel and intent maps
// before classifying so a later Resume starts clean.
func (m *Manager) finishJob(id string, runErr error, startIntent Status, shuttingDown bool) {
	m.mu.Lock()
	intent := m.intents[id]
	if intent == "" {
		intent = startIntent
	}
	delete(m.intents, id)
	delete(m.cancels, id)
	m.mu.Unlock()

	m.classify(id, runErr, intent, shuttingDown)
}

// classify settles the persisted status after a run returns (or after a run was
// skipped because the operator stopped the job before it started):
//   - paused/canceled intent: force that terminal status, whatever the run
//     reported. This overrides the failed status finishFailed sets on a context
//     cancellation and also covers the skip path (runErr == nil), so a stop is
//     never silently dropped.
//   - success with no stop intent: the engine already persisted completed.
//   - shutdown (base context cancelled) with no stop intent: restore the record
//     to queued so a later Start resumes it from its checkpoints.
//   - any other error: the engine already persisted failed.
//
// When an override is required it MUST land, or the record is left at the engine's
// finishFailed status and recover() (which only re-enqueues active/queued) would
// never pick it up again — breaking the promise that Start resumes exactly where a
// transfer was interrupted. So persistOverride retries and, on exhausting the
// retries, logs the failure loudly instead of dropping it silently.
func (m *Manager) classify(id string, runErr error, intent Status, shuttingDown bool) {
	var override Status
	switch {
	case intent == StatusPaused || intent == StatusCanceled:
		override = intent
	case runErr == nil:
		return // engine persisted StatusCompleted
	case shuttingDown:
		override = StatusQueued // recoverable on the next Start
	default:
		return // engine persisted StatusFailed (a genuine transfer error)
	}

	m.persistOverride(id, override)
}

// persistOverrideAttempts bounds the retry loop in persistOverride. A handful of
// attempts absorbs a transient store hiccup without wedging the worker; it is an
// internal robustness constant, not a user-facing tunable.
const persistOverrideAttempts = 3

// persistOverride forces a record to a settled status (queued for a recoverable
// shutdown, paused/canceled for an operator stop) and is resilient to a flaky
// store: it retries the load+save a bounded number of times on a detached,
// time-bounded context (so it survives the aborted base context yet cannot hang),
// and logs at error level if every attempt fails. Logging rather than swallowing
// the error is what stops a shutdown-interrupted job from silently rotting at the
// engine's failed status where recover() can never reach it.
func (m *Manager) persistOverride(id string, override Status) {
	var lastErr error
	for attempt := 1; attempt <= persistOverrideAttempts; attempt++ {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		d, err := m.store.LoadDownload(ctx, id)
		if err != nil {
			stop()
			if errors.Is(err, ErrNotFound) {
				return // record was deleted; nothing to settle
			}
			lastErr = err
			continue
		}
		d.Status = override
		d.UpdatedAt = time.Now().UTC()
		err = m.store.SaveDownload(ctx, d)
		stop()
		if err == nil {
			return
		}
		lastErr = err
	}

	// Every attempt failed. The record keeps whatever status the engine last
	// persisted (failed, for the shutdown case), so it will not auto-recover on the
	// next Start; surface it so an operator can re-enqueue it via Resume. This is
	// the last line: there is no caller to return the error to from a worker.
	m.logger.Error("engine: manager could not persist status override; record may need a manual Resume",
		"download_id", id,
		"target_status", string(override),
		"attempts", persistOverrideAttempts,
		"err", lastErr,
	)
}

// trackProgress and untrackProgress implement progressObserver: the engine calls
// them when a transfer's live counters become available and when it ends, so
// List/Get can fold real-time bytes in without extra store writes.
func (m *Manager) trackProgress(id string, prog *segProgress) {
	m.mu.Lock()
	m.live[id] = prog
	m.mu.Unlock()
}

func (m *Manager) untrackProgress(id string) {
	m.mu.Lock()
	delete(m.live, id)
	m.mu.Unlock()
}

var _ progressObserver = (*Manager)(nil)
