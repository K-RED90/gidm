// Shared menu builders for actions on the current selection, used by both the
// table's right-click menu and the toolbar's selection-mode Priority button so
// the two stay identical.
import { Priority } from './bridge'
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
