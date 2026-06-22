<script lang="ts">
  import Icon from './Icon.svelte'
  import { store, type SortDir, type SortKey } from '../lib/store.svelte'

  let { onAdd }: { onAdd: () => void } = $props()

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

  function pickSort(s: (typeof SORTS)[number]): void {
    store.sortKey = s.key
    store.sortDir = s.dir
    sortOpen = false
  }
</script>

<header class="toolbar" style="--wails-draggable: drag">
  <span class="brand">gidm</span>

  <div class="group" style="--wails-draggable: no-drag">
    <button class="primary" onclick={onAdd} disabled={!store.daemonUp}>
      <Icon name="plus" size={14} />
      Add URL
    </button>
    <button
      class="ghost"
      disabled={!store.hasPaused || !store.daemonUp}
      onclick={() => store.resumeAll()}
      title="Resume all paused"
      aria-label="Resume all paused"
    >
      <Icon name="play" size={14} />
      <span class="lbl">Resume all</span>
    </button>
    <button
      class="ghost"
      disabled={!store.hasActive || !store.daemonUp}
      onclick={() => store.pauseAll()}
      title="Pause all active"
      aria-label="Pause all active"
    >
      <Icon name="pause" size={14} />
      <span class="lbl">Pause all</span>
    </button>
  </div>

  <span class="spacer"></span>

  <div class="search" style="--wails-draggable: no-drag">
    <Icon name="search" size={14} />
    <input
      type="text"
      placeholder="Search downloads…"
      bind:value={store.query}
      spellcheck="false"
      autocomplete="off"
      aria-label="Search downloads"
    />
  </div>

  <div class="sortwrap" style="--wails-draggable: no-drag">
    <button
      class="sort"
      class:open={sortOpen}
      onclick={() => (sortOpen = !sortOpen)}
      aria-haspopup="menu"
      aria-expanded={sortOpen}
      aria-label="Sort downloads"
    >
      <Icon name="sort" size={14} />
      <span class="sort-label">{currentSort.label}</span>
      <Icon name="chevronDown" size={13} />
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
</header>

<style>
  .toolbar {
    grid-area: toolbar;
    display: flex;
    align-items: center;
    gap: var(--space-3);
    height: var(--toolbar-h);
    /* left padding clears the floating macOS traffic lights; the whole bar is the
       window drag region (interactive children opt out via --wails-draggable). */
    padding: 0 var(--space-4) 0 84px;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
  }

  .brand {
    font-family: var(--font-serif);
    font-size: var(--text-xl);
    font-weight: 600;
    letter-spacing: -0.01em;
    color: var(--text);
    padding-right: var(--space-2);
  }

  .group {
    display: flex;
    align-items: center;
    gap: var(--space-1);
  }

  button {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    border: 1px solid transparent;
    border-radius: var(--radius-sm);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 550;
    cursor: pointer;
    white-space: nowrap;
  }
  button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  .primary {
    background: var(--accent);
    color: var(--accent-contrast);
    height: 30px;
    padding: 0 var(--space-3);
    font-weight: 600;
  }
  .primary:hover:not(:disabled) {
    background: var(--accent-hover);
  }

  .ghost {
    background: transparent;
    color: var(--muted);
    height: 30px;
    padding: 0 var(--space-2);
  }
  .ghost:hover:not(:disabled) {
    background: var(--surface-2);
    color: var(--text);
  }

  button:disabled {
    opacity: 0.4;
    cursor: default;
  }

  .spacer {
    flex: 1;
  }

  .search {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    width: clamp(140px, 20vw, 240px);
    padding: 0 var(--space-3);
    height: 32px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    color: var(--muted);
  }
  .search:focus-within {
    border-color: var(--accent);
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

  .sortwrap {
    position: relative;
  }
  .sort {
    background: transparent;
    color: var(--muted);
    padding: var(--space-2) var(--space-2);
  }
  .sort:hover,
  .sort.open {
    background: var(--surface-2);
    color: var(--text);
  }
  .sort-label {
    font-variant-numeric: tabular-nums;
  }

  .menu-scrim {
    position: fixed;
    inset: 0;
    z-index: 30;
    border: 0;
    background: transparent;
    cursor: default;
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
    padding: var(--space-2) var(--space-3);
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--text);
    font-weight: 500;
    text-align: left;
  }
  .menu-item:hover {
    background: var(--surface-2);
  }
  .menu-item.selected {
    color: var(--accent);
  }

  /* Adapt the toolbar to narrow windows: secondary actions go icon-only
     (aria-labels keep them accessible), then the brand drops. */
  @media (max-width: 900px) {
    .ghost .lbl,
    .sort-label {
      display: none;
    }
  }
  @media (max-width: 720px) {
    .brand {
      display: none;
    }
  }
</style>
