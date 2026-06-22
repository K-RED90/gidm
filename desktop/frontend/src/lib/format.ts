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

// capLabel renders a per-download speed cap for the row badge ("≤ 2 MB/s"), or ''
// when the download is uncapped (max_rate 0).
export function capLabel(bps: number): string {
  return bps > 0 ? `≤ ${humanSize(bps)}/s` : ''
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

// fileExt returns the lowercase extension of a download's name (without the dot),
// or '' when there is none.
export function fileExt(d: DownloadView): string {
  const name = fileName(d)
  const dot = name.lastIndexOf('.')
  return dot > 0 && dot < name.length - 1 ? name.slice(dot + 1).toLowerCase() : ''
}

// FileCategory is the IDM-style content grouping used by the sidebar categories.
export type FileCategory = 'compressed' | 'documents' | 'music' | 'pictures' | 'programs' | 'video' | 'other'

// EXT_CATEGORY maps a file extension to its category. Extensions not listed fall
// through to 'other'. One table, kept here so the sidebar and any filter share it.
const EXT_CATEGORY: Record<string, FileCategory> = {
  // compressed
  zip: 'compressed', rar: 'compressed', '7z': 'compressed', gz: 'compressed', tgz: 'compressed',
  bz2: 'compressed', xz: 'compressed', tar: 'compressed', zst: 'compressed',
  // documents
  pdf: 'documents', doc: 'documents', docx: 'documents', xls: 'documents', xlsx: 'documents',
  ppt: 'documents', pptx: 'documents', txt: 'documents', rtf: 'documents', csv: 'documents',
  epub: 'documents', mobi: 'documents', odt: 'documents',
  // music
  mp3: 'music', flac: 'music', wav: 'music', aac: 'music', ogg: 'music', m4a: 'music', opus: 'music',
  // pictures
  jpg: 'pictures', jpeg: 'pictures', png: 'pictures', gif: 'pictures', webp: 'pictures',
  svg: 'pictures', bmp: 'pictures', tiff: 'pictures', heic: 'pictures', ico: 'pictures',
  // programs
  exe: 'programs', msi: 'programs', dmg: 'programs', pkg: 'programs', deb: 'programs',
  rpm: 'programs', appimage: 'programs', apk: 'programs', bin: 'programs',
  // video
  mp4: 'video', mkv: 'video', avi: 'video', mov: 'video', webm: 'video', flv: 'video',
  wmv: 'video', m4v: 'video', mpg: 'video', mpeg: 'video', ts: 'video',
}

// categoryOf classifies a download by its file extension.
export function categoryOf(d: DownloadView): FileCategory {
  return EXT_CATEGORY[fileExt(d)] ?? 'other'
}
