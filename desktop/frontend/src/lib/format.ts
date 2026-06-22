// Pure formatting helpers for the downloads table. Field names match the wire
// shape (DownloadView uses JSON tags: total_size, speed_bps, eta_secs, …).
import type { DownloadView } from './bridge'

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

// humanSize renders a byte count with one significant decimal below 100.
export function humanSize(bytes: number): string {
  if (bytes <= 0) return '0 B'
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), UNITS.length - 1)
  const v = bytes / 1024 ** i
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${UNITS[i]}`
}

// humanSpeed renders bytes/sec, or an em dash when idle.
export function humanSpeed(bps: number): string {
  return bps > 0 ? `${humanSize(bps)}/s` : '—'
}

// percent returns 0..100, or null when the total is unknown — the UI shows "—"
// rather than a misleading 0% (mirrors the CLI's render.go).
export function percent(d: DownloadView): number | null {
  return d.total_size > 0 ? (d.downloaded / d.total_size) * 100 : null
}

// humanEta renders the server's eta_secs; -1 means unknown.
export function humanEta(secs: number): string {
  if (secs < 0) return '—'
  if (secs < 60) return `${secs}s`
  const m = Math.floor(secs / 60)
  if (m < 60) return `${m}m ${secs % 60}s`
  const h = Math.floor(m / 60)
  return `${h}h ${m % 60}m`
}

// fileName derives a display name from the destination path, falling back to the
// URL's last path segment.
export function fileName(d: DownloadView): string {
  if (d.destination) {
    const seg = d.destination.split(/[/\\]/).pop()
    if (seg) return seg
  }
  try {
    const seg = new URL(d.url).pathname.split('/').filter(Boolean).pop()
    if (seg) return seg
  } catch {
    // not a parseable URL — fall through
  }
  return d.url
}
