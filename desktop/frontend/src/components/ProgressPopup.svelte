<script lang="ts">
  import { onMount } from 'svelte'
  import {
    Bridge,
    DownloadStatus,
    closeWindow,
    onDownloads,
    openPath,
    resizeWindow,
    revealPath,
    showMainWindow,
    type DownloadView,
  } from '../lib/bridge'
  import { fileName, humanEta, humanSize, humanSpeed, percent } from '../lib/format'
  import { PAUSABLE, RESUMABLE, STATUS_LABEL } from '../lib/status'

  const POPUP_WIDTH = 480

  // id is fixed for the life of this window (baked into the URL by main.go).
  let { id }: { id: string } = $props()

  // The window is sized to fit this element's content (see onMount), so the popup
  // is never a too-tall box with empty space nor a clipped one.
  let popupEl: HTMLElement | undefined

  // This standalone window isn't wired to the shared store; it keeps its own copy
  // of the snapshot. The Go pump broadcasts downloads:update to every window, so
  // onDownloads keeps it live; Bridge.List fills it once up front so the window
  // isn't blank for the first tick.
  let downloads = $state.raw<DownloadView[]>([])
  let everSeen = $state(false)

  const d = $derived(downloads.find((x) => x.id === id))
  const pct = $derived(d ? percent(d) : null)
  const status = $derived(d?.status)
  const done = $derived(status === DownloadStatus.StatusCompleted)
  const failed = $derived(status === DownloadStatus.StatusFailed)
  const pctText = $derived(pct === null ? (done ? 'Done' : '—') : `${+pct.toFixed(1)}%`)

  // Per-connection slices for the segmented bar (present for in-flight downloads).
  const conns = $derived.by(() => {
    const segs = d?.segments
    if (!segs || segs.length === 0) return []
    return segs.map((s) => {
      const size = s.end - s.start + 1
      return { index: s.index, size, pct: size > 0 ? Math.min(100, (s.completed / size) * 100) : 0 }
    })
  })
  const connCount = $derived(d?.segment_count || conns.length)

  const canPause = $derived(!!status && PAUSABLE.has(status))
  const canResume = $derived(!!status && RESUMABLE.has(status))

  onMount(() => {
    void Bridge.List().then((list) => {
      if (list) downloads = list
    })
    const off = onDownloads((list) => {
      downloads = list
    })
    // Resize the window to the content's height whenever it changes (e.g. the
    // connection bar appears once segments load). popupEl is content-sized, so
    // SetSize never feeds back into its height — no resize loop.
    const ro = new ResizeObserver(() => {
      if (popupEl) void resizeWindow(POPUP_WIDTH, Math.ceil(popupEl.offsetHeight))
    })
    if (popupEl) ro.observe(popupEl)
    return () => {
      off()
      ro.disconnect()
    }
  })

  // Once the download is gone (canceled here or removed in the manager) there is
  // nothing left to show — close the window.
  $effect(() => {
    if (d) everSeen = true
    else if (everSeen) void closeWindow()
  })

  async function pause(): Promise<void> {
    if (d) await Bridge.Pause(d.id)
  }
  async function resume(): Promise<void> {
    if (d) await Bridge.Resume(d.id)
  }
  async function cancel(): Promise<void> {
    if (d) await Bridge.Remove(d.id)
    await closeWindow()
  }
</script>

<div class="popup" bind:this={popupEl}>
  <header class="drag" style="--wails-draggable: drag"></header>

  <div class="body" style="--wails-draggable: no-drag">
    <div class="title">
      <span class="name" title={d ? fileName(d) : ''}>{d ? fileName(d) : 'Starting…'}</span>
      {#if status}
        <span class="badge">{STATUS_LABEL[status] ?? status}</span>
      {/if}
    </div>

    <div class="progress">
      <div class="bar">
        <div
          class="fill {status ?? ''}"
          class:indeterminate={pct === null && status === DownloadStatus.StatusActive}
          style:width="{pct ?? (done ? 100 : 0)}%"
        ></div>
      </div>
      <span class="pct">{pctText}</span>
    </div>

    <dl class="stats">
      <div><dt>Speed</dt><dd>{d ? humanSpeed(d.speed_bps) : '—'}</dd></div>
      <div><dt>Time left</dt><dd>{d && !done ? humanEta(d.eta_secs) : '—'}</dd></div>
      <div>
        <dt>Downloaded</dt>
        <dd>{d ? humanSize(d.downloaded) : '—'}{d && d.total_size > 0 ? ` / ${humanSize(d.total_size)}` : ''}</dd>
      </div>
      <div><dt>Connections</dt><dd>{connCount || '—'}</dd></div>
    </dl>

    {#if conns.length > 0}
      <div class="segbar" aria-hidden="true">
        {#each conns as c (c.index)}
          <div class="seg" style:flex-grow={c.size}><div class="seg-fill" style:width="{c.pct}%"></div></div>
        {/each}
      </div>
    {/if}

    {#if d?.destination}
      <div class="path">
        <span class="plabel">Saved to</span>
        <span class="pval" title={d.destination}>{d.destination}</span>
      </div>
    {/if}
  </div>

  <footer class="foot" style="--wails-draggable: no-drag">
    <button class="link" onclick={() => showMainWindow()}>Open gidm</button>
    <div class="actions">
      {#if done}
        <button class="ghost" onclick={() => d && void revealPath(d.destination)}>Show in Folder</button>
        <button class="primary" onclick={() => d && void openPath(d.destination)}>Open</button>
      {:else}
        {#if canResume}
          <button class="primary" onclick={() => void resume()}>{failed ? 'Retry' : 'Resume'}</button>
        {:else if canPause}
          <button class="ghost" onclick={() => void pause()}>Pause</button>
        {/if}
        <button class="ghost danger" onclick={() => void cancel()}>Cancel</button>
      {/if}
    </div>
  </footer>
</div>

<style>
  .popup {
    display: flex;
    flex-direction: column;
    /* content-sized (no fixed height): onMount resizes the window to fit this, so
       there's no empty space and no clipping */
    /* the app's panel surface, not the darker --bg canvas, so the window tone
       matches the rest of the app rather than reading as a too-dark box */
    background: var(--surface);
    color: var(--text);
    font-size: var(--text-sm);
    overflow: hidden;
  }
  /* clears the floating macOS traffic lights and gives the window a drag handle */
  .drag {
    height: 28px;
    flex-shrink: 0;
  }
  .body {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    padding: 0 var(--space-5) var(--space-4);
  }

  .title {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }
  .name {
    flex: 1;
    min-width: 0;
    font-size: var(--text-lg);
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .badge {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    flex-shrink: 0;
    padding: 2px var(--space-2);
    border-radius: 999px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: var(--text-xs);
    font-weight: 500;
  }
  .progress {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }
  .bar {
    flex: 1;
    height: 8px;
    border-radius: 999px;
    background: var(--track, var(--surface-2));
    overflow: hidden;
  }
  .fill {
    height: 100%;
    border-radius: 999px;
    background: var(--accent);
    transition: width 0.25s ease;
  }
  .fill.completed {
    background: var(--success);
  }
  .fill.paused,
  .fill.failed {
    background: var(--faint);
  }
  .fill.indeterminate {
    width: 35% !important;
    animation: slide 1.1s ease-in-out infinite;
  }
  @keyframes slide {
    0% {
      transform: translateX(-100%);
    }
    100% {
      transform: translateX(340%);
    }
  }
  .pct {
    min-width: 3.5em;
    text-align: right;
    font-size: var(--text-md);
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }

  .stats {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-2) var(--space-5);
    margin: 0;
  }
  .stats div {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-3);
    border-bottom: 1px solid var(--border);
    padding-bottom: var(--space-2);
  }
  .stats dt {
    color: var(--muted);
    font-size: var(--text-xs);
  }
  .stats dd {
    margin: 0;
    font-variant-numeric: tabular-nums;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .segbar {
    display: flex;
    gap: 2px;
    height: 6px;
  }
  .seg {
    background: var(--track, var(--surface-2));
    border-radius: 2px;
    overflow: hidden;
  }
  .seg-fill {
    height: 100%;
    background: var(--accent);
    transition: width 0.25s ease;
  }

  .path {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
    min-width: 0;
  }
  .plabel {
    color: var(--muted);
    font-size: var(--text-xs);
    flex-shrink: 0;
  }
  .pval {
    color: var(--faint);
    font-size: var(--text-xs);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    direction: rtl;
    text-align: left;
  }

  .foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--space-3) var(--space-5);
    border-top: 1px solid var(--border);
    flex-shrink: 0;
  }
  .actions {
    display: flex;
    gap: var(--space-2);
  }
  button {
    padding: var(--space-2) var(--space-4);
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-strong);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-weight: 500;
    cursor: pointer;
  }
  button:hover {
    background: var(--surface-2);
  }
  button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }
  .primary {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-contrast);
  }
  .primary:hover {
    background: var(--accent-hover);
  }
  .danger:hover {
    color: var(--danger);
    border-color: var(--danger);
  }
  .link {
    padding: var(--space-2) 0;
    border: 0;
    background: none;
    color: var(--muted);
    font-weight: 500;
  }
  .link:hover {
    background: none;
    color: var(--text);
  }
</style>
