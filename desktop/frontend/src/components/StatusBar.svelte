<script lang="ts">
  import Icon from './Icon.svelte'
  import { store } from '../lib/store.svelte'
  import { humanSpeed } from '../lib/format'

  const total = $derived(store.downloads.length)
  const shown = $derived(store.filtered.length)
</script>

<footer class="statusbar">
  <span class="stat">
    {total}
    {total === 1 ? 'download' : 'downloads'}{#if shown !== total} · {shown} shown{/if}
  </span>
  <span class="spacer"></span>
  <span class="speed" class:live={store.totalSpeed > 0}>
    <Icon name="download" size={13} />
    {humanSpeed(store.totalSpeed)}
  </span>
</footer>

<style>
  .statusbar {
    grid-area: statusbar;
    display: flex;
    align-items: center;
    gap: var(--space-3);
    height: var(--statusbar-h);
    padding: 0 var(--space-4);
    border-top: 1px solid var(--border);
    background: var(--surface);
    font-size: var(--text-xs);
    color: var(--muted);
  }
  .stat {
    font-variant-numeric: tabular-nums;
  }
  .spacer {
    flex: 1;
  }
  .speed {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    font-variant-numeric: tabular-nums;
    color: var(--muted);
  }
  .speed.live {
    color: var(--text);
  }
</style>
