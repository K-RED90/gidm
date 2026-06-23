<script lang="ts">
  import Icon from './Icon.svelte'
  import Logo from './Logo.svelte'
  import type { IconName } from '../lib/icons'
  import { store, type Selection } from '../lib/store.svelte'

  type Item = { sel: Selection; label: string; icon: IconName }

  const statusItems: Item[] = [
    { sel: { kind: 'status', value: 'all' }, label: 'All Downloads', icon: 'inbox' },
    { sel: { kind: 'status', value: 'active' }, label: 'Active', icon: 'download' },
    { sel: { kind: 'status', value: 'completed' }, label: 'Completed', icon: 'check' },
    { sel: { kind: 'status', value: 'failed' }, label: 'Paused & Failed', icon: 'pause' },
  ]

  const typeItems: Item[] = [
    { sel: { kind: 'type', value: 'compressed' }, label: 'Compressed', icon: 'archive' },
    { sel: { kind: 'type', value: 'documents' }, label: 'Documents', icon: 'document' },
    { sel: { kind: 'type', value: 'music' }, label: 'Music', icon: 'music' },
    { sel: { kind: 'type', value: 'pictures' }, label: 'Pictures', icon: 'image' },
    { sel: { kind: 'type', value: 'programs' }, label: 'Programs', icon: 'program' },
    { sel: { kind: 'type', value: 'video' }, label: 'Video', icon: 'video' },
  ]

  function isActive(sel: Selection): boolean {
    return store.selection.kind === sel.kind && store.selection.value === sel.value
  }
</script>

<aside class="sidebar" style="--wails-draggable: drag">
  <div class="brand">
    <Logo size={18} />
  </div>
  <nav class="nav" style="--wails-draggable: no-drag">
    <p class="section">Status</p>
    {#each statusItems as item (item.label)}
      <button class="item" class:active={isActive(item.sel)} onclick={() => (store.selection = item.sel)}>
        <Icon name={item.icon} size={15} />
        <span class="label">{item.label}</span>
        <span class="count">{store.count(item.sel)}</span>
      </button>
    {/each}

    <p class="section">Categories</p>
    {#each typeItems as item (item.label)}
      {@const n = store.count(item.sel)}
      <button
        class="item"
        class:active={isActive(item.sel)}
        class:empty={n === 0}
        onclick={() => (store.selection = item.sel)}
      >
        <Icon name={item.icon} size={15} />
        <span class="label">{item.label}</span>
        <span class="count">{n}</span>
      </button>
    {/each}
  </nav>

</aside>

<style>
  .sidebar {
    grid-area: sidebar;
    display: flex;
    flex-direction: column;
    min-height: 0;
    background: var(--surface);
    border-right: 1px solid var(--border);
    padding: 0 var(--space-2) var(--space-2);
  }

  .brand {
    display: flex;
    align-items: center;
    height: var(--toolbar-h);
    flex-shrink: 0;
    /* 80px clears the macOS traffic-light button group (3 × 12px + gaps + 12px margin) */
    padding: 0 var(--space-3) 0 80px;
    margin-bottom: var(--space-2);
  }

  .nav {
    display: flex;
    flex-direction: column;
    gap: 1px;
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }

  .section {
    margin: var(--space-3) var(--space-3) var(--space-1);
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.07em;
    color: var(--faint);
  }
  .section:first-child {
    margin-top: var(--space-1);
  }

  .item {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    width: 100%;
    padding: var(--space-2) var(--space-3);
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--muted);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 500;
    text-align: left;
    cursor: pointer;
  }
  .item:hover {
    background: var(--surface-2);
    color: var(--text);
  }
  .item:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
  .item.active {
    background: var(--surface-3);
    color: var(--text);
  }
  .item.empty:not(.active) {
    color: var(--faint);
  }
  .label {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .count {
    font-variant-numeric: tabular-nums;
    font-size: var(--text-xs);
    color: var(--faint);
  }
  .item.active .count {
    color: var(--muted);
  }

</style>
