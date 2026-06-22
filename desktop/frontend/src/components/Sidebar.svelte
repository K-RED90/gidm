<script lang="ts">
  import { store, type Category } from '../lib/store.svelte'

  const items: { id: Category; label: string }[] = [
    { id: 'all', label: 'All Downloads' },
    { id: 'active', label: 'Active' },
    { id: 'completed', label: 'Completed' },
    { id: 'failed', label: 'Paused & Failed' },
  ]

  // File-type categories (Compressed/Documents/…) are part of the IDM feature map
  // but not wired yet; the section is scaffolded so the layout already has a home
  // for them.
  const fileTypes = ['Compressed', 'Documents', 'Music', 'Programs', 'Video']
</script>

<aside class="sidebar">
  <div class="drag" style="--wails-draggable: drag"></div>
  <div class="brand">gidm</div>

  <nav class="nav">
    {#each items as item (item.id)}
      <button
        class="nav-item"
        class:active={store.category === item.id}
        onclick={() => (store.category = item.id)}
      >
        <span class="nav-label">{item.label}</span>
        <span class="nav-count">{store.count(item.id)}</span>
      </button>
    {/each}
  </nav>

  <div class="section-title">Categories</div>
  <nav class="nav muted">
    {#each fileTypes as t (t)}
      <button class="nav-item" disabled>
        <span class="nav-label">{t}</span>
      </button>
    {/each}
  </nav>
</aside>

<style>
  .sidebar {
    display: flex;
    flex-direction: column;
    height: 100vh;
    background: var(--surface);
    border-right: 1px solid var(--border);
    padding: 0 var(--space-2) var(--space-3);
    overflow-y: auto;
  }
  .drag {
    height: var(--titlebar);
    flex: none;
  }
  .brand {
    font-size: var(--text-lg);
    font-weight: 700;
    letter-spacing: -0.02em;
    padding: var(--space-2) var(--space-3) var(--space-4);
  }
  .nav {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .nav-item {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    width: 100%;
    padding: var(--space-2) var(--space-3);
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: var(--text-sm);
    text-align: left;
    cursor: pointer;
  }
  .nav-item:hover:not(:disabled) {
    background: var(--surface-2);
  }
  .nav-item.active {
    background: color-mix(in srgb, var(--accent) 16%, transparent);
    color: var(--accent);
    font-weight: 600;
  }
  .nav-item:disabled {
    color: var(--muted);
    cursor: default;
    opacity: 0.6;
  }
  .nav-label {
    flex: 1;
  }
  .nav-count {
    font-variant-numeric: tabular-nums;
    font-size: var(--text-xs);
    color: var(--muted);
  }
  .nav-item.active .nav-count {
    color: var(--accent);
  }
  .section-title {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--muted);
    padding: var(--space-4) var(--space-3) var(--space-2);
  }
</style>
