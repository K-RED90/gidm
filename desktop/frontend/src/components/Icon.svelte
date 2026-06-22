<script lang="ts">
  import { icons, type IconName } from '../lib/icons'

  // size in px (square); stroke defaults thin for a refined look at small sizes.
  // The icon inherits `currentColor`, so callers control colour via CSS `color`.
  let {
    name,
    size = 16,
    stroke = 1.75,
  }: { name: IconName; size?: number; stroke?: number } = $props()
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width={stroke}
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
>
  <!-- eslint-disable-next-line svelte/no-at-html-tags — trusted constant icon paths -->
  {@html icons[name]}
</svg>

<style>
  svg {
    display: block;
    flex: none;
  }
</style>
