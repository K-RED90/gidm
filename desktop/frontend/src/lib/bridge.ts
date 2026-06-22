// bridge is the single seam between the UI and the generated Wails bindings: the
// only module that imports the deep bindings/ paths and the wails runtime. Every
// component imports daemon calls, the DownloadView type, and the event helpers
// from here, so the generated paths live in exactly one place.
import { Clipboard, Dialogs, Events } from '@wailsio/runtime'
import { Bridge } from '../../bindings/github.com/K-RED90/gidm/desktop/bridge'
import {
  DownloadStatus,
  Priority,
  type DownloadView,
  type Credentials,
  type AuthView,
} from '../../bindings/github.com/K-RED90/gidm/api'

export { Bridge, DownloadStatus, Priority }
export type { DownloadView, Credentials, AuthView }

// copyText puts text on the system clipboard (used by the row "Copy URL" action).
// Clipboard is the frontend Wails runtime module, so no Go round-trip is needed.
export async function copyText(text: string): Promise<void> {
  await Clipboard.SetText(text)
}

// openPath / revealPath ask the Go bridge to open a downloaded file with its
// default app, or reveal it in the OS file manager. The path comes straight from
// the download's destination, so no daemon lookup is involved.
export async function openPath(path: string): Promise<void> {
  if (path) await Bridge.OpenFile(path)
}

export async function revealPath(path: string): Promise<void> {
  if (path) await Bridge.RevealInFolder(path)
}

// pickDirectory opens the OS folder picker and resolves to the chosen absolute
// path, or "" when the user cancels. Single seam to the Wails dialog runtime so
// no component imports it directly.
export async function pickDirectory(title = 'Choose download folder'): Promise<string> {
  const picked = await Dialogs.OpenFile({
    Title: title,
    CanChooseDirectories: true,
    CanChooseFiles: false,
    AllowsMultipleSelection: false,
  })
  return typeof picked === 'string' ? picked : ''
}

// Event names emitted by the Go event pump (main.go); kept in sync with
// bridge.EventDownloads / bridge.EventDaemon on the Go side.
const EVENT_DOWNLOADS = 'downloads:update'
const EVENT_DAEMON = 'daemon:status'

type WailsEvent<T> = { data: T }

// onDownloads subscribes to snapshot pushes; returns an unsubscribe function.
export function onDownloads(cb: (downloads: DownloadView[]) => void): () => void {
  return Events.On(EVENT_DOWNLOADS, (e: WailsEvent<DownloadView[] | null>) => cb(e.data ?? []))
}

// onDaemon subscribes to daemon-reachability pushes; returns an unsubscribe function.
export function onDaemon(cb: (reachable: boolean) => void): () => void {
  return Events.On(EVENT_DAEMON, (e: WailsEvent<boolean>) => cb(Boolean(e.data)))
}
