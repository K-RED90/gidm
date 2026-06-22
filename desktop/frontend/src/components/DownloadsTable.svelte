<script lang="ts">
  import Icon from './Icon.svelte'
  import type { IconName } from '../lib/icons'
  import { Bridge, DownloadStatus, Priority, type DownloadView } from '../lib/bridge'
  import { store } from '../lib/store.svelte'
  import { categoryOf, fileName, humanEta, humanSize, humanSpeed, percent, type FileCategory } from '../lib/format'

  const RESUMABLE = new Set<DownloadStatus>([
    DownloadStatus.StatusPaused,
    DownloadStatus.StatusFailed,
    DownloadStatus.StatusCompleted,
  ])
  const PAUSABLE = new Set<DownloadStatus>([DownloadStatus.StatusActive, DownloadStatus.StatusQueued])

  const STATUS_LABEL: Record<string, string> = {
    queued: 'Queued',
    active: 'Active',
    paused: 'Paused',
    completed: 'Done',
    failed: 'Failed',
  }

  const TYPE_ICON: Record<FileCategory, IconName> = {
    compressed: 'archive',
    documents: 'document',
    music: 'music',
    pictures: 'image',
    programs: 'program',
    video: 'video',
    other: 'file',
  }

  // Clicking a priority pill cycles low → normal → high → low.
  const NEXT_PRIORITY: Record<string, Priority> = {
    [Priority.PriorityLow]: Priority.PriorityNormal,
    [Priority.PriorityNormal]: Priority.PriorityHigh,
    [Priority.PriorityHigh]: Priority.PriorityLow,
  }
  const PRIORITY_LABEL: Record<string, string> = {
    [Priority.PriorityLow]: 'Low',
    [Priority.PriorityNormal]: 'Normal',
    [Priority.PriorityHigh]: 'High',
  }

  async function act(fn: (id: string) => Promise<void>, id: string): Promise<void> {
    try {
      await fn(id)
      await store.refresh()
    } catch (e) {
      console.error(e)
    }
  }

  async function cyclePriority(d: DownloadView): Promise<void> {
    try {
      await store.setPriority(d.id, NEXT_PRIORITY[d.priority] ?? Priority.PriorityNormal)
    } catch (e) {
      console.error(e)
    }
  }
</script>

<div class="table-wrap">
  {#if store.filtered.length === 0}
    <div class="empty">
      {#if store.downloads.length === 0}
        <p class="empty-title">No downloads yet</p>
        <p class="empty-sub">Hit <strong>Add URL</strong> to start your first download.</p>
      {:else}
        <p class="empty-title">Nothing here</p>
        <p class="empty-sub">No downloads match this view.</p>
      {/if}
    </div>
  {:else}
    <table>
      <thead>
        <tr>
          <th class="col-name">Name</th>
          <th class="col-size">Size</th>
          <th class="col-progress">Progress</th>
          <th class="col-speed">Speed</th>
          <th class="col-eta">ETA</th>
          <th class="col-prio">Priority</th>
          <th class="col-status">Status</th>
          <th class="col-actions"></th>
        </tr>
      </thead>
      <tbody>
        {#each store.filtered as d (d.id)}
          {@const pct = percent(d)}
          <tr>
            <td class="col-name" title={d.destination || d.url}>
              <span class="ficon"><Icon name={TYPE_ICON[categoryOf(d)]} size={16} /></span>
              <span class="fname">{fileName(d)}</span>
            </td>
            <td class="col-size">{d.total_size > 0 ? humanSize(d.total_size) : '—'}</td>
            <td class="col-progress">
              <div class="bar" style:--pct="{pct ?? 0}%">
                <div
                  class="bar-fill {d.status}"
                  class:indeterminate={pct === null && d.status === DownloadStatus.StatusActive}
                ></div>
              </div>
              <span class="pct">{pct === null ? '—' : `${Math.round(pct)}%`}</span>
            </td>
            <td class="col-speed">{humanSpeed(d.speed_bps)}</td>
            <td class="col-eta">{humanEta(d.eta_secs)}</td>
            <td class="col-prio">
              <button
                class="prio {d.priority}"
                title="Priority: {PRIORITY_LABEL[d.priority] ?? d.priority} — click to change"
                onclick={() => cyclePriority(d)}
              >
                {PRIORITY_LABEL[d.priority] ?? d.priority}
              </button>
            </td>
            <td class="col-status">
              <span class="badge {d.status}">{STATUS_LABEL[d.status] ?? d.status}</span>
            </td>
            <td class="col-actions">
              <div class="actions">
                {#if PAUSABLE.has(d.status)}
                  <button title="Pause" onclick={() => act(Bridge.Pause, d.id)} aria-label="Pause">
                    <Icon name="pause" size={14} />
                  </button>
                {/if}
                {#if RESUMABLE.has(d.status)}
                  <button title="Resume" onclick={() => act(Bridge.Resume, d.id)} aria-label="Resume">
                    <Icon name="play" size={14} />
                  </button>
                {/if}
                <button class="danger" title="Remove" onclick={() => act(Bridge.Remove, d.id)} aria-label="Remove">
                  <Icon name="trash" size={14} />
                </button>
              </div>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<style>
  .table-wrap {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: var(--text-sm);
  }
  thead th {
    position: sticky;
    top: 0;
    z-index: 1;
    text-align: left;
    font-weight: 600;
    color: var(--faint);
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: var(--space-2) var(--space-3);
    background: var(--bg);
    border-bottom: 1px solid var(--border);
  }
  tbody td {
    height: var(--row-h);
    padding: 0 var(--space-3);
    border-bottom: 1px solid var(--border);
    vertical-align: middle;
    white-space: nowrap;
    color: var(--text);
  }
  tbody tr:hover {
    background: var(--surface-2);
  }

  .col-name {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    max-width: 0;
    width: 38%;
    overflow: hidden;
    font-weight: 500;
  }
  .ficon {
    flex: none;
    color: var(--muted);
  }
  .fname {
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .col-size,
  .col-speed,
  .col-eta {
    font-variant-numeric: tabular-nums;
    color: var(--muted);
  }

  .col-progress {
    width: 22%;
  }
  .col-progress {
    /* lay the bar + pct on one line without a flex td (keeps cell height stable) */
    white-space: nowrap;
  }
  .bar {
    display: inline-block;
    vertical-align: middle;
    width: calc(100% - 44px);
    height: 5px;
    border-radius: 999px;
    background: var(--track);
    overflow: hidden;
  }
  .bar-fill {
    height: 100%;
    width: var(--pct);
    border-radius: 999px;
    background: var(--accent);
    transition: width 0.3s ease;
  }
  .bar-fill.completed {
    background: var(--success);
  }
  .bar-fill.paused,
  .bar-fill.failed {
    background: var(--faint);
  }
  .bar-fill.indeterminate {
    width: 35%;
    animation: slide 1.1s ease-in-out infinite;
  }
  @keyframes slide {
    0% {
      transform: translateX(-120%);
    }
    100% {
      transform: translateX(320%);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .bar-fill.indeterminate {
      animation: none;
      width: 100%;
      opacity: 0.4;
    }
  }
  .pct {
    display: inline-block;
    width: 38px;
    margin-left: var(--space-2);
    text-align: right;
    vertical-align: middle;
    font-variant-numeric: tabular-nums;
    color: var(--muted);
    font-size: var(--text-xs);
  }

  .badge {
    display: inline-block;
    padding: 2px var(--space-2);
    border-radius: 999px;
    font-size: var(--text-xs);
    font-weight: 600;
    background: var(--surface-3);
    color: var(--muted);
  }
  .badge.active {
    background: var(--accent-soft);
    color: var(--accent);
  }
  .badge.completed {
    background: color-mix(in srgb, var(--success) 18%, transparent);
    color: var(--success);
  }
  .badge.failed {
    background: color-mix(in srgb, var(--danger) 20%, transparent);
    color: var(--danger);
  }
  .badge.paused {
    background: color-mix(in srgb, var(--warning) 20%, transparent);
    color: var(--warning);
  }

  .col-prio {
    width: 1%;
  }
  .prio {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    border: 1px solid transparent;
    border-radius: 999px;
    padding: 2px var(--space-2);
    font-family: inherit;
    font-size: var(--text-xs);
    font-weight: 600;
    line-height: 1.4;
    color: var(--muted);
    background: var(--surface-3);
    cursor: pointer;
  }
  .prio::before {
    content: '–';
    font-weight: 700;
  }
  .prio.high::before {
    content: '▲';
    font-size: 8px;
  }
  .prio.low::before {
    content: '▼';
    font-size: 8px;
  }
  .prio.high {
    color: var(--accent);
    background: var(--accent-soft);
  }
  .prio:hover {
    border-color: color-mix(in srgb, var(--accent) 45%, transparent);
  }
  .prio:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }

  .actions {
    display: flex;
    gap: var(--space-1);
    justify-content: flex-end;
    opacity: 0;
  }
  tr:hover .actions {
    opacity: 1;
  }
  .actions button {
    display: grid;
    place-items: center;
    border: 0;
    background: var(--surface-3);
    color: var(--muted);
    border-radius: var(--radius-sm);
    width: 28px;
    height: 28px;
    cursor: pointer;
  }
  .actions button:hover {
    background: var(--accent-soft);
    color: var(--accent);
  }
  .actions button.danger:hover {
    background: color-mix(in srgb, var(--danger) 20%, transparent);
    color: var(--danger);
  }
  .actions button:focus-visible {
    opacity: 1;
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }

  .empty {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--space-2);
    color: var(--muted);
    background-image: var(--grid);
    background-size: var(--grid-size);
  }

  /* Drop the least-critical columns first as the window narrows. */
  @media (max-width: 880px) {
    .col-eta {
      display: none;
    }
  }
  @media (max-width: 780px) {
    .col-speed {
      display: none;
    }
  }
  .empty-title {
    margin: 0;
    font-family: var(--font-serif);
    font-size: var(--text-display);
    font-weight: 600;
    letter-spacing: -0.01em;
    color: var(--text);
  }
  .empty-sub {
    margin: 0;
    font-size: var(--text-md);
  }
  .empty-sub strong {
    color: var(--text);
    font-weight: 600;
  }
</style>
