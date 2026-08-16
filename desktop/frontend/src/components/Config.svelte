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

  let customReleaseInput = $state('');
  let installCoreSelected = $state(true);
  let installDesktopSelected = $state(true);

  $effect(() => {
    if (app.config) {
      nodeForm = {
        data_dir: app.config.data_dir || '',
        gateway_addr: app.config.gateway_addr || '127.0.0.1:8083',
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

  async function saveForm() {
    saving = true;
    try {
      if (!app.config) return;
      const updated = {
        ...app.config,
        data_dir: nodeForm.data_dir,
        gateway_addr: nodeForm.gateway_addr,
        api_addr: nodeForm.api_addr,
        grpc_addr: nodeForm.grpc_addr,
        keep_alive: nodeForm.keep_alive
      };
      await SaveConfig(updated);
      app.config = updated;
      app.addToast('success', 'Configuration saved successfully');
      if (configMode === 'raw') {
        await loadRawYaml();
      }
    } catch (e) {
      app.addToast('error', 'Failed to save configuration: ' + (e.message || e));
    } finally {
      saving = false;
    }
  }

  async function saveRaw() {
    saving = true;
    try {
      await SaveNodeConfigRaw(rawYaml);
      await app.loadApp();
      app.addToast('success', 'Raw YAML saved successfully');
    } catch (e) {
      app.addToast('error', 'Failed to save config.yaml: ' + (e.message || e));
    } finally {
      saving = false;
    }
  }

  async function resetDefaults() {
    if (!confirm('Are you sure you want to reset configuration to defaults?')) return;
    try {
      await WriteDefaultConfig();
      await app.loadApp();
      if (configMode === 'raw') {
        await loadRawYaml();
      }
      app.addToast('success', 'Configuration reset to defaults');
    } catch (e) {
      app.addToast('error', 'Failed to reset defaults: ' + (e.message || e));
    }
  }

  async function pickDirectory() {
    try {
      const selected = await SelectDirectory();
      if (selected) {
        nodeForm.data_dir = selected;
      }
    } catch (e) {
      app.addToast('error', 'Failed to select directory: ' + (e.message || e));
    }
  }

  async function inspectRelease() {
    if (!customReleaseInput || !customReleaseInput.trim()) {
      app.addToast('warning', 'Please enter a valid tag or release URL');
      return;
    }
    await app.inspectCustomReleaseAction(customReleaseInput);
  }

  async function installCustomRelease() {
    if (!installCoreSelected && !installDesktopSelected) {
      app.addToast('warning', 'Select at least one component (Core or Desktop)');
      return;
    }
    const tagOrURL = app.customReleaseData?.tag_name || customReleaseInput;
    await app.installComponentsAction(tagOrURL, installCoreSelected, installDesktopSelected);
  }
</script>

<div class="space-y-6 max-w-4xl pb-12">
  <!-- Section Title -->
  <div class="flex items-center justify-between">
    <div>
      <h1 class="font-display text-xl text-[#e9e2d2]">Node Configuration</h1>
      <p class="text-xs text-[#8c887a] mt-0.5">Manage node network addresses, Pebble storage paths, and release versions.</p>
    </div>

    <!-- Toggle Visual Form vs Raw YAML -->
    <div class="flex items-center gap-2">
      <div class="flex bg-[#0c1416] p-1 rounded-[4px] border border-[rgba(233,226,210,0.08)] text-xs">
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
                placeholder="127.0.0.1:8083"
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

    <!-- Custom Release URL & Version Switcher Card -->
    <div class="double-bezel">
      <div class="double-bezel-inner space-y-5">
        <div class="flex items-center justify-between border-b border-[rgba(233,226,210,0.08)] pb-3.5">
          <div class="flex items-center gap-2.5">
            <div class="w-7 h-7 rounded-[3px] bg-[rgba(232,163,61,0.1)] text-[#e8a33d] flex items-center justify-center border border-[rgba(232,163,61,0.2)]">
              <Icon name="download" size={15} />
            </div>
            <div>
              <h2 class="font-display text-sm text-[#e9e2d2]">Custom Version & Release Switcher</h2>
              <p class="eyebrow !text-[9px]">Upgrade, Downgrade, or Install Any Release via Tag or GitHub URL</p>
            </div>
          </div>
          <span class="text-[10px] font-mono text-[#8c887a]">Current: {app.config?.installed_version || 'v2.8.6'}</span>
        </div>

        <div class="space-y-3">
          <p class="text-xs text-[#8c887a] leading-relaxed">
            Paste a GitHub release URL or enter a version tag to install a specific build. You can choose whether to install only the Desktop GUI, only the Daemon Core, or both.
          </p>

          <div class="flex items-center gap-2">
            <input
              type="text"
              bind:value={customReleaseInput}
              class="bg-[#0c1416] border border-[rgba(233,226,210,0.1)] text-[#e9e2d2] text-xs font-mono rounded-[3px] p-2.5 flex-1 outline-none focus:border-[#e8a33d]"
              placeholder="e.g. v2.8.3 or https://github.com/nnlgsakib/membuss/releases/tag/v2.8.3"
              onkeydown={(e) => e.key === 'Enter' && inspectRelease()}
            />
            <button
              class="btn-rack text-xs"
              onclick={inspectRelease}
              disabled={app.customReleaseLoading || app.updating}
            >
              <Icon name="refresh" size={13} class={app.customReleaseLoading ? 'animate-spin' : ''} />
              <span>{app.customReleaseLoading ? 'Inspecting...' : 'Inspect Release'}</span>
            </button>
          </div>

          <!-- Active Download / Extraction Progress -->
          {#if app.updating}
            <div class="p-4 bg-[#0c1416] border border-[rgba(232,163,61,0.2)] rounded-[3px] space-y-3">
              <div class="flex items-center justify-between text-xs font-mono">
                <span class="text-[#e9e2d2] truncate max-w-sm">{app.updateMessage || 'Installing components...'}</span>
                <span class="text-[#e8a33d] font-bold">{app.updateProgress}%</span>
              </div>
              <div class="w-full bg-[rgba(233,226,210,0.08)] h-2 rounded-[2px] overflow-hidden">
                <div
                  class="bg-[#e8a33d] h-full transition-all duration-300 ease-out"
                  style="width: {Math.max(5, app.updateProgress)}%"
                ></div>
              </div>
            </div>
          {/if}

          <!-- Resolved Release Panel -->
          {#if app.customReleaseData && !app.updating}
            <div class="p-4 bg-[#0c1416] rounded-[3px] border border-[rgba(87,183,158,0.25)] space-y-4">
              <!-- Version comparison header -->
              <div class="flex items-center justify-between">
                <div class="flex items-center gap-3">
                  <span class="text-xs font-mono text-[#8c887a]">Target Version:</span>
                  <span class="text-sm font-mono font-bold text-[#57b79e]">{app.customReleaseData.tag_name}</span>
                  {#if app.customReleaseData.is_newer}
                    <span class="px-1.5 py-0.5 rounded text-[8px] font-mono font-bold bg-[rgba(87,183,158,0.1)] text-[#57b79e] border border-[rgba(87,183,158,0.2)]">Upgrade</span>
                  {:else if app.customReleaseData.is_older}
                    <span class="px-1.5 py-0.5 rounded text-[8px] font-mono font-bold bg-[rgba(232,163,61,0.1)] text-[#e8a33d] border border-[rgba(232,163,61,0.2)]">Downgrade</span>
                  {:else}
                    <span class="px-1.5 py-0.5 rounded text-[8px] font-mono font-bold bg-[rgba(233,226,210,0.06)] text-[#8c887a] border border-[rgba(233,226,210,0.1)]">Current Version</span>
                  {/if}
                </div>
              </div>

              <!-- Component Selection Checkboxes -->
              <div class="space-y-2 border-t border-[rgba(233,226,210,0.08)] pt-3">
                <span class="eyebrow !text-[9px] block">Choose Components to Install</span>
                <div class="grid grid-cols-1 sm:grid-cols-2 gap-2.5">
                  <label class="flex items-start gap-2.5 p-2.5 rounded-[3px] bg-[#111d20] border cursor-pointer {installCoreSelected ? 'border-[#57b79e]' : 'border-[rgba(233,226,210,0.08)]'}">
                    <input type="checkbox" bind:checked={installCoreSelected} class="mt-0.5 accent-[#57b79e]" />
                    <div class="space-y-0.5">
                      <span class="text-xs font-semibold text-[#e9e2d2]">Daemon Core (membuss.exe)</span>
                      <p class="text-[10px] text-[#8c887a]">Installs the node & CLI core in your data bin/ directory.</p>
                    </div>
                  </label>

                  <label class="flex items-start gap-2.5 p-2.5 rounded-[3px] bg-[#111d20] border cursor-pointer {installDesktopSelected ? 'border-[#57b79e]' : 'border-[rgba(233,226,210,0.08)]'}">
                    <input type="checkbox" bind:checked={installDesktopSelected} class="mt-0.5 accent-[#57b79e]" />
                    <div class="space-y-0.5">
                      <span class="text-xs font-semibold text-[#e9e2d2]">Desktop GUI (membuss-desktop)</span>
                      <p class="text-[10px] text-[#8c887a]">Atomically updates the running desktop application executable.</p>
                    </div>
                  </label>
                </div>
              </div>

              <!-- Action button -->
              <div class="flex items-center justify-end gap-3 pt-2">
                <button
                  class="btn-ochre text-xs font-bold px-5 py-2"
                  onclick={installCustomRelease}
                  disabled={(!installCoreSelected && !installDesktopSelected) || app.updating}
                >
                  <Icon name="download" size={14} />
                  <span>Install {app.customReleaseData.tag_name}</span>
                </button>
              </div>
            </div>
          {/if}
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
