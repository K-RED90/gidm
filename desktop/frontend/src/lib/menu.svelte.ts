// A tiny singleton controller for the app's one floating context menu. Any
// component opens it via menu.show(x, y, items); ContextMenu.svelte renders it.
// Keeping it a module-level singleton means there is exactly one menu in the DOM
// (no stacking, no per-row menu instances), opened on demand at the pointer.
import type { IconName } from './icons'

export type MenuItem =
  | {
      kind: 'item'
      label: string
      icon?: IconName
      disabled?: boolean
      danger?: boolean
      checked?: boolean
      run: () => void
    }
  | { kind: 'submenu'; label: string; icon?: IconName; disabled?: boolean; items: MenuItem[] }
  | { kind: 'sep' }

class MenuController {
  open = $state(false)
  x = $state(0)
  y = $state(0)
  items = $state<MenuItem[]>([])

  show(x: number, y: number, items: MenuItem[]): void {
    this.x = x
    this.y = y
    this.items = items
    this.open = true
  }

  hide(): void {
    this.open = false
    this.items = []
  }
}

export const menu = new MenuController()
