<script lang="ts">
  import { onMount } from 'svelte'
  import { store } from './lib/store.svelte'
  import Toolbar from './components/Toolbar.svelte'
  import Sidebar from './components/Sidebar.svelte'
  import DownloadsTable from './components/DownloadsTable.svelte'
  import StatusBar from './components/StatusBar.svelte'
  import DaemonBanner from './components/DaemonBanner.svelte'
  import AddDialog from './components/AddDialog.svelte'

  let addOpen = $state(false)

  onMount(() => store.connect())
</script>

<div class="app">
  <Toolbar onAdd={() => (addOpen = true)} />
  <Sidebar />
  <main class="main">
    {#if !store.daemonUp}
      <DaemonBanner />
    {/if}
    <DownloadsTable />
  </main>
  <StatusBar />
</div>

<AddDialog open={addOpen} onClose={() => (addOpen = false)} />

<style>
  .app {
    display: grid;
    grid-template-columns: var(--sidebar-w) 1fr;
    grid-template-rows: var(--toolbar-h) 1fr var(--statusbar-h);
    grid-template-areas:
      'toolbar toolbar'
      'sidebar main'
      'statusbar statusbar';
    height: 100vh;
    overflow: hidden;
  }
  .main {
    grid-area: main;
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    background: var(--bg);
  }
</style>
