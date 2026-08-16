<script>
  import { app } from '../lib/state.svelte';
  import Icon from './Icon.svelte';
  import { DownloadContent, SelectDirectory } from '../../wailsjs/go/main/App';

  let midInput = $state('');
  let destDir = $state('');
  let downloading = $state(false);
  let errorMsg = $state('');
  let successFile = $state('');
  let progressMsg = $state('');

  async function pickDestFolder() {
    try {
      const folder = await SelectDirectory();
      if (folder) {
        destDir = folder;
      }
    } catch (e) {
      errorMsg = e.message || String(e);
    }
  }

  async function startDownload() {
    if (!midInput.trim()) {
      errorMsg = 'Please enter a valid MID or MemNS identifier';
      return;
    }

    downloading = true;
    errorMsg = '';
    successFile = '';
    progressMsg = 'Connecting to network and resolving MID...';

    try {
      const target = destDir.trim() || app.config?.data_dir || '';
      const savedPath = await DownloadContent(target, midInput.trim());
      successFile = savedPath;
      progressMsg = 'Download complete!';
      app.addToast('success', 'File downloaded successfully');
    } catch (e) {
      errorMsg = e.message || String(e);
      app.addToast('error', 'Download failed: ' + errorMsg);
    } finally {
      downloading = false;
    }
  }

  function closeModal() {
    if (downloading) return;
    app.showDownloaderModal = false;
    errorMsg = '';
    successFile = '';
    progressMsg = '';
  }
</script>

{#if app.showDownloaderModal}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div class="modal-backdrop" onclick={closeModal} role="presentation">
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div class="double-bezel max-w-md w-full" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true" tabindex="-1">
      <div class="double-bezel-inner !p-6 bg-[#111d20]">
        <!-- Modal Header -->
        <div class="flex items-center justify-between pb-3.5 border-b border-[rgba(233,226,210,0.08)]">
          <div class="flex items-center gap-2.5">
            <div class="w-8 h-8 rounded-[3px] bg-[rgba(233,226,210,0.04)] text-[#e8a33d] flex items-center justify-center border border-[rgba(233,226,210,0.08)]">
              <Icon name="download" size={17} />
            </div>
            <div>
              <h3 class="font-display text-sm text-[#e9e2d2]">Download Content</h3>
              <p class="eyebrow !text-[9px]">Retrieve files by MID or MemNS pointer</p>
            </div>
          </div>
          <button class="text-[#8c887a] hover:text-white p-1 cursor-pointer" onclick={closeModal}>
            <Icon name="x" size={16} />
          </button>
        </div>

        <!-- Modal Body -->
        <div class="py-4 space-y-4">
          <div>
            <span class="eyebrow block mb-1">Content Identifier (MID / MemNS)</span>
            <input
              type="text"
              bind:value={midInput}
              placeholder="e.g. mem1z4... or memns://domain"
              class="bg-[#0c1416] border border-[rgba(233,226,210,0.1)] text-[#e9e2d2] text-xs font-mono rounded-[3px] p-2.5 w-full outline-none focus:border-[#e8a33d]"
              disabled={downloading}
            />
          </div>

          <div>
            <span class="eyebrow block mb-1">Save Destination Directory</span>
            <div class="flex items-center gap-2">
              <input
                type="text"
                bind:value={destDir}
                placeholder="Default: Node directory"
                class="bg-[#0c1416] border border-[rgba(233,226,210,0.1)] text-[#e9e2d2] text-xs font-mono rounded-[3px] p-2.5 flex-1 outline-none focus:border-[#e8a33d]"
                disabled={downloading}
              />
              <button class="btn-rack text-xs shrink-0" onclick={pickDestFolder} disabled={downloading}>
                <Icon name="folder" size={13} />
                <span>Browse</span>
              </button>
            </div>
          </div>

          {#if downloading}
            <div class="p-3 bg-[rgba(232,163,61,0.08)] border border-[rgba(232,163,61,0.2)] rounded-[3px] text-xs text-[#f4cd8a] flex items-center gap-2.5 font-mono">
              <Icon name="refresh" size={14} class="animate-spin text-[#e8a33d] shrink-0" />
              <span>{progressMsg}</span>
            </div>
          {/if}

          {#if errorMsg}
            <div class="p-3 bg-[rgba(224,101,76,0.1)] border border-[rgba(224,101,76,0.3)] rounded-[3px] text-xs text-[#ec8a76] flex items-center gap-2.5 font-mono">
              <Icon name="warning" size={14} class="text-[#e0654c] shrink-0" />
              <span class="break-all">{errorMsg}</span>
            </div>
          {/if}

          {#if successFile}
            <div class="p-3 bg-[rgba(87,183,158,0.1)] border border-[rgba(87,183,158,0.3)] rounded-[3px] text-xs text-[#7fcdb6] flex items-center gap-2.5 font-mono">
              <Icon name="check" size={14} class="text-[#57b79e] shrink-0" />
              <span class="truncate">Saved to: {successFile}</span>
            </div>
          {/if}
        </div>

        <!-- Modal Footer -->
        <div class="pt-3 border-t border-[rgba(233,226,210,0.08)] flex items-center justify-end gap-2">
          <button class="btn-rack text-xs" onclick={closeModal} disabled={downloading}>
            Cancel
          </button>
          <button class="btn-ochre text-xs" onclick={startDownload} disabled={downloading}>
            <Icon name="download" size={14} />
            <span>{downloading ? 'Downloading...' : 'Start Download'}</span>
          </button>
        </div>
      </div>
    </div>
  </div>
{/if}
