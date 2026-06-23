<script lang="ts">
  // Brand lockup: the gidm bolt mark + wordmark. The mark carries its own brand
  // colour (amber→pink); the rest of the UI keeps its monochrome accent.
  let { size = 18, wordmark = true }: { size?: number; wordmark?: boolean } = $props()
</script>

<span class="brand" style="--mark-h: {size}px">
  <svg class="mark" viewBox="334 142 356 748" aria-hidden="true" xmlns="http://www.w3.org/2000/svg">
    <defs>
      <linearGradient id="gidm-bolt" x1="512" y1="150" x2="512" y2="882" gradientUnits="userSpaceOnUse">
        <stop offset="0" stop-color="#fcd34d" />
        <stop offset="1" stop-color="#fb7185" />
      </linearGradient>
    </defs>
    <path
      d="M578 150 L342 590 L488 590 L412 882 L682 500 L530 500 Z"
      fill="url(#gidm-bolt)"
      stroke="url(#gidm-bolt)"
      stroke-width="14"
      stroke-linejoin="round"
    />
  </svg>
  {#if wordmark}<span class="word">gidm</span>{/if}
</span>

<style>
  .brand {
    display: inline-flex;
    align-items: center;
    gap: 9px;
    user-select: none;
  }
  .mark {
    height: var(--mark-h);
    width: auto;
    display: block;
    flex-shrink: 0;
  }
  .word {
    font-size: 16px;
    font-weight: 650;
    letter-spacing: -0.02em;
    color: var(--text);
  }
</style>
