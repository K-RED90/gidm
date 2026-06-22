<script lang="ts">
  import { onMount } from 'svelte'
  import { store } from './lib/store.svelte'
  import Toolbar from './components/Toolbar.svelte'
  import Sidebar from './components/Sidebar.svelte'
  import DownloadsTable from './components/DownloadsTable.svelte'
  import StatusBar from './components/StatusBar.svelte'
  import DaemonBanner from './components/DaemonBanner.svelte'
  import AddDialog from './components/AddDialog.svelte'
  import DetailsDialog from './components/DetailsDialog.svelte'
  import SettingsDialog from './components/SettingsDialog.svelte'
  import ContextMenu from './components/ContextMenu.svelte'
  import Toaster from './components/Toaster.svelte'

  let addOpen = $state(false)
  let detailsId = $state<string | null>(null)
  let settingsOpen = $state(false)

  // modalOpen tells the table's keyboard handler to stand down while a dialog is
  // up; set it alongside each open/close so it stays correct without an effect.
  function openAdd(): void {
    addOpen = true
    store.modalOpen = true
  }
  function closeAdd(): void {
    addOpen = false
    store.modalOpen = detailsId !== null || settingsOpen
  }
  function openDetails(id: string): void {
    detailsId = id
    store.modalOpen = true
  }
  function closeDetails(): void {
    detailsId = null
    store.modalOpen = addOpen || settingsOpen
  }
  function openSettings(): void {
    settingsOpen = true
    store.modalOpen = true
  }
  function closeSettings(): void {
    settingsOpen = false
    store.modalOpen = addOpen || detailsId !== null
  }

  onMount(() => store.connect())
</script>

<div class="app">
  <Toolbar onAdd={openAdd} onSettings={openSettings} />
  <Sidebar />
  <main class="main">
    {#if !store.daemonUp}
      <DaemonBanner />
    {/if}
    <DownloadsTable onDetails={openDetails} />
  </main>
  <StatusBar />
</div>

<AddDialog open={addOpen} onClose={closeAdd} />
<DetailsDialog id={detailsId} onClose={closeDetails} />
<SettingsDialog open={settingsOpen} onClose={closeSettings} />
<ContextMenu />
<Toaster />

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
