// Shared menu builders for actions on the current selection, used by both the
// table's right-click menu and the toolbar's selection-mode Priority button so
// the two stay identical.
import { Priority, type DownloadView } from './bridge'
import type { MenuItem } from './menu.svelte'
import { store } from './store.svelte'

const PRIORITIES: { value: Priority; label: string }[] = [
  { value: Priority.PriorityHigh, label: 'High' },
  { value: Priority.PriorityNormal, label: 'Normal' },
  { value: Priority.PriorityLow, label: 'Low' },
]

// priorityMenuItems builds the Low/Normal/High radio items. The current priority
// is checked only when every selected download already shares it (mixed → none).
export function priorityMenuItems(): MenuItem[] {
  const sel = store.selectedList
  const common = sel.length > 0 && sel.every((d) => d.priority === sel[0].priority) ? sel[0].priority : null
  return PRIORITIES.map((p) => ({
    kind: 'item',
    label: p.label,
    checked: common === p.value,
    run: () => store.setPrioritySelected(p.value),
  }))
}

// RATE_PRESETS are the per-download speed-cap choices (bytes/sec; 0 = unlimited).
const RATE_PRESETS: { label: string; bps: number }[] = [
  { label: 'Unlimited', bps: 0 },
  { label: '256 KB/s', bps: 256 * 1024 },
  { label: '512 KB/s', bps: 512 * 1024 },
  { label: '1 MB/s', bps: 1024 * 1024 },
  { label: '2 MB/s', bps: 2 * 1024 * 1024 },
  { label: '5 MB/s', bps: 5 * 1024 * 1024 },
  { label: '10 MB/s', bps: 10 * 1024 * 1024 },
]

// rateMenuItems builds the per-download "Limit speed" radio list for one download.
// The download's current cap (max_rate; 0 = uncapped) is checked when it matches a
// preset. Applies live via store.setRate.
export function rateMenuItems(d: DownloadView): MenuItem[] {
  const current = d.max_rate ?? 0
  return RATE_PRESETS.map((p) => ({
    kind: 'item',
    label: p.label,
    checked: current === p.bps,
    run: () => void store.setRate(d.id, p.bps),
  }))
}
