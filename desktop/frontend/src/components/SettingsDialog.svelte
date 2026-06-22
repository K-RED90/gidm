<script lang="ts">
  import { fade, scale } from 'svelte/transition'
  import Icon from './Icon.svelte'
  import { Bridge, Priority, pickDirectory } from '../lib/bridge'
  import { store } from '../lib/store.svelte'
  import { toaster } from '../lib/toast.svelte'

  let { open = false, onClose }: { open?: boolean; onClose: () => void } = $props()

  const KB = 1024
  const MB = 1024 * 1024
  const MAX_SEGMENTS = 16
  const FOCUSABLE =
    'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])'
  const PRIORITIES: { value: Priority; label: string }[] = [
    { value: Priority.PriorityLow, label: 'Low' },
    { value: Priority.PriorityNormal, label: 'Normal' },
    { value: Priority.PriorityHigh, label: 'High' },
  ]

  let dir = $state('')
  let segments = $state(8)
  let priority = $state<Priority>(Priority.PriorityNormal)
  let maxRateVal = $state(0)
  let maxRateUnit = $state(MB)
  let perDlVal = $state(0)
  let perDlUnit = $state(MB)

  let loading = $state(false)
  let busy = $state(false)
  let error = $state('')
  let cardEl = $state<HTMLElement>()

  const reduceMotion =
    typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  const motionMs = reduceMotion ? 0 : 150

  const canSave = $derived(store.daemonUp && !busy && !loading && dir.trim().length > 0)

  // splitRate picks a friendly unit + value for a bytes/sec cap (0 → unlimited).
  function splitRate(bps: number): { value: number; unit: number } {
    if (bps <= 0) return { value: 0, unit: MB }
    if (bps >= MB) return { value: Math.round((bps / MB) * 100) / 100, unit: MB }
    return { value: Math.round((bps / KB) * 100) / 100, unit: KB }
  }

  function toBps(val: number, unit: number): number {
    return val > 0 ? Math.round(val * unit) : 0
  }

  // Load the daemon's current settings into the form each time the dialog opens.
  // The effect only tracks `open`; load()'s state writes happen after its first
  // await, so they don't feed back into the effect's dependencies.
  $effect(() => {
    if (open) void load()
  })

  async function load(): Promise<void> {
    loading = true
    error = ''
    try {
      const c = await Bridge.GetConfig()
      dir = c.download_dir
      segments = c.segments_per_download || 8
      priority = c.default_priority
      const m = splitRate(c.max_rate)
      maxRateVal = m.value
      maxRateUnit = m.unit
      const p = splitRate(c.per_download_max_rate)
      perDlVal = p.value
      perDlUnit = p.unit
    } catch (e) {
      error = e instanceof Error ? e.message : String(e)
    } finally {
      loading = false
    }
  }

  async function browse(): Promise<void> {
    const picked = await pickDirectory()
    if (picked) dir = picked
  }

  async function save(): Promise<void> {
    if (!canSave) return
    busy = true
    error = ''
    try {
      await Bridge.SetConfig(dir.trim(), segments, priority, toBps(maxRateVal, maxRateUnit), toBps(perDlVal, perDlUnit))
      toaster.success('Settings saved')
      onClose()
      await store.refresh()
    } catch (e) {
      error = e instanceof Error ? e.message : String(e)
    } finally {
      busy = false
    }
  }

  function close(): void {
    if (busy) return
    error = ''
    onClose()
  }

  // Window-level keyboard handling: Esc closes, Tab stays trapped in the card.
  function onWindowKeydown(e: KeyboardEvent): void {
    if (!open) return
    if (e.key === 'Escape') {
      e.preventDefault()
      close()
    } else if (e.key === 'Tab') {
      trapTab(e)
    }
  }

  function trapTab(e: KeyboardEvent): void {
    if (!cardEl) return
    const items = [...cardEl.querySelectorAll<HTMLElement>(FOCUSABLE)]
    if (items.length === 0) return
    const first = items[0]
    const last = items[items.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }
</script>

<svelte:window onkeydown={onWindowKeydown} />

{#if open}
  <button
    type="button"
    class="scrim"
    tabindex="-1"
    aria-label="Close"
    onclick={close}
    transition:fade={{ duration: motionMs }}
  ></button>

  <div class="wrap">
    <div
      class="card"
      role="dialog"
      aria-modal="true"
      aria-labelledby="settings-title"
      bind:this={cardEl}
      transition:scale={{ start: 0.96, opacity: 0, duration: motionMs }}
    >
      <header class="head">
        <h2 id="settings-title">Settings</h2>
        <button type="button" class="x" aria-label="Close" onclick={close}>
          <Icon name="close" size={16} />
        </button>
      </header>

      <div class="body">
        <div class="field">
          <span class="flabel">Default download folder</span>
          <div class="folder">
            <input class="input mono" type="text" value={dir} placeholder="Downloads folder" readonly />
            <button type="button" class="browse" onclick={browse}>
              <Icon name="folder" size={15} />
              Browse
            </button>
          </div>
        </div>

        <div class="row2">
          <div class="field">
            <span class="flabel">Default connections</span>
            <div class="stepper" role="group" aria-label="Default connections">
              <button
                type="button"
                aria-label="Fewer connections"
                disabled={segments <= 1}
                onclick={() => (segments = Math.max(1, segments - 1))}>−</button
              >
              <span class="stepper-val">{segments}</span>
              <button
                type="button"
                aria-label="More connections"
                disabled={segments >= MAX_SEGMENTS}
                onclick={() => (segments = Math.min(MAX_SEGMENTS, segments + 1))}>+</button
              >
            </div>
          </div>

          <div class="field">
            <span class="flabel">Default priority</span>
            <div class="segmented" role="group" aria-label="Default priority">
              {#each PRIORITIES as p (p.value)}
                <button
                  type="button"
                  class:selected={priority === p.value}
                  aria-pressed={priority === p.value}
                  onclick={() => (priority = p.value)}>{p.label}</button
                >
              {/each}
            </div>
          </div>
        </div>

        <div class="field">
          <span class="flabel">Global speed limit</span>
          <div class="ratefield">
            <input class="input num" type="number" min="0" step="0.1" bind:value={maxRateVal} aria-label="Global speed limit value" />
            <select class="unit" bind:value={maxRateUnit} aria-label="Global speed limit unit">
              <option value={KB}>KB/s</option>
              <option value={MB}>MB/s</option>
            </select>
            <span class="hint">{maxRateVal > 0 ? 'across all downloads' : 'Unlimited'}</span>
          </div>
        </div>

        <div class="field">
          <span class="flabel">Per-download limit (default)</span>
          <div class="ratefield">
            <input class="input num" type="number" min="0" step="0.1" bind:value={perDlVal} aria-label="Per-download limit value" />
            <select class="unit" bind:value={perDlUnit} aria-label="Per-download limit unit">
              <option value={KB}>KB/s</option>
              <option value={MB}>MB/s</option>
            </select>
            <span class="hint">{perDlVal > 0 ? 'each new download' : 'Unlimited'}</span>
          </div>
        </div>

        {#if error}
          <p class="error" role="alert">{error}</p>
        {/if}
      </div>

      <footer class="foot">
        <button type="button" class="ghost" onclick={close}>Cancel</button>
        <button type="button" class="primary" disabled={!canSave} onclick={save}>
          {busy ? 'Saving…' : 'Save Settings'}
        </button>
      </footer>
    </div>
  </div>
{/if}

<style>
  .scrim {
    position: fixed;
    inset: 0;
    z-index: 40;
    border: 0;
    margin: 0;
    padding: 0;
    background: var(--scrim);
    cursor: default;
    -webkit-app-region: no-drag;
  }

  .wrap {
    position: fixed;
    inset: 0;
    z-index: 41;
    display: grid;
    place-items: center;
    padding: var(--space-5);
    pointer-events: none;
  }

  .card {
    pointer-events: auto;
    width: min(520px, 100%);
    background: color-mix(in srgb, var(--surface-2) 92%, transparent);
    backdrop-filter: blur(20px);
    -webkit-backdrop-filter: blur(20px);
    border: 1px solid rgba(255, 255, 255, 0.08);
    border-radius: var(--radius-lg);
    box-shadow: 0 0 0 1px rgba(0, 0, 0, 0.5), 0 8px 32px rgba(0, 0, 0, 0.6), 0 32px 100px rgba(0, 0, 0, 0.85);
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }
  .card :global(button):focus-visible,
  .input:focus-visible,
  .unit:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 20px 24px 12px;
  }
  .head h2 {
    margin: 0;
    font-family: var(--font-serif);
    font-size: var(--text-lg);
    font-weight: 600;
    letter-spacing: -0.01em;
  }
  .x {
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--faint);
    cursor: pointer;
  }
  .x:hover {
    background: var(--surface-2);
    color: var(--text);
  }

  .body {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    padding: 0 24px 24px;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    min-width: 0;
  }
  .flabel {
    font-size: var(--text-xs);
    font-weight: 600;
    color: var(--muted);
  }

  .input {
    width: 100%;
    min-width: 0;
    height: 36px;
    border: 1px solid var(--border-strong);
    border-radius: 10px;
    background: var(--surface-2);
    color: var(--text);
    font: inherit;
    font-size: var(--text-md);
    padding: 0 var(--space-3);
  }
  .input::placeholder {
    color: var(--faint);
  }
  .input:focus {
    outline: 0;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-soft);
  }
  .input[readonly] {
    color: var(--muted);
    cursor: default;
  }
  .mono {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
  }
  .input.num {
    width: 96px;
    flex: none;
    font-variant-numeric: tabular-nums;
    border-radius: 8px;
  }

  .folder {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-width: 0;
  }
  .folder .input {
    flex: 1;
    text-overflow: ellipsis;
  }
  .browse {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    height: 36px;
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    background: var(--surface-2);
    color: var(--text);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 550;
    padding: 0 var(--space-3);
    cursor: pointer;
  }
  .browse:hover {
    border-color: var(--accent);
    color: var(--accent);
  }

  .row2 {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: var(--space-4);
    align-items: start;
  }
  @media (max-width: 540px) {
    .row2 {
      grid-template-columns: 1fr;
    }
  }

  .stepper {
    display: inline-flex;
    align-items: center;
    height: 36px;
    width: fit-content;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    background: var(--surface-2);
    overflow: hidden;
  }
  .stepper button {
    width: 36px;
    height: 100%;
    border: 0;
    background: transparent;
    color: var(--text);
    font-size: var(--text-lg);
    line-height: 1;
    cursor: pointer;
  }
  .stepper button:hover:not(:disabled) {
    background: var(--surface-2);
  }
  .stepper button:disabled {
    color: var(--faint);
    cursor: default;
  }
  .stepper-val {
    min-width: 44px;
    text-align: center;
    font-variant-numeric: tabular-nums;
    font-weight: 600;
    font-size: var(--text-sm);
    border-inline: 1px solid var(--border-strong);
    align-self: stretch;
    display: grid;
    place-items: center;
  }

  .segmented {
    display: flex;
    height: 36px;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    background: var(--surface-2);
    padding: 3px;
    gap: 2px;
  }
  .segmented button {
    flex: 1;
    border: 0;
    border-radius: 999px;
    background: transparent;
    color: var(--muted);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 550;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .segmented button:hover {
    color: var(--text);
  }
  .segmented button.selected {
    background: var(--surface-3);
    color: var(--text);
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.5);
  }

  .ratefield {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  .unit {
    flex: none;
    height: 36px;
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    background: var(--surface-2);
    color: var(--text);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 550;
    padding: 0 var(--space-2);
    cursor: pointer;
  }
  .unit:focus {
    outline: 0;
    border-color: var(--accent);
    box-shadow: 0 0 0 3px var(--accent-soft);
  }
  .hint {
    color: var(--faint);
    font-size: var(--text-sm);
  }

  .error {
    margin: 0;
    color: var(--danger);
    font-size: var(--text-sm);
  }

  .foot {
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: var(--space-2);
    padding: 12px 24px 20px;
  }
  .foot button {
    display: inline-flex;
    align-items: center;
    border: 0;
    font: inherit;
    font-weight: 600;
    font-size: var(--text-sm);
    cursor: pointer;
  }
  .ghost {
    background: transparent;
    color: var(--muted);
    border-radius: var(--radius-sm);
    padding: var(--space-2) var(--space-3);
  }
  .ghost:hover {
    background: var(--surface-2);
    color: var(--text);
  }
  .primary {
    background: var(--accent);
    color: var(--accent-contrast);
    border-radius: 999px;
    padding: 0 var(--space-4);
    height: 34px;
  }
  .primary:hover:not(:disabled) {
    background: var(--accent-hover);
  }
  .primary:disabled {
    opacity: 0.45;
    cursor: default;
  }
</style>
