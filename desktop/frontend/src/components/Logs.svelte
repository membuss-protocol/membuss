<script>
  import { app } from '../lib/state.svelte';
  import Icon from './Icon.svelte';
  import { ClearDaemonLogs } from '../../wailsjs/go/main/App';

  let filterText = $state('');
  let autoScroll = $state(true);
  let isPaused = $state(false);
  let logsContainer = $state(null);
  let lastUpdated = $state(new Date().toLocaleTimeString());

  let filteredLogs = $derived.by(() => {
    if (!app.logs) return [];
    const lines = app.logs.split('\n');
    if (!filterText.trim()) return lines;
    const q = filterText.toLowerCase();
    return lines.filter(l => l.toLowerCase().includes(q));
  });

  $effect(() => {
    if (app.logs) {
      lastUpdated = new Date().toLocaleTimeString();
    }
  });

  $effect(() => {
    if (autoScroll && logsContainer && !isPaused) {
      logsContainer.scrollTop = logsContainer.scrollHeight;
    }
  });

  function copyAllLogs() {
    navigator.clipboard.writeText(app.logs || '');
    app.addToast('info', 'All logs copied to clipboard');
  }

  async function clearLogs() {
    try {
      await ClearDaemonLogs();
      app.logs = '';
      app.addToast('info', 'Daemon logs cleared');
    } catch (e) {
      app.logs = '';
    }
  }
</script>

<div class="h-full flex flex-col space-y-4">
  <!-- Terminal Topbar Control -->
  <div class="double-bezel shrink-0">
    <div class="double-bezel-inner !p-3 !px-4 flex flex-col lg:flex-row lg:items-center justify-between gap-3">
      <div class="flex items-center gap-3">
        <div class="flex items-center gap-2">
          <span class="relative flex h-2 w-2">
            <span class="animate-ping absolute inline-flex h-full w-full rounded-full {app.nodeStatus.process_running ? 'bg-[#57b79e] opacity-60' : 'bg-transparent'}"></span>
            <span class="relative inline-flex rounded-full h-2 w-2 {app.nodeStatus.process_running ? 'bg-[#57b79e]' : 'bg-[#5a574f]'}"></span>
          </span>
          <span class="eyebrow !text-[#e9e2d2]">Daemon Log Stream</span>
        </div>
        <span class="text-xs text-[#8c887a] font-mono">({filteredLogs.length} lines · Polled {lastUpdated})</span>
      </div>

      <!-- Actions -->
      <div class="flex flex-wrap items-center gap-2">
        <div class="relative">
          <input
            type="text"
            bind:value={filterText}
            placeholder="Filter logs..."
            class="bg-[#0c1416] border border-[rgba(233,226,210,0.1)] text-[#e9e2d2] text-xs font-mono rounded-[3px] py-1 px-2.5 w-40 outline-none focus:border-[#e8a33d]"
          />
          {#if filterText}
            <button class="absolute right-2 top-1.5 text-[#8c887a] hover:text-white cursor-pointer" onclick={() => filterText = ''}>
              <Icon name="x" size={12} />
            </button>
          {/if}
        </div>

        <button
          class="btn-rack text-xs py-1"
          onclick={() => isPaused = !isPaused}
          title={isPaused ? "Resume Live Stream" : "Pause Live Stream"}
        >
          <span>{isPaused ? "Resume" : "Pause"}</span>
        </button>

        <button
          class="btn-rack text-xs py-1 {autoScroll ? 'text-[#e8a33d]' : ''}"
          onclick={() => autoScroll = !autoScroll}
          title="Toggle Auto-Scroll"
        >
          <span>Auto-Scroll</span>
        </button>

        <button class="btn-rack text-xs py-1" onclick={copyAllLogs} title="Copy Logs to Clipboard">
          <Icon name="copy" size={13} />
          <span>Copy</span>
        </button>

        <button class="btn-rack text-xs py-1" onclick={clearLogs} title="Clear Terminal Window & Truncate Log File">
          <span>Clear</span>
        </button>

        {#if app.nodeStatus.process_running}
          <button class="btn-rack text-xs py-1" onclick={() => app.restartNodeAction()} title="Restart Node Daemon">
            <Icon name="refresh" size={13} class="text-[#e8a33d]" />
            <span>Restart Daemon</span>
          </button>
        {/if}
      </div>
    </div>
  </div>

  <!-- Terminal Display Area -->
  <div class="double-bezel flex-1 min-h-[500px]">
    <div
      bind:this={logsContainer}
      class="double-bezel-inner !p-4 font-mono text-[11px] leading-relaxed text-[#bcb4a1] overflow-y-auto max-h-[calc(100vh-210px)] select-text bg-[#0c1416]"
    >
      {#if filteredLogs.length > 0}
        {#each filteredLogs as line}
          <div class="py-0.5 whitespace-pre-wrap break-all hover:bg-[rgba(233,226,210,0.03)] px-1 rounded-[2px] {line.includes('=== MEMBUSS') ? 'text-[#e8a33d] font-bold border-b border-[rgba(232,163,61,0.2)] pb-1 mb-1' : line.toLowerCase().includes('err') || line.toLowerCase().includes('fail') ? 'text-[#e0654c]' : line.toLowerCase().includes('warn') ? 'text-[#e8a33d]' : line.toLowerCase().includes('info') ? 'text-[#57b79e]' : ''}">
            {line}
          </div>
        {/each}
      {:else}
        <div class="text-[#5a574f] py-8 text-center italic font-mono">
          {#if filterText}
            No log entries matching "{filterText}"
          {:else}
            No daemon logs available yet. Start the node to view streaming output.
          {/if}
        </div>
      {/if}
    </div>
  </div>
</div>
