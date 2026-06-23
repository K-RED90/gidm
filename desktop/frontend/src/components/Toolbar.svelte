<script lang="ts">
  import { fly } from 'svelte/transition'
  import Icon from './Icon.svelte'
  import { store, type SortDir, type SortKey } from '../lib/store.svelte'
  import { menu } from '../lib/menu.svelte'
  import { priorityMenuItems } from '../lib/actions'

  let { onAdd, onSettings }: { onAdd: () => void; onSettings: () => void } = $props()

  const SORTS: { label: string; key: SortKey; dir: SortDir }[] = [
    { label: 'Newest first', key: 'added', dir: 'desc' },
    { label: 'Oldest first', key: 'added', dir: 'asc' },
    { label: 'Name (A–Z)', key: 'name', dir: 'asc' },
    { label: 'Largest', key: 'size', dir: 'desc' },
    { label: 'Fastest', key: 'speed', dir: 'desc' },
    { label: 'Status', key: 'status', dir: 'asc' },
  ]

  let sortOpen = $state(false)

  const currentSort = $derived(
    SORTS.find((s) => s.key === store.sortKey && s.dir === store.sortDir) ?? SORTS[0],
  )
  const selecting = $derived(store.selectedIds.size > 0)
  const reduceMotion =
    typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  const flyIn = { y: -6, duration: reduceMotion ? 0 : 130 }

  function pickSort(s: (typeof SORTS)[number]): void {
    store.sortKey = s.key
    store.sortDir = s.dir
    sortOpen = false
  }

  function openPriority(e: MouseEvent): void {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    menu.show(r.left, r.bottom + 4, priorityMenuItems())
  }
</script>

<header class="toolbar" style="--wails-draggable: drag">
  <!-- Left: Add URL or selection badge -->
  {#if selecting}
    <div class="sel-badge" style="--wails-draggable: no-drag" in:fly={flyIn}>
      <button class="iconbtn" aria-label="Clear selection" title="Clear selection" onclick={() => store.clearSelection()}>
        <Icon name="close" size={15} />
      </button>
      <span class="count">{store.selectedIds.size} selected</span>
    </div>
  {:else}
    <button class="primary" style="--wails-draggable: no-drag" onclick={onAdd} disabled={!store.daemonUp}>
      <Icon name="plus" size={14} />
      Add URL
    </button>
  {/if}

  <!-- Center: search (always visible) -->
  <span class="flex1"></span>
  <div class="search" style="--wails-draggable: no-drag">
    <Icon name="search" size={13} />
    <input
      type="text"
      placeholder="Search"
      bind:value={store.query}
      spellcheck="false"
      autocomplete="off"
      aria-label="Search downloads"
    />
  </div>
  <span class="flex1"></span>

  <!-- Right cluster: selection actions OR global controls -->
  <div class="cluster" style="--wails-draggable: no-drag">
    {#if selecting}
      <button class="ghost" disabled={!store.canPauseSelected} onclick={() => store.pauseSelected()}>
        <Icon name="pause" size={14} />
        <span class="lbl">Pause</span>
      </button>
      <button class="ghost" disabled={!store.canResumeSelected} onclick={() => store.resumeSelected()}>
        <Icon name="play" size={14} />
        <span class="lbl">Resume</span>
      </button>
      <button class="ghost" onclick={openPriority} aria-haspopup="menu">
        <Icon name="sliders" size={14} />
        <span class="lbl">Priority</span>
        <Icon name="chevronDown" size={13} />
      </button>
      <button class="ghost danger" onclick={() => store.removeSelected()}>
        <Icon name="trash" size={14} />
        <span class="lbl">Remove</span>
      </button>
    {:else}
      <div class="sortwrap">
        <button
          class="iconbtn"
          class:active={sortOpen}
          onclick={() => (sortOpen = !sortOpen)}
          aria-haspopup="menu"
          aria-expanded={sortOpen}
          aria-label="Sort: {currentSort.label}"
          title="Sort: {currentSort.label}"
        >
          <Icon name="sort" size={15} />
          <Icon name="chevronDown" size={11} />
        </button>
        {#if sortOpen}
          <button class="menu-scrim" tabindex="-1" aria-label="Close menu" onclick={() => (sortOpen = false)}></button>
          <div class="menu" role="menu">
            {#each SORTS as s (s.label)}
              <button
                class="menu-item"
                class:selected={s === currentSort}
                role="menuitemradio"
                aria-checked={s === currentSort}
                onclick={() => pickSort(s)}
              >
                {s.label}
                {#if s === currentSort}<Icon name="check" size={14} />{/if}
              </button>
            {/each}
          </div>
        {/if}
      </div>

      <span class="divider" aria-hidden="true"></span>

      <button
        class="iconbtn"
        disabled={!store.hasPaused || !store.daemonUp}
        onclick={() => store.resumeAll()}
        title="Resume all paused"
        aria-label="Resume all paused"
      >
        <Icon name="play" size={15} />
      </button>
      <button
        class="iconbtn"
        disabled={!store.hasActive || !store.daemonUp}
        onclick={() => store.pauseAll()}
        title="Pause all active"
        aria-label="Pause all active"
      >
        <Icon name="pause" size={15} />
      </button>

      <span class="divider" aria-hidden="true"></span>
    {/if}

    <button
      class="iconbtn"
      onclick={onSettings}
      title="Settings"
      aria-label="Settings"
    >
      <Icon name="settings" size={16} />
    </button>
  </div>
</header>

<style>
  .toolbar {
    grid-area: toolbar;
    display: flex;
    align-items: center;
    gap: var(--space-2);
    height: var(--toolbar-h);
    padding: 0 var(--space-3);
    background: color-mix(in srgb, var(--surface) 88%, transparent);
    backdrop-filter: blur(24px) saturate(1.5);
    -webkit-backdrop-filter: blur(24px) saturate(1.5);
    border-bottom: 1px solid var(--border);
  }

  /* ---- Selection badge (replaces Add URL when selecting) ---------------- */
  .sel-badge {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    flex-shrink: 0;
  }
  .count {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--text);
    font-variant-numeric: tabular-nums;
  }

  /* ---- Layout helpers --------------------------------------------------- */
  .flex1 {
    flex: 1;
    min-width: var(--space-3);
  }
  .cluster {
    display: flex;
    align-items: center;
    gap: 2px;
    flex-shrink: 0;
  }
  .divider {
    width: 1px;
    height: 14px;
    background: var(--border-strong);
    flex-shrink: 0;
    margin: 0 var(--space-1);
  }

  /* ---- Base button ------------------------------------------------------ */
  button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 5px;
    border: 1px solid transparent;
    border-radius: var(--radius-sm);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 550;
    cursor: pointer;
    white-space: nowrap;
    transition: background 0.1s, color 0.1s, opacity 0.1s;
  }
  button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  button:disabled {
    opacity: 0.3;
    cursor: default;
  }

  /* Primary — pill shape */
  .primary {
    background: var(--accent);
    color: var(--accent-contrast);
    height: 30px;
    padding: 0 var(--space-4);
    font-weight: 600;
    border-radius: 999px;
    flex-shrink: 0;
  }
  .primary:hover:not(:disabled) {
    background: var(--accent-hover);
  }

  /* Icon button */
  .iconbtn {
    width: 28px;
    height: 28px;
    padding: 0;
    background: transparent;
    color: var(--muted);
    border-radius: var(--radius-sm);
  }
  .iconbtn:hover:not(:disabled),
  .iconbtn.active {
    background: var(--surface-2);
    color: var(--text);
  }
  /* Sort button has two icons so needs a bit more width */
  .sortwrap .iconbtn {
    width: auto;
    padding: 0 var(--space-2);
    gap: 2px;
  }

  /* Ghost buttons (selection bar) */
  .ghost {
    background: transparent;
    color: var(--muted);
    height: 28px;
    padding: 0 var(--space-2);
  }
  .ghost:hover:not(:disabled) {
    background: var(--surface-2);
    color: var(--text);
  }
  .ghost.danger { color: var(--danger); }
  .ghost.danger:hover:not(:disabled) {
    background: color-mix(in srgb, var(--danger) 16%, transparent);
    color: var(--danger);
  }

  /* Menu scrim — reset button defaults, fills the viewport */
  .menu-scrim {
    position: fixed;
    inset: 0;
    z-index: 30;
    width: auto;
    height: auto;
    border: 0;
    border-radius: 0;
    background: transparent;
    cursor: default;
  }

  /* ---- Search field ----------------------------------------------------- */
  .search {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    width: clamp(160px, 28vw, 400px);
    padding: 0 var(--space-3);
    height: 30px;
    background: var(--surface-3);
    border: 1px solid transparent;
    border-radius: 999px;
    color: var(--muted);
    transition: border-color 0.15s, background 0.15s;
    flex-shrink: 0;
  }
  .search:focus-within {
    border-color: color-mix(in srgb, var(--accent) 45%, transparent);
    background: color-mix(in srgb, var(--bg) 80%, var(--surface-3));
    color: var(--text);
  }
  .search input {
    flex: 1;
    min-width: 0;
    border: 0;
    outline: 0;
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: var(--text-sm);
  }
  .search input::placeholder {
    color: var(--muted);
  }

  /* ---- Sort dropdown ---------------------------------------------------- */
  .sortwrap {
    position: relative;
  }
  .menu {
    position: absolute;
    z-index: 31;
    top: calc(100% + 6px);
    right: 0;
    min-width: 184px;
    padding: var(--space-1);
    background: var(--surface);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-overlay);
  }
  .menu-item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    height: auto;
    padding: var(--space-2) var(--space-3);
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--text);
    font-weight: 500;
    text-align: left;
  }
  .menu-item:hover { background: var(--surface-2); }
  .menu-item.selected { color: var(--accent); }

  /* Collapse selbar labels on narrow windows */
  @media (max-width: 860px) {
    .ghost .lbl { display: none; }
  }
</style>
