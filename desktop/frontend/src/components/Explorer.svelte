<script>
  import { onMount, onDestroy } from 'svelte';
  import { app } from '../lib/state.svelte';
  import Icon from './Icon.svelte';
  import { CheckExplorer } from '../../wailsjs/go/main/App';
  import * as wailsRuntime from '../../wailsjs/runtime/runtime';

  let iframeRef = $state(null);
  let isChecking = $state(false);
  let explorerUrl = $derived(`http://${app.config?.gateway_addr || '127.0.0.1:8083'}/explorer/`);
  let checkInterval = null;

  function handleIframeMessage(event) {
    if (!event || !event.data) return;
    const data = event.data;

    // Handle open external browser URL requests from embedded explorer
    if (data.type === 'open-external' && data.url) {
      try {
        wailsRuntime.BrowserOpenURL(data.url);
      } catch (e) {
        window.open(data.url, '_blank');
      }
    }

    // Handle copy requests from embedded explorer
    if ((data.type === 'copy' || data.type === 'copy-text') && data.text) {
      try {
        navigator.clipboard.writeText(data.text);
      } catch (e) {
        try {
          const el = document.createElement('textarea');
          el.value = data.text;
          el.setAttribute('readonly', '');
          el.style.position = 'absolute';
          el.style.left = '-9999px';
          document.body.appendChild(el);
          el.select();
          document.execCommand('copy');
          document.body.removeChild(el);
        } catch (err) {}
      }
    }
  }

  onMount(() => {
    window.addEventListener('message', handleIframeMessage);

    // Fast poller while waiting for gateway to come online
    checkGateway();
    checkInterval = setInterval(() => {
      if (!app.explorerOnline && app.nodeStatus.process_running) {
        checkGateway();
      }
    }, 800);
  });

  onDestroy(() => {
    window.removeEventListener('message', handleIframeMessage);
    if (checkInterval) clearInterval(checkInterval);
  });

  async function checkGateway() {
    if (isChecking) return;
    isChecking = true;
    try {
      const online = await CheckExplorer();
      app.explorerOnline = online;
    } catch (e) {
      app.explorerOnline = false;
    } finally {
      isChecking = false;
    }
  }

  function reloadExplorer() {
    if (iframeRef && app.explorerOnline) {
      iframeRef.src = explorerUrl + '?t=' + Date.now();
      app.addToast('info', 'Explorer frame refreshed');
    } else {
      checkGateway();
    }
  }

  function openExternal() {
    try {
      wailsRuntime.BrowserOpenURL(explorerUrl);
    } catch (e) {
      window.open(explorerUrl, '_blank');
    }
  }
</script>

<div class="h-full flex flex-col space-y-4">
  <!-- Explorer Header Chrome -->
  <div class="double-bezel shrink-0">
    <div class="double-bezel-inner !p-3 !px-4 flex items-center justify-between">
      <div class="flex items-center gap-3">
        <div class="flex items-center gap-2">
          <span class="relative flex h-2 w-2">
            <span class="animate-ping absolute inline-flex h-full w-full rounded-full {app.explorerOnline ? 'bg-[#57b79e] opacity-60' : 'bg-transparent'}"></span>
            <span class="relative inline-flex rounded-full h-2 w-2 {app.explorerOnline ? 'bg-[#57b79e]' : app.nodeStatus.process_running ? 'bg-[#e8a33d]' : 'bg-[#5a574f]'}"></span>
          </span>
          <span class="text-xs font-mono font-bold text-[#e9e2d2] truncate max-w-sm">{explorerUrl}</span>
        </div>
        {#if !app.nodeStatus.process_running}
          <span class="eyebrow !text-[9px] !text-[#e8a33d]">Daemon Standby</span>
        {:else if !app.explorerOnline}
          <span class="eyebrow !text-[9px] !text-[#e8a33d] animate-pulse">Initializing Gateway...</span>
        {:else}
          <span class="eyebrow !text-[9px] !text-[#57b79e]">Gateway Ready</span>
        {/if}
      </div>

      <div class="flex items-center gap-2">
        <button
          class="btn-rack text-xs"
          onclick={reloadExplorer}
          title="Reload Web Explorer Frame"
        >
          <Icon name="refresh" size={13} class={isChecking ? 'animate-spin' : ''} />
          <span>Reload</span>
        </button>

        <button
          class="btn-rack text-xs"
          onclick={openExternal}
          disabled={!app.explorerOnline}
          title="Open in your default web browser"
        >
          <Icon name="external" size={13} />
          <span>Open in Browser</span>
        </button>
      </div>
    </div>
  </div>

  <!-- Embedded Webview Container -->
  <div class="double-bezel flex-1 min-h-[520px]">
    <div class="double-bezel-inner !p-0 h-full w-full relative bg-[#0c1416] overflow-hidden">
      {#if app.explorerOnline}
        <iframe
          bind:this={iframeRef}
          src={explorerUrl}
          title="Membuss Web Explorer"
          class="w-full h-full border-0 absolute inset-0 bg-[#0c1416] pointer-events-auto"
          allow="clipboard-read; clipboard-write; fullscreen; cross-origin-isolated *"
          allowfullscreen
          loading="eager"
        ></iframe>
      {:else if app.nodeStatus.process_running}
        <!-- Gateway Initialization State (Prevents WebView2 Dead Cloud Error Page) -->
        <div class="w-full h-full flex flex-col items-center justify-center p-8 text-center space-y-4">
          <div class="w-14 h-14 rounded-[4px] bg-[rgba(232,163,61,0.06)] border border-[rgba(232,163,61,0.2)] text-[#e8a33d] flex items-center justify-center">
            <Icon name="refresh" size={28} class="animate-spin text-[#e8a33d]" />
          </div>
          <div class="max-w-sm space-y-1">
            <h3 class="font-display text-lg text-[#e9e2d2]">Binding HTTP Web Gateway</h3>
            <p class="text-xs text-[#8c887a] font-mono">
              Waiting for gateway service on {explorerUrl}...
            </p>
          </div>
          <div class="w-48 bg-[rgba(233,226,210,0.06)] h-1.5 rounded-[1px] overflow-hidden">
            <div class="bg-[#e8a33d] h-full w-2/3 animate-pulse"></div>
          </div>
        </div>
      {:else}
        <!-- Node Daemon Standby State -->
        <div class="w-full h-full flex flex-col items-center justify-center p-8 text-center space-y-4">
          <div class="w-16 h-16 rounded-[4px] bg-[rgba(233,226,210,0.04)] border border-[rgba(233,226,210,0.08)] flex items-center justify-center text-[#8c887a]">
            <Icon name="explorer" size={32} />
          </div>
          <div class="max-w-sm space-y-1">
            <h3 class="font-display text-lg text-[#e9e2d2]">Node Daemon is Standby</h3>
            <p class="text-xs text-[#8c887a]">
              Start the Membuss node daemon to launch the local Gateway and Web Explorer portal.
            </p>
          </div>
          <button
            class="btn-ochre text-xs px-6 py-2.5 font-bold"
            onclick={() => app.startNodeAction()}
            disabled={app.nodeStarting}
          >
            <Icon name="power" size={15} />
            <span>{app.nodeStarting ? 'Launching Node...' : 'Start Node & Open Explorer'}</span>
          </button>
        </div>
      {/if}
    </div>
  </div>
</div>
