// Shared download-status helpers. These sets define which actions apply to a
// given status and are used by both the store (bulk actions, filtering) and the
// table (per-row buttons), so the two never drift.
import { DownloadStatus } from './bridge'

// ACTIVE: in-flight downloads (queued or transferring). This is also exactly the
// set that can be paused, so PAUSABLE aliases it.
export const ACTIVE: ReadonlySet<DownloadStatus> = new Set([
  DownloadStatus.StatusQueued,
  DownloadStatus.StatusActive,
])
export const PAUSABLE = ACTIVE

// RESUMABLE: terminal states a download can be (re-)started from. Completed is
// included so a finished item can be re-downloaded, matching the per-row button.
export const RESUMABLE: ReadonlySet<DownloadStatus> = new Set([
  DownloadStatus.StatusPaused,
  DownloadStatus.StatusFailed,
  DownloadStatus.StatusCompleted,
])

export const STATUS_LABEL: Record<string, string> = {
  queued: 'Queued',
  active: 'Active',
  paused: 'Paused',
  completed: 'Done',
  failed: 'Failed',
}
