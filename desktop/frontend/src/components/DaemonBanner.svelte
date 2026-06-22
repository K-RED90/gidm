<script lang="ts">
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
  <span class="icon" aria-hidden="true">!</span>
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
    margin: 0 var(--space-5) var(--space-3);
    padding: var(--space-3) var(--space-4);
    border: 1px solid color-mix(in srgb, var(--danger) 40%, var(--border));
    border-radius: var(--radius-md);
    background: color-mix(in srgb, var(--danger) 12%, var(--surface));
    font-size: var(--text-sm);
  }
  .icon {
    flex: none;
    width: 22px;
    height: 22px;
    display: grid;
    place-items: center;
    border-radius: 999px;
    background: var(--danger);
    color: #fff;
    font-weight: 700;
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 2px;
    flex: 1;
  }
  .text span {
    color: var(--muted);
  }
  button {
    flex: none;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--text);
    border-radius: var(--radius-sm);
    padding: var(--space-2) var(--space-3);
    font: inherit;
    font-size: var(--text-sm);
    cursor: pointer;
  }
  button:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
