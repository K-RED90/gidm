<script lang="ts">
  import { Bridge } from '../lib/bridge'
  import { store } from '../lib/store.svelte'

  let url = $state('')
  let error = $state('')
  let busy = $state(false)

  const canAdd = $derived(store.daemonUp && !busy && url.trim().length > 0)

  async function add(): Promise<void> {
    const u = url.trim()
    if (!u || busy) return
    busy = true
    error = ''
    try {
      await Bridge.Add(u)
      url = ''
      await store.refresh()
    } catch (e) {
      error = e instanceof Error ? e.message : String(e)
    } finally {
      busy = false
    }
  }

  function onkeydown(e: KeyboardEvent): void {
    if (e.key === 'Enter') void add()
  }
</script>

<!-- The whole bar is a window-drag region except the interactive field. -->
<div class="addbar" style="--wails-draggable: drag">
  <div class="field" class:invalid={error} style="--wails-draggable: no-drag">
    <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
      <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
    </svg>
    <input
      type="text"
      placeholder="Paste a download URL…"
      bind:value={url}
      {onkeydown}
      disabled={!store.daemonUp || busy}
      spellcheck="false"
      autocomplete="off"
      aria-label="Download URL"
    />
    <button class="add" onclick={add} disabled={!canAdd}>Add</button>
  </div>
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</div>

<style>
  .addbar {
    padding: var(--titlebar) var(--space-5) var(--space-3);
    background: var(--bg);
  }
  .field {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-2) var(--space-2) var(--space-2) var(--space-3);
  }
  .field:focus-within {
    border-color: var(--accent);
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 18%, transparent);
  }
  .field.invalid {
    border-color: var(--danger);
  }
  .icon {
    width: 18px;
    height: 18px;
    color: var(--muted);
    flex: none;
  }
  input {
    flex: 1;
    min-width: 0;
    border: 0;
    outline: 0;
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: var(--text-md);
  }
  input::placeholder {
    color: var(--muted);
  }
  .add {
    flex: none;
    border: 0;
    border-radius: var(--radius-sm);
    background: var(--accent);
    color: var(--accent-contrast);
    font: inherit;
    font-weight: 600;
    font-size: var(--text-sm);
    padding: var(--space-2) var(--space-4);
    cursor: pointer;
  }
  .add:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .error {
    margin: var(--space-2) 0 0;
    color: var(--danger);
    font-size: var(--text-sm);
  }
</style>
