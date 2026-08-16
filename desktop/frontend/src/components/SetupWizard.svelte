<script>
  import { app } from '../lib/state.svelte';
  import Icon from './Icon.svelte';
  import {
    SelectDirectory,
    InstallBinaries,
    WriteDefaultConfig,
    SaveConfig
  } from '../../wailsjs/go/main/App';

  let selectedDir = $state('');
  let step = $state('pick_dir'); // 'pick_dir' | 'installing' | 'complete'
  let progressText = $state('');
  let errorMessage = $state('');

  async function pickFolder() {
    try {
      const folder = await SelectDirectory();
      if (folder) {
        selectedDir = folder;
      }
    } catch (e) {
      errorMessage = e.message || String(e);
    }
  }

  async function runSetup() {
    if (!selectedDir) {
      errorMessage = 'Please select a storage directory';
      return;
    }

    step = 'installing';
    errorMessage = '';
    progressText = 'Initializing repository and downloading daemon binaries...';

    try {
      await InstallBinaries(selectedDir);
      progressText = 'Writing default node configuration...';
      await WriteDefaultConfig(selectedDir);
      const newCfg = {
        ...(app.config || {}),
        data_dir: selectedDir,
        setup_complete: true
      };
      await SaveConfig(newCfg);
      progressText = 'Setup complete!';
      app.addToast('success', 'Membuss repository configured successfully');
      await app.loadApp();
    } catch (e) {
      step = 'pick_dir';
      errorMessage = e.message || String(e);
      app.addToast('error', 'Setup failed: ' + errorMessage);
    }
  }
</script>

<div class="min-h-[500px] flex items-center justify-center p-6">
  <div class="double-bezel max-w-lg w-full">
    <div class="double-bezel-inner p-8 text-center space-y-6">
      <!-- Header Logo / Icon -->
      <div class="w-14 h-14 rounded-[4px] bg-[rgba(233,226,210,0.04)] border border-[rgba(233,226,210,0.08)] text-[#e8a33d] flex items-center justify-center mx-auto shadow-inner">
        <Icon name="server" size={28} />
      </div>

      <div>
        <h2 class="font-display text-xl text-[#e9e2d2]">Membuss Desktop Setup</h2>
        <p class="text-xs text-[#8c887a] mt-1 max-w-sm mx-auto">
          Welcome to Membuss! Set up your local node storage repository and download the latest daemon binaries to join the network.
        </p>
      </div>

      {#if step === 'pick_dir'}
        <div class="space-y-4 text-left pt-2">
          <div>
            <span class="eyebrow block">Choose Data Directory</span>
            <div class="flex items-center gap-2 mt-1.5">
              <input
                type="text"
                bind:value={selectedDir}
                placeholder="e.g. C:\Users\YourName\.membuss"
                class="bg-[#0c1416] border border-[rgba(233,226,210,0.1)] text-[#e9e2d2] text-xs font-mono rounded-[3px] p-2.5 flex-1 outline-none focus:border-[#e8a33d]"
              />
              <button class="btn-rack text-xs" onclick={pickFolder}>
                <Icon name="folder" size={14} />
                <span>Browse</span>
              </button>
            </div>
            <p class="text-[10px] text-[#5a574f] mt-1 font-mono">
              Your cryptographic keys, BadgerDB blockstore, and logs will be saved here.
            </p>
          </div>

          {#if errorMessage}
            <div class="p-3 bg-[rgba(224,101,76,0.1)] border border-[rgba(224,101,76,0.3)] rounded-[3px] text-xs text-[#ec8a76] flex items-center gap-2">
              <Icon name="warning" size={15} class="text-[#e0654c] shrink-0" />
              <span>{errorMessage}</span>
            </div>
          {/if}

          <button
            class="w-full btn-ochre justify-center py-2.5 text-xs font-bold mt-2"
            onclick={runSetup}
          >
            <span>Initialize Node & Download Binaries</span>
          </button>
        </div>
      {:else if step === 'installing'}
        <div class="space-y-4 py-6">
          <div class="flex items-center justify-center">
            <Icon name="refresh" size={32} class="animate-spin text-[#e8a33d]" />
          </div>
          <div class="space-y-1">
            <h4 class="font-display text-sm text-[#e9e2d2]">Installing Membuss Binaries</h4>
            <p class="text-xs text-[#8c887a] font-mono">{progressText}</p>
          </div>
          <div class="w-full bg-[rgba(233,226,210,0.06)] h-1.5 rounded-[1px] overflow-hidden">
            <div class="bg-[#e8a33d] h-full w-2/3 animate-pulse"></div>
          </div>
        </div>
      {/if}
    </div>
  </div>
</div>
