<script lang="ts">
  import { onMount } from 'svelte'
  import { store } from './lib/store.svelte'
  import Sidebar from './components/Sidebar.svelte'
  import AddBar from './components/AddBar.svelte'
  import DownloadsTable from './components/DownloadsTable.svelte'
  import StatusBar from './components/StatusBar.svelte'
  import DaemonBanner from './components/DaemonBanner.svelte'

  onMount(() => store.connect())
</script>

<div class="app">
  <Sidebar />
  <main class="main">
    <AddBar />
    {#if !store.daemonUp}
      <DaemonBanner />
    {/if}
    <DownloadsTable />
    <StatusBar />
  </main>
</div>

<style>
  .app {
    display: grid;
    grid-template-columns: 220px 1fr;
    height: 100vh;
    overflow: hidden;
  }
  .main {
    display: flex;
    flex-direction: column;
    min-width: 0;
    background: var(--bg);
  }
</style>
