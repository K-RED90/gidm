<script lang="ts">
  import { fade, scale } from 'svelte/transition'
  import Icon from './Icon.svelte'
  import { DownloadStatus, copyText, openPath, revealPath, type DownloadView } from '../lib/bridge'
  import { store } from '../lib/store.svelte'
  import { STATUS_LABEL } from '../lib/status'
  import { fileName, humanEta, humanSize, humanSpeed, percent } from '../lib/format'

  // id is the download to inspect, or null when closed. The detail is derived live
  // from the store's polled snapshot, so the dialog updates every tick on its own.
  let { id = null, onClose }: { id?: string | null; onClose: () => void } = $props()

  const FOCUSABLE = 'a[href],button:not([disabled]),input:not([disabled]),[tabindex]:not([tabindex="-1"])'
  const PRIORITY_LABEL: Record<string, string> = { low: 'Low', normal: 'Normal', high: 'High' }

  const open = $derived(id !== null)
  const d = $derived<DownloadView | undefined>(id ? store.downloads.find((x) => x.id === id) : undefined)
  const pct = $derived(d ? percent(d) : null)

  // Per-connection slices for the breakdown (present for in-flight downloads).
  const conns = $derived.by(() => {
    const segs = d?.segments
    if (!segs || segs.length === 0) return []
    return segs.map((s) => {
      const size = s.end - s.start + 1
      return { index: s.index, size, done: s.completed, pct: size > 0 ? Math.min(100, (s.completed / size) * 100) : 0 }
    })
  })

  const reduceMotion =
    typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  const motionMs = reduceMotion ? 0 : 150

  let cardEl = $state<HTMLElement>()

  function fmtDate(iso: string | undefined): string {
    if (!iso) return '—'
    const t = new Date(iso)
    return Number.isNaN(t.getTime()) ? '—' : t.toLocaleString()
  }

  function onWindowKeydown(e: KeyboardEvent): void {
    if (!open) return
    if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    } else if (e.key === 'Tab') {
      trapTab(e)
    }
  }

  function trapTab(e: KeyboardEvent): void {
    if (!cardEl) return
    const items = [...cardEl.querySelectorAll<HTMLElement>(FOCUSABLE)]
    if (items.length === 0) return
    const first = items[0]
    const last = items[items.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }
</script>

<svelte:window onkeydown={onWindowKeydown} />

{#if open && d}
  <button class="scrim" tabindex="-1" aria-label="Close" onclick={onClose} transition:fade={{ duration: motionMs }}></button>

  <div class="wrap">
    <div
      class="card"
      role="dialog"
      aria-modal="true"
      aria-labelledby="details-title"
      bind:this={cardEl}
      transition:scale={{ start: 0.96, opacity: 0, duration: motionMs }}
    >
      <header class="head">
        <div class="title">
          <span class="pctbig">{pct === null ? '' : `${Math.round(pct)}%`}</span>
          <h2 id="details-title" title={fileName(d)}>{fileName(d)}</h2>
        </div>
        <button type="button" class="x" aria-label="Close" onclick={onClose}>
          <Icon name="close" size={16} />
        </button>
      </header>

      <div class="body">
        <!-- overall progress -->
        <div class="overall">
          <div class="bar" style:--pct="{pct ?? 0}%">
            <div class="bar-fill {d.status}" class:indeterminate={pct === null && d.status === DownloadStatus.StatusActive}></div>
          </div>
        </div>

        <dl class="props">
          <div><dt>Status</dt><dd><i class="dot {d.status}"></i>{STATUS_LABEL[d.status] ?? d.status}</dd></div>
          <div><dt>Size</dt><dd>{d.total_size > 0 ? humanSize(d.total_size) : 'Unknown'}</dd></div>
          <div><dt>Downloaded</dt><dd>{humanSize(d.downloaded)}{pct === null ? '' : ` (${Math.round(pct)}%)`}</dd></div>
          <div><dt>Speed</dt><dd>{humanSpeed(d.speed_bps)}</dd></div>
          <div><dt>Time left</dt><dd>{humanEta(d.eta_secs)}</dd></div>
          <div><dt>Priority</dt><dd>{PRIORITY_LABEL[d.priority] ?? d.priority}</dd></div>
          <div><dt>Connections</dt><dd>{d.segment_count || conns.length || '—'}</dd></div>
          <div><dt>Added</dt><dd>{fmtDate(d.created_at)}</dd></div>
          <div class="wide"><dt>URL</dt><dd class="mono ellipsis" title={d.url}>{d.url}</dd></div>
          <div class="wide"><dt>Saved to</dt><dd class="mono ellipsis" title={d.destination}>{d.destination || '—'}</dd></div>
          {#if d.checksum}
            <div class="wide"><dt>Checksum</dt><dd class="mono ellipsis" title={d.checksum}>{d.checksum}</dd></div>
          {/if}
        </dl>

        {#if conns.length > 0}
          <section class="conns">
            <div class="conns-head">
              <span>Progress by connections</span>
              <span class="muted">{conns.length} active</span>
            </div>
            <!-- combined segmented bar -->
            <div class="segbar" aria-hidden="true">
              {#each conns as c (c.index)}
                <div class="seg" style:flex-grow={c.size}><div class="seg-fill" style:width="{c.pct}%"></div></div>
              {/each}
            </div>
            <!-- per-connection list -->
            <ul class="conn-list">
              {#each conns as c (c.index)}
                <li>
                  <span class="cn">{c.index + 1}</span>
                  <div class="cbar"><div class="cbar-fill" style:width="{c.pct}%"></div></div>
                  <span class="cdone">{humanSize(c.done)} / {humanSize(c.size)}</span>
                  <span class="cpct">{Math.round(c.pct)}%</span>
                </li>
              {/each}
            </ul>
          </section>
        {/if}
      </div>

      <footer class="foot">
        {#if d.status === DownloadStatus.StatusCompleted}
          <button type="button" class="ghost" onclick={() => void openPath(d.destination)}>
            <Icon name="externalLink" size={14} /> Open
          </button>
        {/if}
        <button type="button" class="ghost" onclick={() => void revealPath(d.destination)}>
          <Icon name="folderOpen" size={14} /> Reveal
        </button>
        <button type="button" class="ghost" onclick={() => void copyText(d.url)}>
          <Icon name="copy" size={14} /> Copy URL
        </button>
        <span class="spacer"></span>
        <button type="button" class="primary" onclick={onClose}>Close</button>
      </footer>
    </div>
  </div>
{/if}

<style>
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 40;
    border: 0;
    margin: 0;
    padding: 0;
    background: var(--scrim);
    cursor: default;
  }
  .wrap {
    position: fixed;
    inset: 0;
    z-index: 41;
    display: grid;
    place-items: center;
    padding: var(--space-5);
    pointer-events: none;
  }
  .card {
    pointer-events: auto;
    width: min(600px, 100%);
    max-height: calc(100vh - 2 * var(--space-5));
    background: color-mix(in srgb, var(--surface-2) 92%, transparent);
    backdrop-filter: blur(20px);
    -webkit-backdrop-filter: blur(20px);
    border: 1px solid rgba(255, 255, 255, 0.08);
    border-radius: var(--radius-lg);
    box-shadow: 0 0 0 1px rgba(0, 0, 0, 0.5), 0 8px 32px rgba(0, 0, 0, 0.6), 0 32px 100px rgba(0, 0, 0, 0.85);
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }
  .card :global(button):focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    padding: 20px 24px 12px;
    min-width: 0;
  }
  .title {
    display: flex;
    align-items: baseline;
    gap: var(--space-3);
    min-width: 0;
  }
  .pctbig {
    font-variant-numeric: tabular-nums;
    font-weight: 600;
    color: var(--accent);
    font-size: var(--text-md);
    flex: none;
  }
  .head h2 {
    margin: 0;
    font-family: var(--font-serif);
    font-size: var(--text-lg);
    font-weight: 600;
    letter-spacing: -0.01em;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }
  .x {
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    flex: none;
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--faint);
    cursor: pointer;
  }
  .x:hover {
    background: var(--surface-2);
    color: var(--text);
  }

  .body {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    padding: 0 24px 24px;
    overflow-y: auto;
  }

  .overall .bar {
    height: 8px;
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

  .props {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 6px;
    margin: 0;
  }
  .props > div {
    display: flex;
    flex-direction: column;
    gap: 3px;
    min-width: 0;
    padding: 10px 12px;
    background: var(--bg);
    border-radius: 8px;
  }
  .props .wide {
    grid-column: 1 / -1;
  }
  dt {
    font-size: var(--text-xs);
    font-weight: 600;
    color: var(--faint);
  }
  dd {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--text);
    display: flex;
    align-items: center;
    gap: 7px;
  }
  .mono {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--muted);
  }
  .ellipsis {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: block;
  }
  .dot {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 999px;
    background: var(--faint);
  }
  .dot.active {
    background: var(--accent);
  }
  .dot.completed {
    background: var(--success);
  }
  .dot.failed {
    background: var(--danger);
  }
  .dot.paused {
    background: var(--warning);
  }

  .conns {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding-top: var(--space-2);
    border-top: 1px solid var(--border);
  }
  .conns-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    font-size: var(--text-xs);
    font-weight: 600;
    color: var(--faint);
  }
  .conns-head .muted {
    color: var(--faint);
    font-variant-numeric: tabular-nums;
  }
  .segbar {
    display: flex;
    gap: 1px;
    height: 6px;
  }
  .seg {
    flex-basis: 0;
    min-width: 1px;
    height: 100%;
    background: var(--track);
    border-radius: 1px;
    overflow: hidden;
  }
  .seg:first-child {
    border-radius: 999px 1px 1px 999px;
  }
  .seg:last-child {
    border-radius: 1px 999px 999px 1px;
  }
  .seg-fill {
    height: 100%;
    background: var(--accent);
    transition: width 0.3s ease;
  }
  .conn-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    max-height: 168px;
    overflow-y: auto;
  }
  .conn-list li {
    display: grid;
    grid-template-columns: 18px 1fr auto auto;
    align-items: center;
    gap: var(--space-2);
    font-size: var(--text-xs);
    color: var(--muted);
  }
  .cn {
    color: var(--faint);
    font-variant-numeric: tabular-nums;
    text-align: right;
  }
  .cbar {
    height: 4px;
    border-radius: 999px;
    background: var(--track);
    overflow: hidden;
  }
  .cbar-fill {
    height: 100%;
    background: var(--accent);
    transition: width 0.3s ease;
  }
  .cdone {
    font-variant-numeric: tabular-nums;
  }
  .cpct {
    font-variant-numeric: tabular-nums;
    color: var(--faint);
    min-width: 30px;
    text-align: right;
  }

  .foot {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: 12px 24px 20px;
  }
  .foot .spacer {
    flex: 1;
  }
  .foot button {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    border: 0;
    font: inherit;
    font-weight: 600;
    font-size: var(--text-sm);
    cursor: pointer;
  }
  .ghost {
    background: transparent;
    color: var(--muted);
    border-radius: var(--radius-sm);
    padding: var(--space-2) var(--space-3);
  }
  .ghost:hover {
    background: var(--surface-2);
    color: var(--text);
  }
  .ghost :global(svg) {
    color: var(--muted);
  }
  .primary {
    background: var(--accent);
    color: var(--accent-contrast);
    border-radius: 999px;
    padding: 0 var(--space-4);
    height: 34px;
  }
  .primary:hover {
    background: var(--accent-hover);
  }

  @media (max-width: 540px) {
    .props {
      grid-template-columns: 1fr;
    }
  }
</style>
