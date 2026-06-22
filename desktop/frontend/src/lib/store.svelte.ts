// Shared app state. Per Svelte 5 guidance, cross-component reactive state lives
// in a class with $state fields (not a writable store). A single instance is
// exported; components read store.downloads / store.filtered / store.daemonUp
// directly and they stay reactive.
import { Bridge, DownloadStatus, onDaemon, onDownloads, type DownloadView } from './bridge'

export type Category = 'all' | 'active' | 'completed' | 'failed'

const ACTIVE = new Set<DownloadStatus>([DownloadStatus.StatusQueued, DownloadStatus.StatusActive])

class AppStore {
  // $state.raw: downloads are replaced wholesale each poll (never mutated in
  // place), so the cheaper non-proxied reactivity is the right fit.
  downloads = $state.raw<DownloadView[]>([])
  daemonUp = $state(false)
  category = $state<Category>('all')

  filtered = $derived.by<DownloadView[]>(() => {
    switch (this.category) {
      case 'active':
        return this.downloads.filter((d) => ACTIVE.has(d.status))
      case 'completed':
        return this.downloads.filter((d) => d.status === DownloadStatus.StatusCompleted)
      case 'failed':
        return this.downloads.filter(
          (d) => d.status === DownloadStatus.StatusFailed || d.status === DownloadStatus.StatusPaused,
        )
      default:
        return this.downloads
    }
  })

  totalSpeed = $derived(this.downloads.reduce((sum, d) => sum + (d.speed_bps || 0), 0))

  // count returns how many downloads fall under a sidebar category (for badges).
  count(c: Category): number {
    switch (c) {
      case 'active':
        return this.downloads.filter((d) => ACTIVE.has(d.status)).length
      case 'completed':
        return this.downloads.filter((d) => d.status === DownloadStatus.StatusCompleted).length
      case 'failed':
        return this.downloads.filter(
          (d) => d.status === DownloadStatus.StatusFailed || d.status === DownloadStatus.StatusPaused,
        ).length
      default:
        return this.downloads.length
    }
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
