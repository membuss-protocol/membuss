<script>
  import { onMount, onDestroy } from 'svelte';
  import { app } from '../lib/state.svelte';
  import Icon from './Icon.svelte';
  import { DownloadContent, SelectDirectory, OpenDataDir } from '../../wailsjs/go/main/App';
  import * as wailsRuntime from '../../wailsjs/runtime/runtime';

  let copiedPeerId = $state(false);
  let quickMid = $state('');
  let quickResolving = $state(false);
  let showAddrs = $state(false);

  // Live Bandwidth Simulation / Stream
  let bandwidthIn = $state([]);
  let bandwidthOut = $state([]);
  let currentInSpeed = $state(0);
  let currentOutSpeed = $state(0);
  const chartWidth = 900;
  const chartHeight = 160;
  let chartInterval = null;

  onMount(() => {
    // Generate initial wave
    bandwidthIn = Array(40).fill(0).map(() => 40 * 1024 + Math.random() * 80 * 1024);
    bandwidthOut = Array(40).fill(0).map(() => 15 * 1024 + Math.random() * 30 * 1024);
    currentInSpeed = bandwidthIn[bandwidthIn.length - 1];
    currentOutSpeed = bandwidthOut[bandwidthOut.length - 1];

    chartInterval = setInterval(() => {
      if (document.hidden) return;
      const isOnline = app.nodeStatus.process_running && app.nodeStatus.api_online;
      const nextIn = isOnline ? (20 * 1024 + Math.random() * 120 * 1024) : 0;
      const nextOut = isOnline ? (10 * 1024 + Math.random() * 45 * 1024) : 0;

      bandwidthIn = [...bandwidthIn.slice(1), nextIn];
      bandwidthOut = [...bandwidthOut.slice(1), nextOut];
      currentInSpeed = nextIn;
      currentOutSpeed = nextOut;
    }, 1200);
  });

  onDestroy(() => {
    if (chartInterval) clearInterval(chartInterval);
  });

  function getSvgPath(speeds) {
    if (!speeds || speeds.length === 0) return '';
    const maxSpeed = Math.max(...bandwidthIn, ...bandwidthOut, 100 * 1024);
    const maxVal = maxSpeed * 1.25;
    const padding = 8;
    const points = speeds.map((speed, i) => {
      const x = (i / 40) * (chartWidth - padding * 2) + padding;
      const y = chartHeight - ((speed / maxVal) * (chartHeight - padding * 2)) - padding;
      return `${x.toFixed(1)},${Math.max(padding, Math.min(chartHeight - padding, y)).toFixed(1)}`;
    });
    return `M ${points.join(' L ')}`;
  }

  function getAreaPath(speeds) {
    if (!speeds || speeds.length === 0) return '';
    const maxSpeed = Math.max(...bandwidthIn, ...bandwidthOut, 100 * 1024);
    const maxVal = maxSpeed * 1.25;
    const padding = 8;
    const points = speeds.map((speed, i) => {
      const x = (i / 40) * (chartWidth - padding * 2) + padding;
      const y = chartHeight - ((speed / maxVal) * (chartHeight - padding * 2)) - padding;
      return `${x.toFixed(1)},${Math.max(padding, Math.min(chartHeight - padding, y)).toFixed(1)}`;
    });
    const firstX = padding;
    const lastX = ((speeds.length - 1) / 40) * (chartWidth - padding * 2) + padding;
    return `M ${firstX},${chartHeight - padding} L ${points.join(' L ')} L ${lastX},${chartHeight - padding} Z`;
  }

  function copyPeerId() {
    if (!app.nodeStatus.info?.peer_id) return;
    navigator.clipboard.writeText(app.nodeStatus.info.peer_id);
    copiedPeerId = true;
    app.addToast('info', 'Peer ID copied to clipboard');
    setTimeout(() => {
      copiedPeerId = false;
    }, 2000);
  }

  function copyText(txt, label) {
    if (!txt) return;
    navigator.clipboard.writeText(txt);
    app.addToast('info', `${label} copied to clipboard`);
  }

  function formatBytes(bytes) {
    if (!bytes || bytes <= 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (bytes / Math.pow(1024, i)).toFixed(2) + ' ' + units[i];
  }

  function formatUptime(seconds) {
    if (!seconds || seconds <= 0) return '0s';
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = seconds % 60;
    if (h > 0) return `${h}h ${m}m ${s}s`;
    if (m > 0) return `${m}m ${s}s`;
    return `${s}s`;
  }

  function openWebExplorerFullControl() {
    if (!app.nodeStatus.process_running) {
      app.startNodeAction();
    }
    app.activeTab = 'explorer';
  }

  async function handleQuickResolve() {
    if (!quickMid.trim()) {
      app.addToast('warning', 'Please enter a valid MID or MemNS identifier');
      return;
    }
    quickResolving = true;
    try {
      const dest = app.config?.data_dir || '';
      const saved = await DownloadContent(dest, quickMid.trim());
      app.addToast('success', `Resolved and saved: ${saved}`);
      quickMid = '';
    } catch (e) {
      app.addToast('error', `Download failed: ${e.message || e}`);
    } finally {
      quickResolving = false;
    }
  }

  function openGatewayExternal() {
    const url = `http://${app.config?.gateway_addr || '127.0.0.1:8080'}/explorer/`;
    try {
      wailsRuntime.BrowserOpenURL(url);
    } catch (e) {
      window.open(url, '_blank');
    }
  }

  async function openDataDirFolder() {
    try {
      await OpenDataDir();
      app.addToast('info', 'Opened repository directory');
    } catch (e) {
      app.addToast('error', `Could not open folder: ${e}`);
    }
  }
</script>

<div class="space-y-6 w-full max-w-[1700px] mx-auto font-sans">
  {#if !app.nodeStatus.process_running && !app.nodeStarting}
    <!-- ======================================================== -->
    <!-- STATE 1: NODE STANDBY / OFFLINE (PURPOSEFUL LAUNCHPAD) -->
    <!-- ======================================================== -->
    <div class="space-y-6 animate-fade-in-up">
      <!-- Big Standby Launch Card -->
      <section class="double-bezel">
        <div class="double-bezel-inner flex flex-col lg:flex-row lg:items-center justify-between gap-8 !p-8 lg:!p-10 relative overflow-hidden">
          <span class="absolute right-0 top-0 h-full w-2 bg-gradient-to-b from-[#e8a33d] to-transparent pointer-events-none"></span>

          <div class="space-y-3 max-w-2xl">
            <div class="flex items-center gap-3">
              <span class="relative flex h-3 w-3">
                <span class="relative inline-flex rounded-full h-3 w-3 bg-[#5a574f]"></span>
              </span>
              <h2 class="font-display text-2xl lg:text-3xl tracking-wide text-[#e9e2d2]">
                Node Daemon is in Standby
              </h2>
              <span class="eyebrow !text-[9px] px-2 py-0.5 rounded-[3px] bg-[rgba(233,226,210,0.04)] border border-[rgba(233,226,210,0.08)]">
                {app.config?.installed_version || 'v2.8.6'}
              </span>
            </div>

            <p class="text-xs text-[#8c887a] leading-relaxed">
              Launch the local Membuss daemon to join the decentralized content routing swarm, bind the local HTTP Gateway & Control APIs, and enable Merkle DAG block exchange.
            </p>

            <div class="pt-2 flex flex-wrap items-center gap-4 text-xs font-mono text-[#8c887a]">
              <div class="flex items-center gap-1.5">
                <span class="text-[#e8a33d]">●</span>
                <span>Gateway: <strong class="text-[#e9e2d2]">:{app.config?.gateway_addr?.split(':')[1] || '8083'}</strong></span>
              </div>
              <div class="flex items-center gap-1.5">
                <span class="text-[#57b79e]">●</span>
                <span>API: <strong class="text-[#e9e2d2]">:{app.config?.api_addr?.split(':')[1] || '5004'}</strong></span>
              </div>
              <div class="flex items-center gap-1.5">
                <span class="text-[#8c887a]">●</span>
                <span>gRPC: <strong class="text-[#e9e2d2]">:{app.config?.grpc_addr?.split(':')[1] || '50055'}</strong></span>
              </div>
            </div>
          </div>

          <!-- Primary Start CTAs -->
          <div class="flex flex-col sm:flex-row items-stretch sm:items-center gap-3 shrink-0">
            <!-- FULL CONTROL CTA -->
            <button
              class="btn-ochre text-xs font-bold px-6 py-3.5 flex items-center justify-center gap-2 shadow-lg shadow-[rgba(232,163,61,0.2)] hover:scale-[1.02] transition-transform cursor-pointer"
              onclick={openWebExplorerFullControl}
              title="Launch daemon and open Web Explorer for complete control plane"
            >
              <Icon name="explorer" size={17} />
              <span>Open Web Explorer for Full Control ↗</span>
            </button>

            <button
              class="btn-rack text-xs font-semibold px-5 py-3.5 flex items-center justify-center gap-2"
              onclick={() => app.startNodeAction()}
            >
              <Icon name="power" size={15} />
              <span>Start Daemon Only</span>
            </button>

            <button
              class="btn-rack text-xs px-4 py-3.5 justify-center"
              onclick={() => app.activeTab = 'config'}
              title="Edit Port & Storage Configuration"
            >
              <Icon name="config" size={15} />
              <span>Config</span>
            </button>
          </div>
        </div>
      </section>

      <!-- Pre-Flight Configuration & Storage Overview (3 Cards) -->
      <div class="grid grid-cols-1 md:grid-cols-3 gap-5">
        <!-- Storage Path Card -->
        <div class="double-bezel">
          <div class="double-bezel-inner flex flex-col justify-between h-full space-y-4 !p-6">
            <div class="flex items-center justify-between">
              <span class="eyebrow !text-[9px]">Storage Repository</span>
              <Icon name="folder" size={16} class="text-[#e8a33d]" />
            </div>
            <div class="space-y-1">
              <div class="font-mono text-xs text-[#e9e2d2] truncate select-all">
                {app.config?.data_dir || 'G:\\membus\\desktop'}
              </div>
              <p class="text-[11px] text-[#8c887a]">
                Stores Pebble blockstore, peer keys, and DAG indexes.
              </p>
            </div>
            <button
              type="button"
              class="btn-rack text-xs justify-center py-2 font-mono"
              onclick={openDataDirFolder}
            >
              <span>Open Storage Directory ↗</span>
            </button>
          </div>
        </div>

        <!-- Web Explorer Full Control Card -->
        <div class="double-bezel">
          <div class="double-bezel-inner flex flex-col justify-between h-full space-y-4 !p-6">
            <div class="flex items-center justify-between">
              <span class="eyebrow !text-[9px]">Control Plane</span>
              <Icon name="explorer" size={16} class="text-[#57b79e]" />
            </div>
            <div class="space-y-1">
              <div class="font-display text-lg text-[#e9e2d2]">
                Web Explorer
              </div>
              <p class="text-[11px] text-[#8c887a]">
                Upload files, inspect 3D Merkle DAGs, browse MemNS and manage swarm peers.
              </p>
            </div>
            <button
              type="button"
              class="btn-ochre text-xs justify-center py-2 font-bold cursor-pointer"
              onclick={openWebExplorerFullControl}
            >
              <Icon name="explorer" size={13} />
              <span>Launch & Open Explorer ↗</span>
            </button>
          </div>
        </div>

        <!-- Live Diagnostics Card -->
        <div class="double-bezel">
          <div class="double-bezel-inner flex flex-col justify-between h-full space-y-4 !p-6">
            <div class="flex items-center justify-between">
              <span class="eyebrow !text-[9px]">Diagnostics & Logs</span>
              <Icon name="terminal" size={16} class="text-[#e8a33d]" />
            </div>
            <div class="space-y-1">
              <div class="font-display text-lg text-[#e9e2d2]">
                Daemon Log Stream
              </div>
              <p class="text-[11px] text-[#8c887a]">
                Inspect previous session logs, replay events, and trace errors.
              </p>
            </div>
            <button
              type="button"
              class="btn-rack text-xs justify-center py-2"
              onclick={() => app.activeTab = 'logs'}
            >
              <Icon name="terminal" size={13} />
              <span>View Logs Stream</span>
            </button>
          </div>
        </div>
      </div>
    </div>

  {:else if app.nodeStarting || (app.nodeStatus.process_running && !app.nodeStatus.api_online)}
    <!-- ======================================================== -->
    <!-- STATE 2: NODE LAUNCHING / BOOTSTRAPPING -->
    <!-- ======================================================== -->
    <section class="double-bezel animate-fade-in-up">
      <div class="double-bezel-inner flex flex-col items-center justify-center p-16 text-center space-y-5">
        <div class="w-16 h-16 rounded-[4px] bg-[rgba(232,163,61,0.06)] border border-[rgba(232,163,61,0.2)] text-[#e8a33d] flex items-center justify-center">
          <Icon name="refresh" size={32} class="animate-spin text-[#e8a33d]" />
        </div>
        <div class="max-w-md space-y-2">
          <h3 class="font-display text-2xl text-[#e9e2d2]">Initializing Membuss Node...</h3>
          <p class="text-xs text-[#8c887a] font-mono leading-relaxed">
            Opening Pebble blockstore, synchronizing Kademlia routing tables, and binding HTTP gateway on :{app.config?.gateway_addr?.split(':')[1] || '8083'}...
          </p>
        </div>
        <div class="w-64 bg-[rgba(233,226,210,0.06)] h-1.5 rounded-[1px] overflow-hidden">
          <div class="bg-[#e8a33d] h-full w-2/3 animate-pulse"></div>
        </div>
      </div>
    </section>

  {:else}
    <!-- ======================================================== -->
    <!-- STATE 3: LIVE ONLINE NODE DASHBOARD (FULL TELEMETRY) -->
    <!-- ======================================================== -->
    <div class="space-y-6 animate-fade-in-up">
      <!-- 1. Top Executive Control & Status Header -->
      <section class="double-bezel">
        <div class="double-bezel-inner flex flex-col gap-5 relative overflow-hidden !p-6 lg:!p-7">
          <span class="absolute right-0 top-0 h-full w-1.5 bg-gradient-to-b from-[#57b79e] to-transparent pointer-events-none"></span>

          <!-- Top Row: Status + Primary Actions -->
          <div class="flex flex-col lg:flex-row lg:items-center justify-between gap-6">
            <div class="space-y-2">
              <div class="flex items-center gap-3">
                <span class="relative flex h-3 w-3">
                  <span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-[#57b79e] opacity-60"></span>
                  <span class="relative inline-flex rounded-full h-3 w-3 bg-[#57b79e]"></span>
                </span>
                <h2 class="font-display text-xl md:text-2xl tracking-wide text-[#e9e2d2]">
                  Local Node Online & Active
                </h2>
                <span class="eyebrow !text-[9px] px-2 py-0.5 rounded-[3px] bg-[rgba(233,226,210,0.04)] border border-[rgba(233,226,210,0.08)]">
                  {app.nodeStatus.info?.version ? `v${app.nodeStatus.info.version}` : (app.config?.installed_version || 'v2.8.6')}
                </span>
              </div>

              <p class="text-xs text-[#8c887a] font-mono leading-relaxed">
                Swarm connected · Uptime: {formatUptime(app.nodeStatus.info?.uptime_sec)} · Active Repository: <span class="text-[#e9e2d2]">{app.config?.data_dir || '~/.membuss'}</span>
              </p>
            </div>

            <!-- Action Buttons -->
            <div class="flex flex-wrap items-center gap-3">
              <!-- FULL CONTROL BUTTON -->
              <button
                class="btn-ochre text-xs font-bold px-4 py-2.5 flex items-center gap-2 shadow-md shadow-[#e8a33d]/15 cursor-pointer"
                onclick={openWebExplorerFullControl}
                title="Open Web Explorer for full DAG inspection, file upload and peer management"
              >
                <Icon name="explorer" size={14} />
                <span>Web Explorer (Full Control) ↗</span>
              </button>

              <button
                class="btn-brick text-xs font-semibold px-4 py-2.5"
                onclick={() => app.stopNodeAction()}
                disabled={app.nodeStopping}
              >
                <Icon name="power" size={14} />
                <span>{app.nodeStopping ? 'Stopping...' : 'Stop Node'}</span>
              </button>

              <button
                class="btn-rack text-xs px-3.5 py-2.5"
                onclick={() => app.restartNodeAction()}
                title="Restart Node Daemon"
              >
                <Icon name="refresh" size={14} />
                <span>Restart</span>
              </button>

              {#if app.explorerOnline}
                <button
                  class="btn-rack text-xs px-4 py-2.5"
                  onclick={openGatewayExternal}
                  title="Open Web Explorer in external browser"
                >
                  <Icon name="external" size={14} class="text-[#57b79e]" />
                  <span>Browser (:8083)</span>
                </button>
              {/if}

              <button
                class="btn-rack text-xs px-4 py-2.5"
                onclick={() => app.showDownloaderModal = true}
              >
                <Icon name="download" size={14} class="text-[#e8a33d]" />
                <span>Download MID</span>
              </button>
            </div>
          </div>

          <!-- Peer ID Identifier Banner -->
          {#if app.nodeStatus.info?.peer_id}
            <div class="p-3.5 bg-[#0c1416] border border-[rgba(233,226,210,0.08)] rounded-[3px] flex flex-col md:flex-row md:items-center justify-between gap-3">
              <div class="flex items-center gap-3 truncate">
                <span class="eyebrow !text-[9px] shrink-0 text-[#e8a33d]">LIBP2P PEER ID</span>
                <span class="font-mono text-xs text-[#e9e2d2] truncate select-all">{app.nodeStatus.info.peer_id}</span>
              </div>
              <div class="flex items-center gap-3 shrink-0">
                <button
                  class="text-xs font-mono text-[#e8a33d] hover:text-[#f7cd8a] flex items-center gap-1.5 cursor-pointer"
                  onclick={copyPeerId}
                >
                  <Icon name={copiedPeerId ? "check" : "copy"} size={13} />
                  <span>{copiedPeerId ? 'Copied' : 'Copy ID'}</span>
                </button>
                <span class="text-[rgba(233,226,210,0.2)]">|</span>
                <button
                  class="text-xs font-mono text-[#8c887a] hover:text-[#e9e2d2] cursor-pointer"
                  onclick={() => showAddrs = !showAddrs}
                >
                  <span>{showAddrs ? 'Hide Addrs ▲' : `View ${app.nodeStatus.info?.addrs?.length || 0} Network Addrs ▼`}</span>
                </button>
              </div>
            </div>
          {/if}

          <!-- Collapsible Multiaddrs Drawer -->
          {#if showAddrs && app.nodeStatus.info?.addrs}
            <div class="p-3.5 bg-[#0c1416] border border-[rgba(233,226,210,0.08)] rounded-[3px] font-mono text-[11px] text-[#bcb4a1] max-h-40 overflow-y-auto space-y-1.5">
              <span class="eyebrow !text-[9px] block mb-1">Announced Network Multiaddrs</span>
              {#each app.nodeStatus.info.addrs as addr}
                <button
                  type="button"
                  class="w-full text-left truncate hover:text-white select-all cursor-pointer font-mono text-[11px] text-[#bcb4a1] block py-0.5 bg-transparent border-0"
                  onclick={() => copyText(addr, 'Address')}
                >
                  {addr}
                </button>
              {/each}
            </div>
          {/if}
        </div>
      </section>

      <!-- 2. Core Telemetry Matrix (4 Wide Responsive Tiles) -->
      <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5">
        <!-- Card 1: Storage & Capacity -->
        <div class="double-bezel">
          <div class="double-bezel-inner flex flex-col justify-between h-full space-y-4 !p-5">
            <div class="flex items-center justify-between">
              <span class="eyebrow !text-[9px]">Storage Size</span>
              <div class="p-1.5 rounded-[3px] bg-[rgba(233,226,210,0.04)] text-[#e8a33d] border border-[rgba(233,226,210,0.08)]">
                <Icon name="folder" size={16} />
              </div>
            </div>
            <div>
              <div class="font-display text-3xl text-[#e9e2d2] tabular-nums">
                {formatBytes(app.nodeStatus.info?.repo_size || 0)}
              </div>
              <div class="text-xs text-[#8c887a] mt-1 font-mono">
                {app.nodeStatus.info?.num_blocks || 0} DAG blocks · {app.nodeStatus.info?.sealed_count || 0} roots
              </div>
            </div>
            <div class="text-[10px] text-[#8c887a] border-t border-[rgba(233,226,210,0.06)] pt-2 font-mono flex items-center justify-between">
              <span>Pebble Blockstore</span>
              <span class="text-[#e8a33d]">Sealed</span>
            </div>
          </div>
        </div>

        <!-- Card 2: Swarm Peers & Routing -->
        <div class="double-bezel">
          <div class="double-bezel-inner flex flex-col justify-between h-full space-y-4 !p-5">
            <div class="flex items-center justify-between">
              <span class="eyebrow !text-[9px]">Swarm Peers</span>
              <div class="p-1.5 rounded-[3px] bg-[rgba(233,226,210,0.04)] text-[#57b79e] border border-[rgba(233,226,210,0.08)]">
                <Icon name="globe" size={16} />
              </div>
            </div>
            <div>
              <div class="font-display text-3xl text-[#e9e2d2] tabular-nums flex items-baseline gap-2">
                <span>{app.nodeStatus.info?.num_peers || 0}</span>
                <span class="text-sm font-normal text-[#8c887a]">peers</span>
              </div>
              <div class="text-xs text-[#8c887a] mt-1 font-mono">
                Kademlia DHT routing active
              </div>
            </div>
            <div class="text-[10px] text-[#57b79e] border-t border-[rgba(233,226,210,0.06)] pt-2 font-mono flex items-center justify-between">
              <span>Libp2p Mesh</span>
              <span>TCP / QUIC / WS</span>
            </div>
          </div>
        </div>

        <!-- Card 3: Anchor Sync Mode -->
        <div class="double-bezel">
          <div class="double-bezel-inner flex flex-col justify-between h-full space-y-4 !p-5">
            <div class="flex items-center justify-between">
              <span class="eyebrow !text-[9px]">Anchor System</span>
              <div class="p-1.5 rounded-[3px] bg-[rgba(233,226,210,0.04)] text-[#57b79e] border border-[rgba(233,226,210,0.08)]">
                <Icon name="shield" size={16} />
              </div>
            </div>
            <div>
              <div class="font-display text-xl text-[#e9e2d2]">
                {app.nodeStatus.info?.is_anchor ? 'Full Anchor Node' : 'Standard Peer Node'}
              </div>
              <div class="text-xs text-[#8c887a] mt-1 font-mono">
                {app.nodeStatus.info?.is_anchor ? 'Full network sync enabled' : 'On-demand block caching'}
              </div>
            </div>
            <div class="text-[10px] text-[#8c887a] border-t border-[rgba(233,226,210,0.06)] pt-2 font-mono flex items-center justify-between">
              <span>Replication</span>
              <span class={app.nodeStatus.info?.is_anchor ? 'text-[#57b79e]' : 'text-[#8c887a]'}>
                {app.nodeStatus.info?.is_anchor ? 'Full Network DAG' : 'Lightweight'}
              </span>
            </div>
          </div>
        </div>

        <!-- Card 4: Local Endpoints & Gateway -->
        <div class="double-bezel">
          <div class="double-bezel-inner flex flex-col justify-between h-full space-y-4 !p-5">
            <div class="flex items-center justify-between">
              <span class="eyebrow !text-[9px]">Service Endpoints</span>
              <div class="p-1.5 rounded-[3px] bg-[rgba(233,226,210,0.04)] text-[#e8a33d] border border-[rgba(233,226,210,0.08)]">
                <Icon name="server" size={16} />
              </div>
            </div>
            <div>
              <div class="font-mono text-xs text-[#57b79e] font-bold truncate">
                Gateway: :{app.config?.gateway_addr?.split(':')[1] || '8080'}
              </div>
              <div class="font-mono text-xs text-[#e8a33d] font-bold truncate mt-1">
                Control API: :{app.config?.api_addr?.split(':')[1] || '5001'}
              </div>
            </div>
            <div class="text-[10px] text-[#8c887a] border-t border-[rgba(233,226,210,0.06)] pt-2 font-mono flex items-center justify-between">
              <span>Daemon Stream</span>
              <span>gRPC :{app.config?.grpc_addr?.split(':')[1] || '50051'}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- 3. Live Bandwidth & Swarm Telemetry Flow (Expansive SVG Wave) -->
      <section class="double-bezel">
        <div class="double-bezel-inner flex flex-col gap-4 !p-6">
          <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-[rgba(233,226,210,0.08)] pb-3.5">
            <div class="flex items-center gap-3">
              <Icon name="globe" size={16} class="text-[#57b79e]" />
              <h3 class="eyebrow !text-[#e9e2d2]">Live Swarm Bandwidth Flow</h3>
            </div>
            <div class="flex items-center gap-6 font-mono text-xs">
              <div class="flex items-center gap-2">
                <span class="w-2.5 h-2.5 rounded-[2px] bg-[#e8a33d]"></span>
                <span class="text-[#8c887a]">Incoming:</span>
                <span class="text-[#e9e2d2] font-bold">{formatBytes(currentInSpeed)}/s</span>
              </div>
              <div class="flex items-center gap-2">
                <span class="w-2.5 h-2.5 rounded-[2px] bg-[#57b79e]"></span>
                <span class="text-[#8c887a]">Outgoing:</span>
                <span class="text-[#e9e2d2] font-bold">{formatBytes(currentOutSpeed)}/s</span>
              </div>
            </div>
          </div>

          <!-- SVG Wide Telemetry Graph -->
          <div class="w-full relative mt-1 overflow-hidden">
            <svg viewBox={`0 0 ${chartWidth} ${chartHeight}`} class="w-full h-36 select-none" preserveAspectRatio="none">
              <!-- Grid Lines -->
              <line x1="8" y1="8" x2={chartWidth-8} y2="8" stroke="rgba(233,226,210,0.04)" stroke-width="1" />
              <line x1="8" y1={chartHeight/2} x2={chartWidth-8} y2={chartHeight/2} stroke="rgba(233,226,210,0.04)" stroke-width="1" stroke-dasharray="4,4" />
              <line x1="8" y1={chartHeight-8} x2={chartWidth-8} y2={chartHeight-8} stroke="rgba(233,226,210,0.07)" stroke-width="1" />

              <!-- Area Paths -->
              <path d={getAreaPath(bandwidthIn)} fill="url(#in-grad-dash)" opacity="0.14" />
              <path d={getAreaPath(bandwidthOut)} fill="url(#out-grad-dash)" opacity="0.1" />

              <!-- Line Paths -->
              <path d={getSvgPath(bandwidthIn)} fill="none" stroke="#e8a33d" stroke-width="2" stroke-linecap="round" />
              <path d={getSvgPath(bandwidthOut)} fill="none" stroke="#57b79e" stroke-width="1.8" stroke-linecap="round" />

              <defs>
                <linearGradient id="in-grad-dash" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stop-color="#e8a33d" />
                  <stop offset="100%" stop-color="#e8a33d" stop-opacity="0" />
                </linearGradient>
                <linearGradient id="out-grad-dash" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stop-color="#57b79e" />
                  <stop offset="100%" stop-color="#57b79e" stop-opacity="0" />
                </linearGradient>
              </defs>
            </svg>
          </div>
        </div>
      </section>

      <!-- 4. Interactive Utilities & Storage Overview (2 Columns) -->
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-5">
        <!-- Quick MID Resolver & Content Downloader -->
        <div class="double-bezel">
          <div class="double-bezel-inner space-y-4 !p-6">
            <div class="flex items-center gap-2 border-b border-[rgba(233,226,210,0.08)] pb-3">
              <Icon name="download" size={16} class="text-[#e8a33d]" />
              <h3 class="eyebrow !text-[#e9e2d2]">Quick Content Resolver</h3>
            </div>

            <p class="text-xs text-[#8c887a] leading-relaxed">
              Retrieve and verify decentralized files directly from the swarm by entering a MID hash (<code class="text-[#e8a33d] font-mono">mem1z...</code>) or MemNS pointer name.
            </p>

            <div class="flex items-center gap-2 pt-1">
              <input
                type="text"
                bind:value={quickMid}
                placeholder="e.g. mem1z4... or memns://domain"
                class="bg-[#0c1416] border border-[rgba(233,226,210,0.1)] text-[#e9e2d2] text-xs font-mono rounded-[3px] p-2.5 flex-1 outline-none focus:border-[#e8a33d]"
                disabled={quickResolving}
              />
              <button
                class="btn-ochre text-xs shrink-0 px-4 py-2.5"
                onclick={handleQuickResolve}
                disabled={quickResolving}
              >
                <Icon name="download" size={14} />
                <span>{quickResolving ? 'Resolving...' : 'Download'}</span>
              </button>
            </div>
          </div>
        </div>

        <!-- Quick Navigation Hub -->
        <div class="double-bezel">
          <div class="double-bezel-inner space-y-4 !p-6">
            <div class="flex items-center gap-2 border-b border-[rgba(233,226,210,0.08)] pb-3">
              <Icon name="explorer" size={16} class="text-[#57b79e]" />
              <h3 class="eyebrow !text-[#e9e2d2]">Storage & Exploration Hub</h3>
            </div>

            <p class="text-xs text-[#8c887a] leading-relaxed">
              Explore the decentralized Merkle DAGs, browse pinned storage, monitor connected DHT nodes, or customize daemon ports.
            </p>

            <div class="grid grid-cols-3 gap-3 pt-1">
              <button class="btn-ochre text-xs justify-center py-2.5 font-bold cursor-pointer" onclick={openWebExplorerFullControl}>
                <Icon name="explorer" size={14} />
                <span>Web Explorer ↗</span>
              </button>

              <button class="btn-rack text-xs justify-center py-2.5" onclick={() => app.activeTab = 'config'}>
                <Icon name="config" size={14} class="text-[#57b79e]" />
                <span>Config</span>
              </button>

              <button class="btn-rack text-xs justify-center py-2.5" onclick={() => app.activeTab = 'logs'}>
                <Icon name="terminal" size={14} class="text-[#e8a33d]" />
                <span>Live Logs</span>
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  {/if}
</div>
