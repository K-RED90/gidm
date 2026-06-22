<script lang="ts">
  import { store } from '../lib/store.svelte'
  import { humanSpeed } from '../lib/format'
</script>

<footer class="statusbar">
  <span class="status" class:up={store.daemonUp}>
    <span class="dot"></span>
    {store.daemonUp ? 'Daemon connected' : 'Daemon offline'}
  </span>
  <span class="spacer"></span>
  <span class="stat">{store.downloads.length} {store.downloads.length === 1 ? 'download' : 'downloads'}</span>
  <span class="stat total-speed">{humanSpeed(store.totalSpeed)}</span>
</footer>

<style>
  .statusbar {
    flex: none;
    display: flex;
    align-items: center;
    gap: var(--space-3);
    padding: var(--space-2) var(--space-5);
    border-top: 1px solid var(--border);
    background: var(--surface);
    font-size: var(--text-xs);
    color: var(--muted);
  }
  .status {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
  }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 999px;
    background: var(--danger);
  }
  .status.up .dot {
    background: var(--success);
  }
  .spacer {
    flex: 1;
  }
  .stat {
    font-variant-numeric: tabular-nums;
  }
  .total-speed {
    color: var(--text);
    font-weight: 600;
  }
</style>
