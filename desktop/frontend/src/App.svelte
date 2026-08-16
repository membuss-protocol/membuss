<script>
  import { onMount } from 'svelte';
  import { app } from './lib/state.svelte';
  import Icon from './components/Icon.svelte';
  import Dashboard from './components/Dashboard.svelte';
  import Explorer from './components/Explorer.svelte';
  import Config from './components/Config.svelte';
  import Logs from './components/Logs.svelte';
  import SetupWizard from './components/SetupWizard.svelte';
  import DownloaderModal from './components/DownloaderModal.svelte';
  import UpdateModal from './components/UpdateModal.svelte';
  import * as wailsRuntime from '../wailsjs/runtime/runtime';

  onMount(async () => {
    await app.loadApp();
  });

  function openGatewayExternal() {
    const url = `http://${app.config?.gateway_addr || '127.0.0.1:8080'}/explorer/`;
    try {
      wailsRuntime.BrowserOpenURL(url);
    } catch (e) {
      window.open(url, '_blank');
    }
  }
</script>

<div class="flex w-screen h-screen premium-bg text-[#e9e2d2] overflow-hidden select-none font-sans">
  <!-- Left Engineering Rack Navigation Sidebar -->
  <aside class="w-64 min-w-[16rem] h-full bg-[#0c1416] border-r border-[rgba(233,226,210,0.08)] flex flex-col justify-between p-4 z-20 shadow-2xl">
    <!-- Top Brand & Navigation -->
    <div class="space-y-6">
      <!-- App Header Branding (matches explorer-web) -->
      <div class="flex flex-col gap-1 px-2 py-1">
        <div class="flex items-center justify-between">
          <span class="font-display text-lg leading-none text-[#e9e2d2]">MEMBUSS</span>
          <span class="text-[9px] font-mono font-bold px-1.5 py-0.5 rounded-[3px] bg-[rgba(233,226,210,0.06)] text-[#e8a33d] border border-[rgba(233,226,210,0.1)]">
            {app.config?.installed_version || 'v2.8.3'}
          </span>
        </div>
        <span class="text-[9px] text-[#8c887a] font-mono tracking-[0.22em] uppercase mt-0.5">decentralized content network</span>
      </div>

      <!-- Navigation Menu -->
      {#if (app.config?.setup_complete && app.installation?.valid) || app.nodeStatus?.process_running || app.nodeStatus?.api_online}
        <nav class="space-y-1">
          <button
            class="w-full flex items-center gap-3 px-3.5 py-2.5 rounded-[4px] text-xs font-medium transition-all duration-200 text-left cursor-pointer {app.activeTab === 'dashboard' ? 'bg-[rgba(233,226,210,0.06)] text-[#e8a33d] font-semibold border-l-2 border-[#e8a33d]' : 'text-[#8c887a] hover:text-[#e9e2d2] hover:bg-[rgba(233,226,210,0.03)]'}"
            onclick={() => app.activeTab = 'dashboard'}
          >
            <Icon name="dashboard" size={16} class={app.activeTab === 'dashboard' ? 'text-[#e8a33d]' : 'text-[#8c887a]'} />
            <span>Dashboard</span>
          </button>

          <button
            class="w-full flex items-center gap-3 px-3.5 py-2.5 rounded-[4px] text-xs font-medium transition-all duration-200 text-left cursor-pointer {app.activeTab === 'explorer' ? 'bg-[rgba(233,226,210,0.06)] text-[#e8a33d] font-semibold border-l-2 border-[#e8a33d]' : 'text-[#8c887a] hover:text-[#e9e2d2] hover:bg-[rgba(233,226,210,0.03)]'}"
            onclick={() => app.activeTab = 'explorer'}
          >
            <Icon name="explorer" size={16} class={app.activeTab === 'explorer' ? 'text-[#e8a33d]' : 'text-[#8c887a]'} />
            <span>Web Explorer</span>
          </button>

          <button
            class="w-full flex items-center gap-3 px-3.5 py-2.5 rounded-[4px] text-xs font-medium transition-all duration-200 text-left cursor-pointer {app.activeTab === 'config' ? 'bg-[rgba(233,226,210,0.06)] text-[#e8a33d] font-semibold border-l-2 border-[#e8a33d]' : 'text-[#8c887a] hover:text-[#e9e2d2] hover:bg-[rgba(233,226,210,0.03)]'}"
            onclick={() => app.activeTab = 'config'}
          >
            <Icon name="config" size={16} class={app.activeTab === 'config' ? 'text-[#e8a33d]' : 'text-[#8c887a]'} />
            <span>Configuration</span>
          </button>

          <button
            class="w-full flex items-center gap-3 px-3.5 py-2.5 rounded-[4px] text-xs font-medium transition-all duration-200 text-left cursor-pointer {app.activeTab === 'logs' ? 'bg-[rgba(233,226,210,0.06)] text-[#e8a33d] font-semibold border-l-2 border-[#e8a33d]' : 'text-[#8c887a] hover:text-[#e9e2d2] hover:bg-[rgba(233,226,210,0.03)]'}"
            onclick={() => app.activeTab = 'logs'}
          >
            <Icon name="terminal" size={16} class={app.activeTab === 'logs' ? 'text-[#e8a33d]' : 'text-[#8c887a]'} />
            <span>Daemon Logs</span>
          </button>
        </nav>
      {/if}
    </div>

    <!-- Sidebar Bottom: Rack Module Controller Widget -->
    <div class="double-bezel">
      <div class="double-bezel-inner p-3.5 space-y-3">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-2">
            <span class="relative flex h-2 w-2">
              <span class="animate-ping absolute inline-flex h-full w-full rounded-full {app.nodeStatus.process_running && app.nodeStatus.api_online ? 'bg-[#57b79e] opacity-60' : 'bg-transparent'}"></span>
              <span class="relative inline-flex rounded-full h-2 w-2 {app.nodeStatus.process_running && app.nodeStatus.api_online ? 'bg-[#57b79e]' : app.nodeStarting ? 'bg-[#e8a33d]' : 'bg-[#5a574f]'}"></span>
            </span>
            <span class="eyebrow !text-[9px] !text-[#e9e2d2]">
              {#if app.nodeStarting}
                Starting...
              {:else if app.nodeStopping}
                Stopping...
              {:else if app.nodeStatus.process_running && app.nodeStatus.api_online}
                Node Online
              {:else if app.nodeStatus.process_running}
                Binding API...
              {:else}
                Node Standby
              {/if}
            </span>
          </div>
          <span class="text-[9px] font-mono text-[#8c887a]">:{app.config?.api_addr?.split(':')[1] || '5001'}</span>
        </div>

        {#if !app.nodeStatus.process_running}
          <button
            class="w-full btn-ochre justify-center py-2 text-xs"
            onclick={() => app.startNodeAction()}
            disabled={app.nodeStarting}
          >
            <Icon name="power" size={14} />
            <span>{app.nodeStarting ? 'Launching...' : 'Start Node'}</span>
          </button>
        {:else}
          <div class="flex items-center gap-2">
            <button
              class="flex-1 btn-brick justify-center py-1.5 text-xs font-semibold"
              onclick={() => app.stopNodeAction()}
              disabled={app.nodeStopping}
            >
              <Icon name="power" size={13} />
              <span>{app.nodeStopping ? 'Stopping...' : 'Stop'}</span>
            </button>
            <button
              class="btn-rack py-1.5 px-2.5 text-xs"
              onclick={() => app.restartNodeAction()}
              title="Restart Daemon"
            >
              <Icon name="refresh" size={13} />
            </button>
          </div>
        {/if}
      </div>
    </div>
  </aside>

  <!-- Main Viewport Area -->
  <div class="flex-1 flex flex-col h-full overflow-hidden bg-[#0c1416]">
    <!-- Top Bar (matches explorer-web) -->
    <header class="h-16 min-h-[4rem] px-6 lg:px-10 border-b border-[rgba(233,226,210,0.08)] bg-[#0c1416]/80 backdrop-blur-xl flex items-center justify-between z-10">
      <div>
        <h1 class="text-sm font-bold text-[#e9e2d2] tracking-wide font-sans">
          {#if app.activeTab === 'dashboard'}
            Node Console & Status
          {:else if app.activeTab === 'explorer'}
            Web Explorer & Storage
          {:else if app.activeTab === 'config'}
            Rack Configuration
          {:else if app.activeTab === 'logs'}
            Daemon Terminal Stream
          {:else}
            Repository Setup
          {/if}
        </h1>
        <p class="eyebrow !text-[9px] mt-0.5">
          {#if app.activeTab === 'dashboard'}
            Live node telemetry, peer connectivity, and blockstore metrics
          {:else if app.activeTab === 'explorer'}
            Embedded gateway interface for browsing MIDs and DAG structures
          {:else if app.activeTab === 'config'}
            Manage ports, data directory, and network parameters
          {:else if app.activeTab === 'logs'}
            Live stdout/stderr stream from local node daemon
          {:else}
            First-time node configuration
          {/if}
        </p>
      </div>

      <!-- Action Toolbar -->
      <div class="flex items-center gap-2.5">
        <button
          class="btn-rack text-xs"
          onclick={() => app.showDownloaderModal = true}
        >
          <Icon name="download" size={14} class="text-[#e8a33d]" />
          <span>Download MID</span>
        </button>

        {#if app.explorerOnline}
          <button
            class="btn-rack text-xs"
            onclick={openGatewayExternal}
          >
            <Icon name="external" size={14} class="text-[#57b79e]" />
            <span>Open Gateway</span>
          </button>
        {/if}

        <button
          class="btn-rack text-xs"
          onclick={() => app.checkForUpdatesAction()}
          disabled={app.updateChecking}
        >
          <Icon name="refresh" size={13} class={app.updateChecking ? 'animate-spin text-[#e8a33d]' : 'text-[#8c887a]'} />
          <span class="hidden sm:inline">{app.updateChecking ? 'Checking...' : 'Check Updates'}</span>
        </button>
      </div>
    </header>

    <!-- Scrollable Main Content -->
    <main class="flex-1 overflow-y-auto p-6 lg:p-10 bg-[#0c1416] premium-bg">
      {#if app.loading}
        <div class="h-full flex flex-col items-center justify-center gap-3 text-[#8c887a] text-xs py-24">
          <Icon name="refresh" size={32} class="animate-spin text-[#e8a33d]" />
          <span class="eyebrow">Connecting to Membuss environment...</span>
        </div>
      {:else if (!app.config?.setup_complete || !app.installation?.valid) && !app.nodeStatus?.process_running && !app.nodeStatus?.api_online}
        <SetupWizard />
      {:else if app.activeTab === 'dashboard'}
        <Dashboard />
      {:else if app.activeTab === 'explorer'}
        <Explorer />
      {:else if app.activeTab === 'config'}
        <Config />
      {:else if app.activeTab === 'logs'}
        <Logs />
      {/if}
    </main>
  </div>

  <!-- Modals -->
  <DownloaderModal />
  <UpdateModal />

  <!-- Toast Notification Overlay -->
  <div class="fixed bottom-5 right-5 z-50 flex flex-col gap-2.5 pointer-events-none max-w-sm">
    {#each app.toasts as toast (toast.id)}
      <div
        class="pointer-events-auto shadow-2xl flex items-start gap-3 p-3.5 rounded-[4px] border text-xs font-medium backdrop-blur-xl animate-in fade-in slide-in-from-bottom-2 duration-200 {toast.type === 'error' ? 'bg-[#1a0e10]/95 text-[#ec8a76] border-[rgba(224,101,76,0.4)]' : toast.type === 'success' ? 'bg-[#0e1a17]/95 text-[#7fcdb6] border-[rgba(87,183,158,0.4)]' : toast.type === 'warning' ? 'bg-[#1c180e]/95 text-[#f4cd8a] border-[rgba(232,163,61,0.4)]' : 'bg-[#111d20]/95 text-[#e9e2d2] border-[rgba(233,226,210,0.14)]'}"
      >
        <Icon
          name={toast.type === 'error' ? 'warning' : toast.type === 'success' ? 'check' : toast.type === 'warning' ? 'warning' : 'info'}
          size={16}
          class="shrink-0 mt-0.5 {toast.type === 'error' ? 'text-[#e0654c]' : toast.type === 'success' ? 'text-[#57b79e]' : toast.type === 'warning' ? 'text-[#e8a33d]' : 'text-[#e8a33d]'}"
        />
        <span class="flex-1 leading-snug break-words">{toast.message}</span>
      </div>
    {/each}
  </div>
</div>
