<script lang="ts">
  import { fade, scale } from 'svelte/transition'
  import Icon from './Icon.svelte'
  import { Bridge, Priority, pickDirectory, type Credentials } from '../lib/bridge'
  import { store } from '../lib/store.svelte'
  import { buildCredentials, parseHeaderLines } from '../lib/auth'

  let { open = false, onClose }: { open?: boolean; onClose: () => void } = $props()

  const MAX_SEGMENTS = 16
  const FOCUSABLE =
    'a[href],button:not([disabled]),input:not([disabled]),[tabindex]:not([tabindex="-1"])'
  const PRIORITIES: { value: Priority; label: string }[] = [
    { value: Priority.PriorityLow, label: 'Low' },
    { value: Priority.PriorityNormal, label: 'Normal' },
    { value: Priority.PriorityHigh, label: 'High' },
  ]

  let url = $state('')
  let dir = $state('')
  let filename = $state('')
  let segments = $state(0) // 0 = Auto (the daemon's configured default)
  let priority = $state<Priority>(Priority.PriorityNormal)
  let error = $state('')
  let busy = $state(false)
  let cardEl = $state<HTMLElement>()

  // Optional request auth, collapsed by default so the common add stays clean.
  let showAuth = $state(false)
  let username = $state('')
  let password = $state('')
  let referer = $state('')
  let cookie = $state('')
  let headersText = $state('')

  // hasAuth drives the disclosure's "set" hint without re-parsing on every render.
  const hasAuth = $derived(
    !!(username.trim() || password || referer.trim() || cookie.trim() || headersText.trim()),
  )
  // Warn when Basic auth would be sent over plain http (credentials in the clear).
  const insecureAuth = $derived(url.trim().toLowerCase().startsWith('http://') && !!(username || password))

  // suggestedName mirrors what the daemon derives when Name is left blank, shown as
  // the placeholder so the user sees the resulting filename without us syncing state.
  const suggestedName = $derived.by(() => {
    try {
      return new URL(url).pathname.split('/').filter(Boolean).pop() || 'download'
    } catch {
      return 'download'
    }
  })

  const canAdd = $derived(store.daemonUp && !busy && url.trim().length > 0)
  const reduceMotion =
    typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  const motionMs = reduceMotion ? 0 : 150

  function reset(): void {
    url = ''
    dir = ''
    filename = ''
    segments = 0
    priority = Priority.PriorityNormal
    error = ''
    showAuth = false
    username = ''
    password = ''
    referer = ''
    cookie = ''
    headersText = ''
  }

  function auth(): Credentials {
    return buildCredentials({
      username,
      password,
      referer,
      cookie,
      headers: parseHeaderLines(headersText),
    })
  }

  function close(): void {
    if (busy) return
    reset()
    onClose()
  }

  async function browse(): Promise<void> {
    const picked = await pickDirectory()
    if (picked) dir = picked
  }

  async function submit(): Promise<void> {
    const u = url.trim()
    if (!u || busy) return
    busy = true
    error = ''
    try {
      await Bridge.Add(u, dir, filename.trim(), segments, priority, auth())
      reset()
      onClose()
      await store.refresh()
    } catch (e) {
      error = e instanceof Error ? e.message : String(e)
    } finally {
      busy = false
    }
  }

  // Keyboard handling lives on window so it is exempt from element a11y rules and
  // works regardless of which field holds focus: Esc closes, Tab stays trapped.
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

  function focusOnOpen(node: HTMLInputElement): void {
    node.focus()
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
      aria-labelledby="add-title"
      bind:this={cardEl}
      transition:scale={{ start: 0.96, opacity: 0, duration: motionMs }}
    >
      <header class="head">
        <h2 id="add-title">Add Download</h2>
        <button type="button" class="x" aria-label="Close" onclick={close}>
          <Icon name="close" size={16} />
        </button>
      </header>

      <div class="body">
        <label class="field">
          <span class="flabel">URL</span>
          <input
            name="url"
            type="text"
            inputmode="url"
            class="input"
            class:invalid={error}
            placeholder="https://…"
            bind:value={url}
            onkeydown={(e) => e.key === 'Enter' && submit()}
            spellcheck="false"
            autocomplete="off"
            {@attach focusOnOpen}
          />
        </label>

        <div class="field">
          <span class="flabel">Save to</span>
          <div class="folder">
            <input
              name="folder"
              class="input mono"
              type="text"
              value={dir}
              placeholder="Downloads folder (default)"
              readonly
            />
            {#if dir}
              <button type="button" class="link" onclick={() => (dir = '')}>Reset</button>
            {/if}
            <button type="button" class="browse" onclick={browse}>
              <Icon name="folder" size={15} />
              Browse
            </button>
          </div>
        </div>

        <label class="field">
          <span class="flabel">Name</span>
          <input
            name="filename"
            type="text"
            class="input"
            placeholder={suggestedName}
            bind:value={filename}
            onkeydown={(e) => e.key === 'Enter' && submit()}
            spellcheck="false"
            autocomplete="off"
          />
        </label>

        <div class="row2">
          <div class="field">
            <span class="flabel">Connections</span>
            <div class="stepper" role="group" aria-label="Connections">
              <button
                type="button"
                aria-label="Fewer connections"
                disabled={segments <= 0}
                onclick={() => (segments = Math.max(0, segments - 1))}>−</button
              >
              <span class="stepper-val" class:auto={segments === 0}>{segments === 0 ? 'Auto' : segments}</span>
              <button
                type="button"
                aria-label="More connections"
                disabled={segments >= MAX_SEGMENTS}
                onclick={() => (segments = Math.min(MAX_SEGMENTS, segments + 1))}>+</button
              >
            </div>
          </div>

          <div class="field">
            <span class="flabel">Priority</span>
            <div class="segmented" role="group" aria-label="Priority">
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

        <div class="auth">
          <button
            type="button"
            class="disclosure"
            aria-expanded={showAuth}
            onclick={() => (showAuth = !showAuth)}
          >
            <Icon name={showAuth ? 'chevronDown' : 'chevronRight'} size={14} />
            <span>Authentication &amp; headers</span>
            {#if hasAuth}<span class="badge">set</span>{:else}<span class="opt">optional</span>{/if}
          </button>

          {#if showAuth}
            <div class="auth-body">
              <div class="row2">
                <label class="field">
                  <span class="flabel">Login</span>
                  <input class="input" type="text" bind:value={username} autocomplete="off" spellcheck="false" />
                </label>
                <label class="field">
                  <span class="flabel">Password</span>
                  <input class="input" type="password" bind:value={password} autocomplete="off" />
                </label>
              </div>
              <label class="field">
                <span class="flabel">Referer</span>
                <input class="input" type="text" bind:value={referer} placeholder="https://…" autocomplete="off" spellcheck="false" />
              </label>
              <label class="field">
                <span class="flabel">Cookie</span>
                <input class="input mono" type="text" bind:value={cookie} placeholder="name=value; name2=value2" autocomplete="off" spellcheck="false" />
              </label>
              <label class="field">
                <span class="flabel">Headers</span>
                <textarea
                  class="input mono area"
                  bind:value={headersText}
                  rows="2"
                  placeholder={'X-Header: value\nOne per line'}
                  spellcheck="false"
                ></textarea>
              </label>
              {#if insecureAuth}
                <p class="warn" role="status">
                  <Icon name="alert" size={13} /> This URL is plain http — the password will be sent unencrypted.
                </p>
              {/if}
            </div>
          {/if}
        </div>

        {#if error}
          <p class="error" role="alert">{error}</p>
        {/if}
      </div>

      <footer class="foot">
        <button type="button" class="ghost" onclick={close}>Cancel</button>
        <button type="button" class="primary" disabled={!canAdd} onclick={submit}>
          {busy ? 'Adding…' : 'Add Download'}
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

  /* Full-screen centering layer; pointer-events pass through to the scrim so a
     click outside the card still closes it. */
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
    background: var(--surface-2);
    border: 1px solid rgba(255, 255, 255, 0.07);
    border-radius: var(--radius-lg);
    box-shadow: 0 0 0 1px rgba(0, 0, 0, 0.4), 0 8px 32px rgba(0, 0, 0, 0.6), 0 32px 100px rgba(0, 0, 0, 0.8);
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }
  .card :global(button):focus-visible,
  .input:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--space-4) var(--space-5);
  }
  .head h2 {
    margin: 0;
    font-family: var(--font-serif);
    font-size: var(--text-xl);
    font-weight: 600;
    letter-spacing: -0.01em;
  }
  .x {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
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

  .body {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    padding: 0 var(--space-5) var(--space-5);
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
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--muted);
  }

  .input {
    width: 100%;
    min-width: 0;
    height: 38px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
    background: var(--surface-3);
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
  .input.invalid {
    border-color: var(--danger);
  }
  .input[readonly] {
    color: var(--muted);
    cursor: default;
  }
  .mono {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
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
  .browse,
  .link {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    height: 38px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-sm);
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
  .link {
    border: 0;
    background: transparent;
    color: var(--muted);
    padding: 0 var(--space-1);
  }
  .link:hover {
    color: var(--text);
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
    height: 38px;
    width: fit-content;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-3);
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
    min-width: 54px;
    text-align: center;
    font-variant-numeric: tabular-nums;
    font-weight: 600;
    font-size: var(--text-sm);
    border-inline: 1px solid var(--border);
    align-self: stretch;
    display: grid;
    place-items: center;
  }
  .stepper-val.auto {
    color: var(--muted);
    font-weight: 550;
  }

  .segmented {
    display: flex;
    height: 38px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-3);
    padding: 3px;
    gap: 3px;
  }
  .segmented button {
    flex: 1;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 550;
    cursor: pointer;
  }
  .segmented button:hover {
    color: var(--text);
  }
  .segmented button.selected {
    background: var(--surface-3);
    color: var(--accent);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.4);
  }

  .error {
    margin: 0;
    color: var(--danger);
    font-size: var(--text-sm);
  }

  /* Auth disclosure: a quiet, full-width toggle so the common add stays clean. */
  .auth {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }
  .disclosure {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    align-self: flex-start;
    border: 0;
    background: transparent;
    padding: 2px 0;
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--muted);
    cursor: pointer;
  }
  .disclosure:hover {
    color: var(--text);
  }
  .disclosure :global(svg) {
    color: var(--faint);
  }
  .disclosure .opt {
    font-weight: 500;
    color: var(--faint);
  }
  .disclosure .badge {
    font-size: var(--text-xs);
    font-weight: 600;
    color: var(--accent);
    background: var(--accent-soft);
    border-radius: 999px;
    padding: 1px var(--space-2);
  }
  .auth-body {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }
  .area {
    height: auto;
    min-height: 56px;
    padding: var(--space-2) var(--space-3);
    resize: vertical;
    line-height: 1.5;
  }
  .warn {
    display: inline-flex;
    align-items: center;
    gap: var(--space-1);
    margin: 0;
    color: var(--warning);
    font-size: var(--text-xs);
  }
  .warn :global(svg) {
    color: var(--warning);
  }

  .foot {
    display: flex;
    justify-content: flex-end;
    gap: var(--space-2);
    padding: var(--space-4) var(--space-5);
    border-top: 1px solid var(--border);
    background: var(--surface-3);
  }
  .foot button {
    border: 1px solid transparent;
    border-radius: var(--radius-sm);
    font: inherit;
    font-weight: 600;
    font-size: var(--text-sm);
    padding: var(--space-2) var(--space-4);
    cursor: pointer;
  }
  .ghost {
    background: transparent;
    border-color: var(--border-strong);
    color: var(--text);
  }
  .ghost:hover {
    background: var(--surface-2);
  }
  .primary {
    background: var(--accent);
    color: var(--accent-contrast);
  }
  .primary:hover:not(:disabled) {
    background: var(--accent-hover);
  }
  .primary:disabled {
    opacity: 0.45;
    cursor: default;
  }
</style>
