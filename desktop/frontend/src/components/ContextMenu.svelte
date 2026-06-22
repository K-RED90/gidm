<script lang="ts">
  // The app's single floating context menu. Mounted once near the App root; it
  // renders whatever the `menu` singleton currently holds, anchored at the
  // pointer and clamped into the viewport. One level of submenu is supported
  // (enough for Set Priority).
  import Icon from './Icon.svelte'
  import { menu, type MenuItem } from '../lib/menu.svelte'

  let panelEl = $state<HTMLDivElement>()
  let pos = $state({ left: 0, top: 0 })
  let ready = $state(false)
  let openSub = $state<number | null>(null)

  // Clamp the panel into the viewport once it has a measured size. Reads menu.x/y
  // and the bound element (both reactive), so it re-runs when the menu re-opens at
  // a new spot. Measuring layout is the one legitimate effect here.
  $effect(() => {
    if (!menu.open || !panelEl) {
      ready = false
      openSub = null
      return
    }
    const pad = 8
    const r = panelEl.getBoundingClientRect()
    let left = menu.x
    let top = menu.y
    if (left + r.width > window.innerWidth - pad) left = Math.max(pad, menu.x - r.width)
    if (top + r.height > window.innerHeight - pad) top = Math.max(pad, window.innerHeight - pad - r.height)
    pos = { left, top }
    ready = true
  })

  function activate(item: Extract<MenuItem, { kind: 'item' }>): void {
    if (item.disabled) return
    item.run()
    menu.hide()
  }

  function focusFirst(node: HTMLElement): void {
    requestAnimationFrame(() => node.querySelector<HTMLElement>('.cm-item:not([disabled])')?.focus())
  }

  function itemEls(): HTMLElement[] {
    return panelEl ? [...panelEl.querySelectorAll<HTMLElement>('.cm-item:not([disabled])')] : []
  }

  function move(delta: number): void {
    const els = itemEls()
    if (els.length === 0) return
    const i = els.indexOf(document.activeElement as HTMLElement)
    const next = i === -1 ? (delta > 0 ? 0 : els.length - 1) : (i + delta + els.length) % els.length
    els[next].focus()
  }

  function onKeydown(e: KeyboardEvent): void {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        move(1)
        break
      case 'ArrowUp':
        e.preventDefault()
        move(-1)
        break
      case 'Home':
        e.preventDefault()
        itemEls()[0]?.focus()
        break
      case 'End': {
        e.preventDefault()
        const els = itemEls()
        els[els.length - 1]?.focus()
        break
      }
      case 'Escape':
        e.preventDefault()
        if (openSub !== null) openSub = null
        else menu.hide()
        break
      case 'ArrowLeft':
        if (openSub !== null) {
          e.preventDefault()
          openSub = null
        }
        break
    }
  }
</script>

{#if menu.open}
  <!-- Transparent full-screen catcher: any click/right-click/wheel outside the
       panel dismisses the menu. -->
  <button
    class="cm-scrim"
    tabindex="-1"
    aria-label="Close menu"
    onpointerdown={() => menu.hide()}
    oncontextmenu={(e) => {
      e.preventDefault()
      menu.hide()
    }}
    onwheel={() => menu.hide()}
  ></button>

  <div
    class="cm"
    class:ready
    bind:this={panelEl}
    role="menu"
    tabindex="-1"
    style:left="{pos.left}px"
    style:top="{pos.top}px"
    onkeydown={onKeydown}
    {@attach focusFirst}
  >
    {#each menu.items as item, i (i)}
      {#if item.kind === 'sep'}
        <div class="cm-sep" role="separator"></div>
      {:else if item.kind === 'submenu'}
        <div
          class="cm-subwrap"
          role="none"
          onmouseenter={() => (openSub = item.disabled ? null : i)}
          onmouseleave={() => (openSub = null)}
        >
          <button
            class="cm-item"
            role="menuitem"
            aria-haspopup="menu"
            aria-expanded={openSub === i}
            disabled={item.disabled}
            onclick={() => (openSub = openSub === i ? null : i)}
            onkeydown={(e) => {
              if (e.key === 'ArrowRight' || e.key === 'Enter') {
                e.preventDefault()
                openSub = i
              }
            }}
          >
            {#if item.icon}<Icon name={item.icon} size={15} />{/if}
            <span class="cm-label">{item.label}</span>
            <Icon name="chevronRight" size={14} />
          </button>
          {#if openSub === i}
            <div class="cm cm-flyout ready" role="menu">
              {#each item.items as sub, j (j)}
                {#if sub.kind === 'item'}
                  <button
                    class="cm-item"
                    class:danger={sub.danger}
                    role={sub.checked !== undefined ? 'menuitemradio' : 'menuitem'}
                    aria-checked={sub.checked !== undefined ? sub.checked : undefined}
                    disabled={sub.disabled}
                    onclick={() => activate(sub)}
                  >
                    {#if sub.icon}<Icon name={sub.icon} size={15} />{/if}
                    <span class="cm-label">{sub.label}</span>
                    {#if sub.checked}<Icon name="check" size={14} />{/if}
                  </button>
                {/if}
              {/each}
            </div>
          {/if}
        </div>
      {:else}
        <button
          class="cm-item"
          class:danger={item.danger}
          role={item.checked !== undefined ? 'menuitemradio' : 'menuitem'}
          aria-checked={item.checked !== undefined ? item.checked : undefined}
          disabled={item.disabled}
          onclick={() => activate(item)}
        >
          {#if item.icon}<Icon name={item.icon} size={15} />{/if}
          <span class="cm-label">{item.label}</span>
          {#if item.checked}<Icon name="check" size={14} />{/if}
        </button>
      {/if}
    {/each}
  </div>
{/if}

<style>
  .cm-scrim {
    position: fixed;
    inset: 0;
    z-index: 60;
    border: 0;
    margin: 0;
    padding: 0;
    background: transparent;
    cursor: default;
  }
  .cm {
    position: fixed;
    z-index: 61;
    min-width: 196px;
    padding: var(--space-1);
    background: var(--surface);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-md);
    box-shadow: var(--shadow-overlay);
    /* hidden until measured/clamped to avoid a one-frame flash at the wrong spot */
    opacity: 0;
  }
  .cm.ready {
    opacity: 1;
  }
  .cm-subwrap {
    position: relative;
  }
  .cm-flyout {
    position: absolute;
    top: -1px;
    left: 100%;
    margin-left: 2px;
    min-width: 150px;
  }
  .cm-item {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    width: 100%;
    padding: var(--space-2) var(--space-2);
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 500;
    text-align: left;
    cursor: pointer;
  }
  .cm-item :global(svg) {
    color: var(--muted);
    flex: none;
  }
  .cm-label {
    flex: 1;
    white-space: nowrap;
  }
  .cm-item:hover:not(:disabled),
  .cm-item:focus-visible {
    background: var(--surface-2);
    outline: none;
  }
  .cm-item:hover:not(:disabled) :global(svg),
  .cm-item:focus-visible :global(svg) {
    color: var(--text);
  }
  .cm-item.danger {
    color: var(--danger);
  }
  .cm-item.danger:hover:not(:disabled),
  .cm-item.danger:focus-visible {
    background: color-mix(in srgb, var(--danger) 16%, transparent);
  }
  .cm-item.danger :global(svg) {
    color: var(--danger);
  }
  .cm-item:disabled {
    color: var(--faint);
    cursor: default;
  }
  .cm-item:disabled :global(svg) {
    color: var(--faint);
  }
  .cm-sep {
    height: 1px;
    margin: var(--space-1) var(--space-2);
    background: var(--border);
  }
</style>
