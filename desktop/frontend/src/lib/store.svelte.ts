// Shared app state. Per Svelte 5 guidance, cross-component reactive state lives
// in a class with $state fields (not a writable store). A single instance is
// exported; components read store.downloads / store.filtered / store.daemonUp
// directly and they stay reactive.
import { Bridge, DownloadStatus, onDaemon, onDownloads, type DownloadView, type Priority } from './bridge'
import { categoryOf, fileName, type FileCategory } from './format'

// A sidebar selection is either a lifecycle status group or a file-type category.
export type StatusCategory = 'all' | 'active' | 'completed' | 'failed'
export type Selection = { kind: 'status'; value: StatusCategory } | { kind: 'type'; value: FileCategory }

export type SortKey = 'added' | 'name' | 'size' | 'speed' | 'status'
export type SortDir = 'asc' | 'desc'

const ACTIVE = new Set<DownloadStatus>([DownloadStatus.StatusQueued, DownloadStatus.StatusActive])

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
    })
    void this.refresh()
  }

  // setPriority changes a download's priority and refreshes so the new order and
  // badge show immediately rather than on the next poll tick.
  async setPriority(id: string, priority: Priority): Promise<void> {
    await Bridge.SetPriority(id, priority)
    await this.refresh()
  }

  // pauseAll / resumeAll are the toolbar's global actions; each fans out over the
  // matching downloads and refreshes once at the end.
  async pauseAll(): Promise<void> {
    const ids = this.downloads.filter((d) => ACTIVE.has(d.status)).map((d) => d.id)
    await Promise.allSettled(ids.map((id) => Bridge.Pause(id)))
    await this.refresh()
  }

  async resumeAll(): Promise<void> {
    const ids = this.downloads
      .filter((d) => d.status === DownloadStatus.StatusPaused || d.status === DownloadStatus.StatusFailed)
      .map((d) => d.id)
    await Promise.allSettled(ids.map((id) => Bridge.Resume(id)))
    await this.refresh()
  }

  // refresh pulls a snapshot immediately (used after an action, for responsiveness
  // rather than waiting for the next poll tick).
  async refresh(): Promise<void> {
    try {
      this.daemonUp = await Bridge.Health()
      this.downloads = this.daemonUp ? ((await Bridge.List()) ?? []) : []
    } catch {
      this.daemonUp = false
    }
  }
}

export const store = new AppStore()
