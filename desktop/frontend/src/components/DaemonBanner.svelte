<script lang="ts">
  import Icon from './Icon.svelte'
  import { store } from '../lib/store.svelte'

  let retrying = $state(false)

  async function retry(): Promise<void> {
    retrying = true
    try {
      await store.refresh()
    } finally {
      retrying = false
    }
  }
</script>

<div class="banner" role="alert">
  <span class="icon"><Icon name="alert" size={18} /></span>
  <div class="text">
    <strong>The gidm daemon isn’t reachable.</strong>
    <span>Downloads are paused until it’s running. The app tries to start it automatically.</span>
  </div>
  <button onclick={retry} disabled={retrying}>{retrying ? 'Retrying…' : 'Retry'}</button>
</div>

<style>
  .banner {
    flex: none;
    display: flex;
    align-items: center;
    gap: var(--space-3);
    margin: var(--space-4) var(--space-4) 0;
    padding: var(--space-3) var(--space-4);
    border: 1px solid color-mix(in srgb, var(--danger) 35%, var(--border));
    border-radius: var(--radius-md);
    background: color-mix(in srgb, var(--danger) 12%, var(--surface));
    font-size: var(--text-sm);
  }
  .icon {
    flex: none;
    display: grid;
    place-items: center;
    color: var(--danger);
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 2px;
    flex: 1;
    min-width: 0;
  }
  .text span {
    color: var(--muted);
  }
  button {
    flex: none;
    border: 1px solid var(--border-strong);
    background: var(--surface-2);
    color: var(--text);
    border-radius: var(--radius-sm);
    padding: var(--space-2) var(--space-3);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 550;
    cursor: pointer;
  }
  button:hover:not(:disabled) {
    background: var(--surface-3);
  }
  button:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
