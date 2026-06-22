<script lang="ts">
  import { Bridge, DownloadStatus, type DownloadView } from '../lib/bridge'
  import { store } from '../lib/store.svelte'
  import { fileName, humanEta, humanSize, humanSpeed, percent } from '../lib/format'

  const RESUMABLE = new Set<DownloadStatus>([
    DownloadStatus.StatusPaused,
    DownloadStatus.StatusFailed,
    DownloadStatus.StatusCompleted,
  ])
  const PAUSABLE = new Set<DownloadStatus>([DownloadStatus.StatusActive, DownloadStatus.StatusQueued])

  async function act(fn: (id: string) => Promise<void>, id: string): Promise<void> {
    try {
      await fn(id)
      await store.refresh()
    } catch (e) {
      console.error(e)
    }
  }

  const STATUS_LABEL: Record<string, string> = {
    queued: 'Queued',
    active: 'Active',
    paused: 'Paused',
    completed: 'Done',
    failed: 'Failed',
  }
</script>

<div class="table-wrap">
  {#if store.filtered.length === 0}
    <div class="empty">
      <p class="empty-title">No downloads</p>
      <p class="empty-sub">Paste a URL above to start one.</p>
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
          <th class="col-status">Status</th>
          <th class="col-actions"></th>
        </tr>
      </thead>
      <tbody>
        {#each store.filtered as d (d.id)}
          {@const pct = percent(d)}
          <tr>
            <td class="col-name" title={d.url}>{fileName(d)}</td>
            <td class="col-size">{d.total_size > 0 ? humanSize(d.total_size) : '—'}</td>
            <td class="col-progress">
              <div class="bar" style:--pct="{pct ?? 0}%">
                <div class="bar-fill" class:indeterminate={pct === null && d.status === DownloadStatus.StatusActive}></div>
              </div>
              <span class="pct">{pct === null ? '—' : `${Math.round(pct)}%`}</span>
            </td>
            <td class="col-speed">{humanSpeed(d.speed_bps)}</td>
            <td class="col-eta">{humanEta(d.eta_secs)}</td>
            <td class="col-status">
              <span class="badge {d.status}">{STATUS_LABEL[d.status] ?? d.status}</span>
            </td>
            <td class="col-actions">
              <div class="actions">
                {#if PAUSABLE.has(d.status)}
                  <button title="Pause" onclick={() => act(Bridge.Pause, d.id)} aria-label="Pause">⏸</button>
                {/if}
                {#if RESUMABLE.has(d.status)}
                  <button title="Resume" onclick={() => act(Bridge.Resume, d.id)} aria-label="Resume">▶</button>
                {/if}
                <button class="danger" title="Remove" onclick={() => act(Bridge.Remove, d.id)} aria-label="Remove">✕</button>
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
    padding: 0 var(--space-5);
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
    color: var(--muted);
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: var(--space-2) var(--space-3);
    background: var(--bg);
    border-bottom: 1px solid var(--border);
  }
  tbody td {
    padding: var(--space-2) var(--space-3);
    border-bottom: 1px solid var(--border);
    vertical-align: middle;
    white-space: nowrap;
  }
  tbody tr:hover {
    background: var(--surface-2);
  }
  .col-name {
    max-width: 0;
    width: 40%;
    overflow: hidden;
    text-overflow: ellipsis;
    font-weight: 500;
  }
  .col-size,
  .col-speed,
  .col-eta {
    font-variant-numeric: tabular-nums;
    color: var(--muted);
  }
  .col-progress {
    width: 28%;
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  .bar {
    flex: 1;
    height: 6px;
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
  .bar-fill.indeterminate {
    width: 35%;
    animation: slide 1.1s ease-in-out infinite;
  }
  @keyframes slide {
    0% { transform: translateX(-120%); }
    100% { transform: translateX(320%); }
  }
  .pct {
    width: 38px;
    text-align: right;
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
    background: var(--surface-2);
    color: var(--muted);
  }
  .badge.active { background: color-mix(in srgb, var(--accent) 18%, transparent); color: var(--accent); }
  .badge.completed { background: color-mix(in srgb, var(--success) 20%, transparent); color: var(--success); }
  .badge.failed { background: color-mix(in srgb, var(--danger) 20%, transparent); color: var(--danger); }
  .badge.paused { background: color-mix(in srgb, var(--warning) 22%, transparent); color: var(--warning); }
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
    border: 0;
    background: var(--surface-2);
    color: var(--text);
    border-radius: var(--radius-sm);
    width: 26px;
    height: 26px;
    cursor: pointer;
    font-size: var(--text-xs);
    line-height: 1;
  }
  .actions button:hover {
    background: color-mix(in srgb, var(--accent) 18%, transparent);
  }
  .actions button.danger:hover {
    background: color-mix(in srgb, var(--danger) 20%, transparent);
    color: var(--danger);
  }
  .empty {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    color: var(--muted);
    gap: var(--space-1);
  }
  .empty-title {
    font-size: var(--text-lg);
    font-weight: 600;
    color: var(--text);
    margin: 0;
  }
  .empty-sub {
    margin: 0;
  }
</style>
