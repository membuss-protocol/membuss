<script>
  import { app } from '../lib/state.svelte';
  import Icon from './Icon.svelte';
  import {
    GetNodeConfigRaw,
    SaveNodeConfigRaw,
    WriteDefaultConfig,
    SaveConfig,
    SelectDirectory
  } from '../../wailsjs/go/main/App';

  let configMode = $state('form'); // 'form' | 'raw'
  let rawYaml = $state('');
  let loadingConfig = $state(false);
  let saving = $state(false);

  let nodeForm = $state({
    data_dir: '',
    gateway_addr: '',
    api_addr: '',
    grpc_addr: '',
    keep_alive: false
  });

  $effect(() => {
    if (app.config) {
      nodeForm = {
        data_dir: app.config.data_dir || '',
        gateway_addr: app.config.gateway_addr || '127.0.0.1:8080',
        api_addr: app.config.api_addr || '127.0.0.1:5001',
        grpc_addr: app.config.grpc_addr || '127.0.0.1:50051',
        keep_alive: !!app.config.keep_alive
      };
    }
  });

  $effect(() => {
    if (configMode === 'raw' && !rawYaml && !loadingConfig) {
      loadRawYaml();
    }
  });

  async function loadRawYaml() {
    loadingConfig = true;
    try {
      rawYaml = await GetNodeConfigRaw();
    } catch (e) {
      app.addToast('error', 'Failed to read config.yaml: ' + (e.message || e));
    } finally {
      loadingConfig = false;
    }
  }

  async function pickDirectory() {
    try {
      const selected = await SelectDirectory();
      if (selected) {
        nodeForm.data_dir = selected;
      }
    } catch (e) {
      app.addToast('error', 'Folder picker failed: ' + (e.message || e));
    }
  }

  async function saveForm() {
    saving = true;
    try {
      const newCfg = {
        ...app.config,
        data_dir: nodeForm.data_dir.trim(),
        gateway_addr: nodeForm.gateway_addr.trim(),
        api_addr: nodeForm.api_addr.trim(),
        grpc_addr: nodeForm.grpc_addr.trim(),
        keep_alive: nodeForm.keep_alive
      };
      await SaveConfig(newCfg);
      app.config = newCfg;
      app.addToast('success', 'Configuration saved');
    } catch (e) {
      app.addToast('error', 'Failed to save settings: ' + (e.message || e));
    } finally {
      saving = false;
    }
  }

  async function saveRaw() {
    saving = true;
    try {
      await SaveNodeConfigRaw(rawYaml);
      app.addToast('success', 'config.yaml saved successfully');
    } catch (e) {
      app.addToast('error', 'Failed to write config.yaml: ' + (e.message || e));
    } finally {
      saving = false;
    }
  }

  async function resetDefaults() {
    if (!confirm('Reset config.yaml to default template?')) return;
    try {
      await WriteDefaultConfig(app.config?.data_dir || '');
      await loadRawYaml();
      app.addToast('info', 'Reset config.yaml to default template');
    } catch (e) {
      app.addToast('error', 'Failed to reset config: ' + (e.message || e));
    }
  }
</script>

<div class="space-y-6 max-w-5xl mx-auto">
  <!-- Mode Selector & Header -->
  <div class="flex items-center justify-between">
    <div>
      <h2 class="font-display text-lg text-[#e9e2d2]">Rack Configuration</h2>
      <p class="text-xs text-[#8c887a]">Configure repository storage, network endpoints, and daemon supervision</p>
    </div>

    <div class="flex items-center gap-3">
      <div class="flex bg-[#111d20] p-1 rounded-[4px] border border-[rgba(233,226,210,0.08)] text-xs font-semibold">
        <button
          class="px-3 py-1.5 rounded-[3px] transition-all cursor-pointer {configMode === 'form' ? 'bg-[rgba(233,226,210,0.08)] text-[#e8a33d] shadow' : 'text-[#8c887a] hover:text-[#e9e2d2]'}"
          onclick={() => configMode = 'form'}
        >
          Visual Form
        </button>
        <button
          class="px-3 py-1.5 rounded-[3px] transition-all cursor-pointer {configMode === 'raw' ? 'bg-[rgba(233,226,210,0.08)] text-[#e8a33d] shadow' : 'text-[#8c887a] hover:text-[#e9e2d2]'}"
          onclick={() => configMode = 'raw'}
        >
          Raw YAML
        </button>
      </div>

      {#if configMode === 'form'}
        <button class="btn-ochre text-xs" onclick={saveForm} disabled={saving}>
          <Icon name="check" size={14} />
          <span>{saving ? 'Saving...' : 'Save Settings'}</span>
        </button>
      {:else}
        <button class="btn-ochre text-xs" onclick={saveRaw} disabled={saving}>
          <Icon name="check" size={14} />
          <span>{saving ? 'Saving...' : 'Save YAML'}</span>
        </button>
        <button class="btn-rack text-xs" onclick={resetDefaults} title="Reset to default config.yaml">
          <Icon name="refresh" size={14} />
          <span>Reset Defaults</span>
        </button>
      {/if}
    </div>
  </div>

  {#if app.nodeStatus.process_running}
    <div class="p-3.5 bg-[rgba(232,163,61,0.08)] border border-[rgba(232,163,61,0.2)] rounded-[4px] text-xs text-[#f4cd8a] flex items-center justify-between gap-3 font-mono">
      <div class="flex items-center gap-2">
        <Icon name="warning" size={16} class="text-[#e8a33d] shrink-0" />
        <span>Daemon is active. Configuration updates will take effect after restarting the node.</span>
      </div>
      <button class="btn-rack py-1 px-3 text-xs shrink-0" onclick={() => app.restartNodeAction()}>
        Restart Now
      </button>
    </div>
  {/if}

  {#if loadingConfig}
    <div class="double-bezel">
      <div class="double-bezel-inner p-12 text-center text-xs text-[#8c887a] font-mono">
        Loading configuration...
      </div>
    </div>
  {:else if configMode === 'form'}
    <!-- Visual Form Settings -->
    <div class="double-bezel">
      <div class="double-bezel-inner space-y-6">
        <!-- Data Directory -->
        <div class="space-y-2">
          <span class="eyebrow block">Repository Data Directory</span>
          <p class="text-xs text-[#8c887a]">Disk path where Pebble blockstore, cryptographic keys, and state files are stored.</p>
          <div class="flex items-center gap-2 mt-2">
            <input
              type="text"
              bind:value={nodeForm.data_dir}
              class="bg-[#0c1416] border border-[rgba(233,226,210,0.1)] text-[#e9e2d2] text-xs font-mono rounded-[3px] p-2.5 flex-1 outline-none focus:border-[#e8a33d]"
              placeholder="e.g. C:\Users\YourName\.membuss"
            />
            <button class="btn-rack text-xs" onclick={pickDirectory}>
              <Icon name="folder" size={14} />
              <span>Browse</span>
            </button>
          </div>
        </div>

        <!-- Network Endpoints -->
        <div class="border-t border-[rgba(233,226,210,0.08)] pt-6 space-y-4">
          <span class="eyebrow block">Network Ports & Endpoints</span>

          <div class="grid grid-cols-1 sm:grid-cols-3 gap-4">
            <div>
              <span class="text-xs font-mono text-[#8c887a] block mb-1">Gateway Port</span>
              <input
                type="text"
                bind:value={nodeForm.gateway_addr}
                class="bg-[#0c1416] border border-[rgba(233,226,210,0.1)] text-[#e9e2d2] text-xs font-mono rounded-[3px] p-2.5 w-full outline-none focus:border-[#e8a33d]"
                placeholder="127.0.0.1:8080"
              />
              <span class="text-[10px] text-[#5a574f] mt-1 block font-mono">Public CDN & Web Explorer</span>
            </div>

            <div>
              <span class="text-xs font-mono text-[#8c887a] block mb-1">Control API Port</span>
              <input
                type="text"
                bind:value={nodeForm.api_addr}
                class="bg-[#0c1416] border border-[rgba(233,226,210,0.1)] text-[#e9e2d2] text-xs font-mono rounded-[3px] p-2.5 w-full outline-none focus:border-[#e8a33d]"
                placeholder="127.0.0.1:5001"
              />
              <span class="text-[10px] text-[#5a574f] mt-1 block font-mono">Local control & sealing API</span>
            </div>

            <div>
              <span class="text-xs font-mono text-[#8c887a] block mb-1">gRPC Port</span>
              <input
                type="text"
                bind:value={nodeForm.grpc_addr}
                class="bg-[#0c1416] border border-[rgba(233,226,210,0.1)] text-[#e9e2d2] text-xs font-mono rounded-[3px] p-2.5 w-full outline-none focus:border-[#e8a33d]"
                placeholder="127.0.0.1:50051"
              />
              <span class="text-[10px] text-[#5a574f] mt-1 block font-mono">CLI daemon stream socket</span>
            </div>
          </div>
        </div>

        <!-- Supervisor / Keep-Alive -->
        <div class="border-t border-[rgba(233,226,210,0.08)] pt-6">
          <label class="flex items-start gap-3 cursor-pointer">
            <input
              type="checkbox"
              bind:checked={nodeForm.keep_alive}
              class="mt-1 rounded bg-[#0c1416] border-[rgba(233,226,210,0.2)] text-[#e8a33d] focus:ring-[#e8a33d]"
            />
            <div>
              <span class="eyebrow !text-[#e9e2d2] block">Keep Node Alive on Application Exit</span>
              <span class="text-xs text-[#8c887a] block mt-0.5">
                When enabled, closing the desktop portal leaves the daemon running in the background.
              </span>
            </div>
          </label>
        </div>
      </div>
    </div>
  {:else}
    <!-- Raw YAML Editor -->
    <div class="double-bezel">
      <div class="double-bezel-inner !p-4">
        <textarea
          bind:value={rawYaml}
          class="w-full h-96 bg-[#0c1416] text-[#e9e2d2] font-mono text-xs p-4 rounded-[3px] border border-[rgba(233,226,210,0.1)] focus:border-[#e8a33d] outline-none resize-none leading-relaxed"
          placeholder="config.yaml contents..."
          spellcheck="false"
        ></textarea>
      </div>
    </div>
  {/if}
</div>
