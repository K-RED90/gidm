// A tiny toast singleton for action feedback. Without it, fire-and-forget actions
// (copy URL, reveal, bulk remove) give no signal — a silent success and a swallowed
// error look identical. Components read `toaster.toasts`; ContextMenu/table/store
// call success()/error().
export type ToastKind = 'info' | 'success' | 'error'
export type Toast = { id: number; kind: ToastKind; message: string }

class Toaster {
  toasts = $state<Toast[]>([])
  private seq = 0

  show(message: string, kind: ToastKind = 'info', ms = 2600): void {
    const id = ++this.seq
    this.toasts = [...this.toasts, { id, kind, message }]
    setTimeout(() => this.dismiss(id), ms)
  }

  success(message: string): void {
    this.show(message, 'success')
  }

  error(message: string): void {
    this.show(message, 'error', 4500)
  }

  dismiss(id: number): void {
    this.toasts = this.toasts.filter((t) => t.id !== id)
  }
}

export const toaster = new Toaster()

// errMessage pulls a human string out of whatever a rejected bridge call throws.
export function errMessage(e: unknown): string {
  if (e instanceof Error) return e.message
  if (typeof e === 'string') return e
  return String(e)
}
