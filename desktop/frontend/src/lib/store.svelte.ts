// Shared app state. Per Svelte 5 guidance, cross-component reactive state lives
// in a class with $state fields (not a writable store). A single instance is
// exported; components read store.downloads / store.filtered / store.daemonUp
// directly and they stay reactive.
import { SvelteSet } from 'svelte/reactivity'
import { Bridge, DownloadStatus, onDaemon, onDownloads, type DownloadView, type Priority } from './bridge'
import { categoryOf, fileName, type FileCategory } from './format'
import { ACTIVE, PAUSABLE, RESUMABLE } from './status'
import { errMessage, toaster } from './toast.svelte'

// A sidebar selection is either a lifecycle status group or a file-type category.
export type StatusCategory = 'all' | 'active' | 'completed' | 'failed'
export type Selection = { kind: 'status'; value: StatusCategory } | { kind: 'type'; value: FileCategory }

export type SortKey = 'added' | 'name' | 'size' | 'speed' | 'status'
export type SortDir = 'asc' | 'desc'

function matchesStatus(d: DownloadView, c: StatusCategory): boolean {
  switch (c) {
    case 'active':
      return ACTIVE.has(d.status)
    case 'completed':
      return d.status === DownloadStatus.StatusCompleted
    case 'failed':
      return d.status === DownloadStatus.StatusFailed || d.status === DownloadStatus.StatusPaused
    default:
      return true
  }
}

function matches(d: DownloadView, sel: Selection): boolean {
  return sel.kind === 'status' ? matchesStatus(d, sel.value) : categoryOf(d) === sel.value
}

class AppStore {
  // $state.raw: downloads are replaced wholesale each poll (never mutated in
  // place), so the cheaper non-proxied reactivity is the right fit.
  downloads = $state.raw<DownloadView[]>([])
  daemonUp = $state(false)
  selection = $state<Selection>({ kind: 'status', value: 'all' })
  query = $state('')
  sortKey = $state<SortKey>('added')
  sortDir = $state<SortDir>('desc')

  // modalOpen is set by App while a blocking dialog (Add / Details) is up, so the
  // table's window key handler can stand down and not act on selection.
  modalOpen = $state(false)

  // Multi-selection. SvelteSet gives per-id reactive membership: a row reading
  // selectedIds.has(d.id) re-renders only when its own membership flips, not on
  // every selection change. anchorId pins shift-range selection; focusId is the
  // keyboard cursor row.
  selectedIds = new SvelteSet<string>()
  anchorId = $state<string | null>(null)
  focusId = $state<string | null>(null)

  // filtered applies the active selection, then the search query, then the sort.
  filtered = $derived.by<DownloadView[]>(() => {
    let list = this.downloads.filter((d) => matches(d, this.selection))

    const q = this.query.trim().toLowerCase()
    if (q) {
      list = list.filter((d) => fileName(d).toLowerCase().includes(q) || d.url.toLowerCase().includes(q))
    }

    return this.applySort(list)
  })

  totalSpeed = $derived(this.downloads.reduce((sum, d) => sum + (d.speed_bps || 0), 0))
  hasActive = $derived(this.downloads.some((d) => ACTIVE.has(d.status)))
  hasPaused = $derived(
    this.downloads.some((d) => d.status === DownloadStatus.StatusPaused || d.status === DownloadStatus.StatusFailed),
  )

  // Selection-derived state for the header checkbox tri-state and action gating.
  selectedList = $derived(this.downloads.filter((d) => this.selectedIds.has(d.id)))
  allFilteredSelected = $derived(this.filtered.length > 0 && this.filtered.every((d) => this.selectedIds.has(d.id)))
  someFilteredSelected = $derived(this.filtered.some((d) => this.selectedIds.has(d.id)))
  canPauseSelected = $derived(this.selectedList.some((d) => PAUSABLE.has(d.status)))
  canResumeSelected = $derived(this.selectedList.some((d) => RESUMABLE.has(d.status)))

  // count returns how many downloads fall under a selection (for sidebar badges).
  count(sel: Selection): number {
    return this.downloads.reduce((n, d) => (matches(d, sel) ? n + 1 : n), 0)
  }

  // applySort returns a sorted copy; the daemon already returns downloads in
  // creation order, so 'added' just reuses (or reverses) that ordering.
  private applySort(list: DownloadView[]): DownloadView[] {
    if (this.sortKey === 'added') {
      return this.sortDir === 'asc' ? list : [...list].reverse()
    }
    const sign = this.sortDir === 'asc' ? 1 : -1
    return [...list].sort((a, b) => {
      switch (this.sortKey) {
        case 'name':
          return fileName(a).localeCompare(fileName(b)) * sign
        case 'size':
          return (a.total_size - b.total_size) * sign
        case 'speed':
          return (a.speed_bps - b.speed_bps) * sign
        case 'status':
          return a.status.localeCompare(b.status) * sign
        default:
          return 0
      }
    })
  }

  // connect wires the Go event pump into this store and does an initial fetch, so
  // the UI is populated before the first push arrives.
  connect(): void {
    onDaemon((up) => {
      this.daemonUp = up
    })
    onDownloads((list) => {
      this.downloads = list
      this.reconcileSelection(list)
    })
    void this.refresh()
  }

  // reconcileSelection prunes selection/anchor/focus to ids that still exist.
  // downloads are replaced wholesale each poll, but ids are stable, so a live
  // selection survives a refresh while removed items drop out automatically.
  private reconcileSelection(list: DownloadView[]): void {
    if (this.selectedIds.size === 0 && this.anchorId === null && this.focusId === null) return
    const live = new Set(list.map((d) => d.id))
    for (const id of this.selectedIds) {
      if (!live.has(id)) this.selectedIds.delete(id)
    }
    if (this.anchorId && !live.has(this.anchorId)) this.anchorId = null
    if (this.focusId && !live.has(this.focusId)) this.focusId = null
  }

  // --- selection ops (operate over the visible `filtered` order) -------------

  selectOnly(id: string): void {
    this.selectedIds.clear()
    this.selectedIds.add(id)
    this.anchorId = id
    this.focusId = id
  }

  toggle(id: string): void {
    if (this.selectedIds.has(id)) this.selectedIds.delete(id)
    else this.selectedIds.add(id)
    this.anchorId = id
    this.focusId = id
  }

  // selectRangeTo selects the contiguous run from the anchor to id over the
  // current filtered order (replacing the selection, like a file manager).
  selectRangeTo(id: string): void {
    const list = this.filtered
    const a = this.anchorId ?? id
    const i = list.findIndex((d) => d.id === a)
    const j = list.findIndex((d) => d.id === id)
    if (i === -1 || j === -1) {
      this.selectOnly(id)
      return
    }
    const [lo, hi] = i <= j ? [i, j] : [j, i]
    this.selectedIds.clear()
    for (let k = lo; k <= hi; k++) this.selectedIds.add(list[k].id)
    this.focusId = id // anchor stays put so shift-extend keeps growing from it
  }

  selectAll(): void {
    for (const d of this.filtered) this.selectedIds.add(d.id)
    if (this.filtered.length > 0) {
      this.anchorId ??= this.filtered[0].id
      this.focusId = this.filtered[this.filtered.length - 1].id
    }
  }

  clearSelection(): void {
    this.selectedIds.clear()
    this.anchorId = null
    // keep focusId so keyboard navigation can resume from where it was
  }

  // --- bulk actions (fan out single-item calls, mirroring pauseAll/resumeAll) -

  // runBatch fans out one Bridge call per id and surfaces any failures via a
  // toast — otherwise Promise.allSettled would swallow a rejected call and the
  // whole action would look like it did nothing. It refreshes once at the end.
  private async runBatch(verb: string, ids: string[], fn: (id: string) => Promise<void>): Promise<void> {
    if (ids.length === 0) return
    const results = await Promise.allSettled(ids.map(fn))
    await this.refresh()
    const failed = results.filter((r): r is PromiseRejectedResult => r.status === 'rejected')
    if (failed.length > 0) {
      const detail = errMessage(failed[0].reason)
      toaster.error(failed.length === 1 ? `Could not ${verb}: ${detail}` : `Could not ${verb} ${failed.length} downloads: ${detail}`)
    }
  }

  async pauseSelected(): Promise<void> {
    const ids = this.selectedList.filter((d) => PAUSABLE.has(d.status)).map((d) => d.id)
    await this.runBatch('pause', ids, (id) => Bridge.Pause(id))
  }

  async resumeSelected(): Promise<void> {
    const ids = this.selectedList.filter((d) => RESUMABLE.has(d.status)).map((d) => d.id)
    await this.runBatch('resume', ids, (id) => Bridge.Resume(id))
  }

  async removeSelected(): Promise<void> {
    const ids = [...this.selectedIds]
    this.clearSelection() // destructive: clear immediately for instant feedback
    await this.runBatch('remove', ids, (id) => Bridge.Remove(id))
  }

  async setPrioritySelected(priority: Priority): Promise<void> {
    const ids = [...this.selectedIds]
    await this.runBatch('change priority', ids, (id) => Bridge.SetPriority(id, priority))
  }

  // setPriority changes a download's priority and refreshes so the new order and
  // badge show immediately rather than on the next poll tick.
  async setPriority(id: string, priority: Priority): Promise<void> {
    await Bridge.SetPriority(id, priority)
    await this.refresh()
  }

  // setRate caps a single download to bps bytes/sec (0 removes the cap) and
  // refreshes so the row's limit badge updates at once. The daemon applies it live
  // if the download is running.
  async setRate(id: string, bps: number): Promise<void> {
    try {
      await Bridge.SetRate(id, bps)
      await this.refresh()
    } catch (e) {
      toaster.error(`Could not set speed limit: ${errMessage(e)}`)
    }
  }

  // pauseAll / resumeAll are the toolbar's global actions; each fans out over the
  // matching downloads and refreshes once at the end.
  async pauseAll(): Promise<void> {
    const ids = this.downloads.filter((d) => ACTIVE.has(d.status)).map((d) => d.id)
    await this.runBatch('pause', ids, (id) => Bridge.Pause(id))
  }

  async resumeAll(): Promise<void> {
    const ids = this.downloads
      .filter((d) => d.status === DownloadStatus.StatusPaused || d.status === DownloadStatus.StatusFailed)
      .map((d) => d.id)
    await this.runBatch('resume', ids, (id) => Bridge.Resume(id))
  }

  // refresh pulls a snapshot immediately (used after an action, for responsiveness
  // rather than waiting for the next poll tick).
  async refresh(): Promise<void> {
    try {
      this.daemonUp = await Bridge.Health()
      const list = this.daemonUp ? ((await Bridge.List()) ?? []) : []
      this.downloads = list
      this.reconcileSelection(list)
    } catch {
      this.daemonUp = false
    }
  }
}

export const store = new AppStore()
