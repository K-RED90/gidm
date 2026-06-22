<script lang="ts">
  import Icon from './Icon.svelte'
  import type { IconName } from '../lib/icons'
  import { Bridge, DownloadStatus, Priority, copyText, openPath, revealPath, type DownloadView } from '../lib/bridge'
  import { store } from '../lib/store.svelte'
  import { menu, type MenuItem } from '../lib/menu.svelte'
  import { priorityMenuItems, rateMenuItems } from '../lib/actions'
  import { toaster, errMessage } from '../lib/toast.svelte'
  import { PAUSABLE, RESUMABLE, STATUS_LABEL } from '../lib/status'
  import { capLabel, categoryOf, fileName, humanEta, humanSize, humanSpeed, percent, type FileCategory } from '../lib/format'

  // onDetails opens the Properties dialog (owned by App) for a single download.
  let { onDetails }: { onDetails: (id: string) => void } = $props()

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
      toaster.error(errMessage(e))
    }
  }

  // Single-selection extras: surface the outcome so the action never looks inert.
  async function doCopyUrl(url: string): Promise<void> {
    try {
      await copyText(url)
      toaster.success('URL copied to clipboard')
    } catch (e) {
      toaster.error(`Copy failed: ${errMessage(e)}`)
    }
  }

  async function doOpen(path: string): Promise<void> {
    try {
      await openPath(path)
    } catch (e) {
      toaster.error(`Open failed: ${errMessage(e)}`)
    }
  }

  async function doReveal(path: string): Promise<void> {
    try {
      await revealPath(path)
    } catch (e) {
      toaster.error(`Reveal failed: ${errMessage(e)}`)
    }
  }

  async function cyclePriority(d: DownloadView): Promise<void> {
    try {
      await store.setPriority(d.id, NEXT_PRIORITY[d.priority] ?? Priority.PriorityNormal)
    } catch (e) {
      toaster.error(errMessage(e))
    }
  }

  const selecting = $derived(store.selectedIds.size > 0)

  // segView builds the IDM-style per-connection slices: one slice per segment,
  // sized to its share of the file and filled to its own progress. Returns null
  // when there is nothing useful to show (single segment, no segments yet, or an
  // unknown total) so the row falls back to the plain bar.
  function segView(d: DownloadView): { grow: number; pct: number }[] | null {
    const segs = d.segments
    if (!segs || segs.length < 2 || d.total_size <= 0) return null
    return segs.map((s) => {
      const size = s.end - s.start + 1
      return { grow: size, pct: size > 0 ? Math.min(100, (s.completed / size) * 100) : 0 }
    })
  }

  // --- selection interactions -------------------------------------------------

  function onRowClick(e: MouseEvent, d: DownloadView): void {
    // clicks on row controls (checkbox / priority / action buttons) act on their
    // own; they should not also move the selection.
    if ((e.target as HTMLElement).closest('button')) return
    if (e.shiftKey) store.selectRangeTo(d.id)
    else if (e.metaKey || e.ctrlKey) store.toggle(d.id)
    else store.selectOnly(d.id)
  }

  function onRowDblClick(e: MouseEvent, d: DownloadView): void {
    if ((e.target as HTMLElement).closest('button')) return
    // a finished file opens directly; anything else opens its properties.
    if (d.status === DownloadStatus.StatusCompleted) void doOpen(d.destination)
    else onDetails(d.id)
  }

  function onRowContextMenu(e: MouseEvent, d: DownloadView): void {
    e.preventDefault()
    // right-clicking a row outside the selection replaces it (native convention).
    if (!store.selectedIds.has(d.id)) store.selectOnly(d.id)
    menu.show(e.clientX, e.clientY, buildRowMenu(d))
  }

  function onWrapClick(e: MouseEvent): void {
    if (!(e.target as HTMLElement).closest('tr')) store.clearSelection()
  }

  function buildRowMenu(d: DownloadView): MenuItem[] {
    const n = store.selectedIds.size
    const tag = n > 1 ? ` (${n})` : ''
    const items: MenuItem[] = [
      { kind: 'item', label: `Pause${tag}`, icon: 'pause', disabled: !store.canPauseSelected, run: () => store.pauseSelected() },
      { kind: 'item', label: `Resume${tag}`, icon: 'play', disabled: !store.canResumeSelected, run: () => store.resumeSelected() },
      { kind: 'submenu', label: 'Set Priority', icon: 'sliders', items: priorityMenuItems() },
    ]
    if (n === 1) {
      items.push({ kind: 'item', label: 'Restart', icon: 'refresh', run: () => void store.restart(d.id) })
      items.push({ kind: 'submenu', label: 'Limit speed', icon: 'gauge', items: rateMenuItems(d) })
      items.push({ kind: 'sep' })
      if (d.status === DownloadStatus.StatusCompleted) {
        items.push({ kind: 'item', label: 'Open file', icon: 'externalLink', run: () => void doOpen(d.destination) })
      }
      items.push({ kind: 'item', label: 'Reveal in folder', icon: 'folderOpen', run: () => void doReveal(d.destination) })
      items.push({ kind: 'item', label: 'Copy URL', icon: 'copy', run: () => void doCopyUrl(d.url) })
      items.push({ kind: 'item', label: 'Properties…', icon: 'info', run: () => onDetails(d.id) })
    }
    items.push({ kind: 'sep' })
    items.push({ kind: 'item', label: `Remove${tag}`, icon: 'trash', danger: true, run: () => store.removeSelected() })
    return items
  }

  // --- keyboard ---------------------------------------------------------------

  function isTyping(e: KeyboardEvent): boolean {
    return !!(e.target as HTMLElement)?.closest('input, textarea, [contenteditable="true"]')
  }

  function onWindowKeydown(e: KeyboardEvent): void {
    if (menu.open || store.modalOpen || isTyping(e)) return
    if ((e.metaKey || e.ctrlKey) && (e.key === 'a' || e.key === 'A')) {
      e.preventDefault()
      store.selectAll()
      return
    }
    switch (e.key) {
      case 'Escape':
        store.clearSelection()
        break
      case 'ArrowDown':
        e.preventDefault()
        moveFocus(1, e.shiftKey)
        break
      case 'ArrowUp':
        e.preventDefault()
        moveFocus(-1, e.shiftKey)
        break
      case 'Delete':
      case 'Backspace':
        if (store.selectedIds.size > 0) {
          e.preventDefault()
          void store.removeSelected()
        }
        break
      case ' ':
        if (store.selectedIds.size > 0) {
          e.preventDefault()
          void (store.canPauseSelected ? store.pauseSelected() : store.resumeSelected())
        }
        break
    }
  }

  function moveFocus(delta: number, extend: boolean): void {
    const list = store.filtered
    if (list.length === 0) return
    const cur = list.findIndex((d) => d.id === store.focusId)
    const idx = cur === -1 ? (delta > 0 ? 0 : list.length - 1) : Math.min(list.length - 1, Math.max(0, cur + delta))
    const id = list[idx].id
    if (extend) store.selectRangeTo(id)
    else store.selectOnly(id)
    requestAnimationFrame(() => document.querySelector(`tr[data-id="${id}"]`)?.scrollIntoView({ block: 'nearest' }))
  }
</script>

<svelte:window onkeydown={onWindowKeydown} />

<div
  class="table-wrap"
  role="presentation"
  onclick={onWrapClick}
>
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
      <colgroup>
        <col class="c-sel" />
        <col class="c-name" />
        <col class="c-size" />
        <col class="c-progress" />
        <col class="c-speed" />
        <col class="c-eta" />
        <col class="c-prio" />
        <col class="c-status" />
        <col class="c-actions" />
      </colgroup>
      <thead>
        <tr>
          <th class="col-sel">
            <button
              class="cbox"
              class:on={store.allFilteredSelected}
              role="checkbox"
              aria-checked={store.allFilteredSelected ? 'true' : store.someFilteredSelected ? 'mixed' : 'false'}
              aria-label="Select all"
              onclick={() => (store.allFilteredSelected ? store.clearSelection() : store.selectAll())}
            >
              {#if store.allFilteredSelected}
                <Icon name="check" size={12} />
              {:else if store.someFilteredSelected}
                <Icon name="minus" size={12} />
              {/if}
            </button>
          </th>
          <th class="col-name">Name</th>
          <th class="col-size num">Size</th>
          <th class="col-progress">Progress</th>
          <th class="col-speed num">Speed</th>
          <th class="col-eta num">ETA</th>
          <th class="col-prio">Priority</th>
          <th class="col-status">Status</th>
          <th class="col-actions"></th>
        </tr>
      </thead>
      <tbody>
        {#each store.filtered as d (d.id)}
          {@const pct = percent(d)}
          {@const selected = store.selectedIds.has(d.id)}
          {@const seg = segView(d)}
          <tr
            data-id={d.id}
            class:selected
            aria-selected={selected}
            tabindex={store.focusId === d.id ? 0 : -1}
            onclick={(e) => onRowClick(e, d)}
            ondblclick={(e) => onRowDblClick(e, d)}
            oncontextmenu={(e) => onRowContextMenu(e, d)}
          >
            <td class="col-sel">
              <button
                class="cbox"
                class:on={selected}
                role="checkbox"
                aria-checked={selected}
                aria-label="Select {fileName(d)}"
                onclick={(e) => {
                  e.stopPropagation()
                  store.toggle(d.id)
                }}
              >
                {#if selected}<Icon name="check" size={12} />{/if}
              </button>
            </td>
            <td class="col-name" title={d.destination || d.url}>
              <div class="name">
                <span class="ficon"><Icon name={TYPE_ICON[categoryOf(d)]} size={16} /></span>
                <span class="fname">{fileName(d)}</span>
                {#if (d.max_rate ?? 0) > 0}
                  <span class="caplimit" title="Speed limit">{capLabel(d.max_rate ?? 0)}</span>
                {/if}
              </div>
            </td>
            <td class="col-size num">{d.total_size > 0 ? humanSize(d.total_size) : '—'}</td>
            <td class="col-progress">
              <div class="progress">
                {#if seg}
                  <div class="segbar" title="{seg.length} connections" aria-hidden="true">
                    {#each seg as s, i (i)}
                      <div class="seg" style:flex-grow={s.grow}>
                        <div class="seg-fill" style:width="{s.pct}%"></div>
                      </div>
                    {/each}
                  </div>
                {:else}
                  <div class="bar" style:--pct="{pct ?? 0}%">
                    <div
                      class="bar-fill {d.status}"
                      class:indeterminate={pct === null && d.status === DownloadStatus.StatusActive}
                    ></div>
                  </div>
                {/if}
                <span class="pct">{pct === null ? '' : d.status === DownloadStatus.StatusCompleted ? '' : `${Math.round(pct)}%`}</span>
              </div>
            </td>
            <td class="col-speed num">{humanSpeed(d.speed_bps)}</td>
            <td class="col-eta num">{humanEta(d.eta_secs)}</td>
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
              <span class="status">
                <i class="dot {d.status}"></i>
                {STATUS_LABEL[d.status] ?? d.status}
              </span>
            </td>
            <td class="col-actions">
              {#if !selecting}
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
              {/if}
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
    table-layout: fixed;
    border-collapse: collapse;
    font-size: var(--text-sm);
  }

  /* Column widths — single source of truth via colgroup; Name has no width and
     absorbs the remaining space. table-layout:fixed makes header + body align. */
  .c-sel {
    width: 38px;
  }
  .c-size {
    width: 84px;
  }
  .c-progress {
    width: 196px;
  }
  .c-speed {
    width: 94px;
  }
  .c-eta {
    width: 72px;
  }
  .c-prio {
    width: 88px;
  }
  .c-status {
    width: 110px;
  }
  .c-actions {
    width: 100px;
  }

  thead th {
    position: sticky;
    top: 0;
    z-index: 1;
    text-align: left;
    font-weight: 600;
    color: var(--faint);
    font-size: var(--text-xs);
    padding: var(--space-2) var(--space-3);
    background: var(--bg);
    border-bottom: 1px solid var(--border);
  }
  thead th.num {
    text-align: right;
  }

  tbody td {
    height: var(--row-h);
    padding: 0 var(--space-3);
    border-bottom: 1px solid color-mix(in srgb, var(--border) 50%, transparent);
    vertical-align: middle;
    white-space: nowrap;
    overflow: hidden;
    color: var(--text);
  }
  tbody tr {
    cursor: default;
  }
  tbody tr:hover:not(.selected) {
    background: var(--surface-2);
  }
  tbody tr.selected {
    background: var(--surface-3);
  }
  tbody tr.selected:hover {
    background: color-mix(in srgb, var(--surface-3) 82%, var(--accent-soft));
  }
  /* accent edge on the selected row, drawn on the first cell */
  tbody tr.selected > td.col-sel {
    box-shadow: inset 2px 0 0 0 var(--accent);
  }
  tbody tr:focus-visible {
    outline: 1px solid var(--border-strong);
    outline-offset: -1px;
  }

  /* selection checkbox */
  .col-sel {
    text-align: center;
  }
  .cbox {
    display: inline-grid;
    place-items: center;
    width: 16px;
    height: 16px;
    border: 1.5px solid var(--border-strong);
    border-radius: 5px;
    background: var(--bg);
    color: var(--accent-contrast);
    cursor: pointer;
    transition: background 0.12s ease, border-color 0.12s ease;
  }
  .cbox:hover {
    border-color: var(--faint);
  }
  .cbox.on {
    background: var(--accent);
    border-color: var(--accent);
  }
  .cbox:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  /* dim the per-row checkbox until the row is hovered/selected, so the column
     reads as quiet whitespace until you reach for it */
  tbody .cbox:not(.on) {
    opacity: 0;
  }
  tbody tr:hover .cbox,
  tbody tr.selected .cbox,
  tbody .cbox:focus-visible {
    opacity: 1;
  }

  .col-name {
    font-weight: 500;
  }
  .name {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-width: 0;
  }
  .ficon {
    flex: none;
    color: var(--muted);
  }
  .fname {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  /* Per-download speed-cap badge: a quiet pill next to the name. */
  .caplimit {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 3px;
    padding: 1px 6px;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    font-size: 10.5px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    letter-spacing: 0.01em;
    color: var(--muted);
    background: var(--surface-2);
  }

  .num {
    text-align: right;
    font-variant-numeric: tabular-nums;
    color: var(--muted);
  }

  .progress {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  .bar {
    flex: 1;
    min-width: 0;
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

  /* per-connection bar: one track slice per segment, sized to its share */
  .segbar {
    flex: 1;
    min-width: 0;
    display: flex;
    gap: 1px;
    height: 5px;
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
    flex: none;
    width: 30px;
    text-align: right;
    font-variant-numeric: tabular-nums;
    color: var(--faint);
    font-size: var(--text-xs);
  }

  .prio {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    border: 1px solid color-mix(in srgb, var(--border-strong) 60%, transparent);
    border-radius: 999px;
    padding: 2px 8px;
    font-family: inherit;
    font-size: var(--text-xs);
    font-weight: 600;
    color: var(--faint);
    background: transparent;
    cursor: pointer;
    transition: background 0.1s, border-color 0.1s;
  }
  .prio::before {
    content: '';
    width: 5px;
    height: 5px;
    border-radius: 999px;
    background: var(--faint);
    flex-shrink: 0;
  }
  .prio.normal {
    color: var(--muted);
  }
  .prio.normal::before {
    background: var(--border-strong);
  }
  .prio.high {
    color: var(--accent);
    border-color: color-mix(in srgb, var(--accent) 30%, transparent);
  }
  .prio.high::before {
    background: var(--accent);
  }
  .prio:hover {
    background: var(--surface-2);
    border-color: var(--border-strong);
  }
  .prio:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }

  .dot { display: none; }

  .status {
    display: inline-flex;
    align-items: center;
    padding: 2px 9px;
    border-radius: 999px;
    font-size: var(--text-xs);
    font-weight: 600;
    background: var(--surface-2);
    color: var(--muted);
  }
  .status:has(.dot.active) {
    background: color-mix(in srgb, var(--accent) 13%, transparent);
    color: var(--accent);
  }
  .status:has(.dot.completed) {
    background: color-mix(in srgb, var(--success) 15%, transparent);
    color: var(--success);
  }
  .status:has(.dot.failed) {
    background: color-mix(in srgb, var(--danger) 15%, transparent);
    color: var(--danger);
  }
  .status:has(.dot.paused) {
    background: color-mix(in srgb, var(--warning) 15%, transparent);
    color: var(--warning);
  }

  .actions {
    display: flex;
    gap: var(--space-1);
    justify-content: flex-end;
    opacity: 0;
  }
  tr:hover .actions,
  tr:focus-within .actions,
  tr.selected .actions {
    opacity: 1;
  }
  .actions button {
    display: grid;
    place-items: center;
    border: 0;
    background: var(--surface-3);
    color: var(--muted);
    border-radius: var(--radius-sm);
    width: 26px;
    height: 26px;
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

  /* Drop the least-critical columns first as the window narrows. Hiding the
     th/td pair is what actually collapses the column under table-layout:fixed. */
  @media (max-width: 880px) {
    th.col-eta,
    td.col-eta {
      display: none;
    }
  }
  @media (max-width: 780px) {
    th.col-speed,
    td.col-speed {
      display: none;
    }
  }
</style>
