// bridge is the single seam between the UI and the generated Wails bindings: the
// only module that imports the deep bindings/ paths and the wails runtime. Every
// component imports daemon calls, the DownloadView type, and the event helpers
// from here, so the generated paths live in exactly one place.
import { Events } from '@wailsio/runtime'
import { Bridge } from '../../bindings/github.com/K-RED90/gidm/desktop/bridge'
import { DownloadStatus, Priority, type DownloadView } from '../../bindings/github.com/K-RED90/gidm/api'

export { Bridge, DownloadStatus, Priority }
export type { DownloadView }

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
