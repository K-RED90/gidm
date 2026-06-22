<script lang="ts">
  import { fly } from 'svelte/transition'
  import Icon from './Icon.svelte'
  import type { IconName } from '../lib/icons'
  import { toaster, type ToastKind } from '../lib/toast.svelte'

  const ICON: Record<ToastKind, IconName> = { info: 'info', success: 'check', error: 'alert' }
  const reduceMotion =
    typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  const flyIn = { y: 12, duration: reduceMotion ? 0 : 180 }
</script>

<div class="toaster" role="status" aria-live="polite">
  {#each toaster.toasts as t (t.id)}
    <div class="toast {t.kind}" transition:fly={flyIn}>
      <Icon name={ICON[t.kind]} size={15} />
      <span class="msg">{t.message}</span>
      <button class="x" aria-label="Dismiss" onclick={() => toaster.dismiss(t.id)}>
        <Icon name="close" size={13} />
      </button>
    </div>
  {/each}
</div>

<style>
  .toaster {
    position: fixed;
    bottom: calc(var(--statusbar-h) + var(--space-3));
    left: 50%;
    transform: translateX(-50%);
    z-index: 70;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    align-items: center;
    pointer-events: none;
  }
  .toast {
    pointer-events: auto;
    display: flex;
    align-items: center;
    gap: var(--space-2);
    max-width: min(440px, 90vw);
    padding: var(--space-2) var(--space-2) var(--space-2) var(--space-3);
    background: var(--surface);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-overlay);
    font-size: var(--text-sm);
    color: var(--text);
  }
  .toast :global(svg) {
    flex: none;
    color: var(--muted);
  }
  .toast.success :global(svg) {
    color: var(--success);
  }
  .toast.error {
    border-color: color-mix(in srgb, var(--danger) 55%, var(--border-strong));
  }
  .toast.error :global(svg) {
    color: var(--danger);
  }
  .msg {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .x {
    display: grid;
    place-items: center;
    width: 22px;
    height: 22px;
    flex: none;
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--muted);
    cursor: pointer;
  }
  .x:hover {
    background: var(--surface-2);
    color: var(--text);
  }
</style>
