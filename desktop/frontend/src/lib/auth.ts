// Helpers for the per-download credential editors in the Add and Properties
// dialogs: turning the headers textarea to/from a map and assembling a lean
// Credentials payload that omits empty fields.
import type { Credentials } from './bridge'

// parseHeaderLines turns a textarea of "Name: value" lines into a header map,
// skipping blanks and lines without a colon. Trimming keeps values free of the
// CR/LF the daemon rejects as header injection.
export function parseHeaderLines(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const t = line.trim()
    if (!t) continue
    const i = t.indexOf(':')
    if (i <= 0) continue
    const name = t.slice(0, i).trim()
    const value = t.slice(i + 1).trim()
    if (name) out[name] = value
  }
  return out
}

// headerLines is the inverse, for pre-filling the editor from stored headers.
// The generated map type permits undefined values, so they are coerced to empty.
export function headerLines(headers?: { [key: string]: string | undefined } | null): string {
  if (!headers) return ''
  return Object.entries(headers)
    .map(([k, v]) => `${k}: ${v ?? ''}`)
    .join('\n')
}

// buildCredentials assembles a Credentials payload, dropping empty fields so the
// daemon stores no auth when nothing is set.
export function buildCredentials(fields: {
  username?: string
  password?: string
  referer?: string
  cookie?: string
  headers?: Record<string, string>
}): Credentials {
  const c: Credentials = {}
  const u = fields.username?.trim()
  const r = fields.referer?.trim()
  const ck = fields.cookie?.trim()
  if (u) c.username = u
  if (fields.password) c.password = fields.password
  if (r) c.referer = r
  if (ck) c.cookie = ck
  if (fields.headers && Object.keys(fields.headers).length) c.headers = fields.headers
  return c
}
