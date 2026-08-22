<script>
  import { app } from '../lib/state.svelte';
  import Icon from './Icon.svelte';

  let selectedTag = $state('');

  $effect(() => {
    if (app.showUpdateModal) {
      if (app.availableVersions.length === 0) {
        app.loadAvailableVersions();
      }
      if (!selectedTag && app.updateInfo?.latest_version) {
        selectedTag = app.updateInfo.latest_version;
      }
    }
  });

  function close() {
    if (app.updating) return;
    app.showUpdateModal = false;
  }

  function handleInstall() {
    app.installVersionAction(selectedTag || app.updateInfo?.latest_version);
  }
</script>

{#if app.showUpdateModal}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div class="modal-backdrop" onclick={close} role="presentation">
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div class="double-bezel max-w-md w-full" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true" tabindex="-1">
      <div class="double-bezel-inner !p-6 bg-[#111d20]">
        <!-- Modal Header -->
        <div class="flex items-center justify-between pb-3.5 border-b border-[rgba(233,226,210,0.08)]">
          <div class="flex items-center gap-2.5">
            <div class="w-8 h-8 rounded-[3px] bg-[rgba(87,183,158,0.1)] text-[#57b79e] flex items-center justify-center border border-[rgba(87,183,158,0.2)]">
              <Icon name="refresh" size={17} />
            </div>
            <div>
              <h3 class="font-display text-sm text-[#e9e2d2]">Release & Version Manager</h3>
              <p class="eyebrow !text-[9px]">Upgrade, downgrade, or switch binary version</p>
            </div>
          </div>
          <button class="text-[#8c887a] hover:text-white p-1 cursor-pointer" onclick={close}>
            <Icon name="x" size={16} />
          </button>
        </div>

        <!-- Modal Body -->
        <div class="py-5 space-y-4">
          <!-- Current vs Target Card -->
          <div class="p-3.5 bg-[#0c1416] rounded-[3px] border border-[rgba(233,226,210,0.08)] flex items-center justify-around text-center">
            <div>
              <span class="eyebrow !text-[9px] block">Installed</span>
              <span class="text-xs font-mono font-bold text-[#e9e2d2] mt-1 block">
                {app.updateInfo?.current_version || app.config?.installed_version || 'v2.9.4'}
              </span>
            </div>
            <div class="text-[#5a574f] font-mono">→</div>
            <div>
              <span class="eyebrow !text-[9px] !text-[#57b79e] block">Target Version</span>
              <span class="text-xs font-mono font-bold text-[#57b79e] mt-1 block">
                {selectedTag || app.updateInfo?.latest_version || 'Latest'}
              </span>
            </div>
          </div>

          <!-- Version Picker Dropdown -->
          <div class="space-y-1.5">
            <div class="flex items-center justify-between">
              <span class="eyebrow !text-[9px]">Select Release Target</span>
              {#if app.loadingVersions}
                <span class="text-[9px] font-mono text-[#8c887a] flex items-center gap-1">
                  <Icon name="refresh" size={10} class="animate-spin text-[#e8a33d]" />
                  fetching tags...
                </span>
              {/if}
            </div>

            {#if app.availableVersions && app.availableVersions.length > 0}
              <select
                bind:value={selectedTag}
                disabled={app.updating}
                class="w-full bg-[#0c1416] border border-[rgba(233,226,210,0.12)] text-[#e9e2d2] text-xs font-mono rounded-[3px] p-2.5 outline-none focus:border-[#e8a33d]"
              >
                {#each app.availableVersions as rel}
                  <option value={rel.tag_name}>
                    {rel.tag_name} {rel.is_latest ? '(Latest Release)' : ''} {rel.is_current ? '(Currently Installed)' : ''} {rel.type === 'downgrade' ? '(Older)' : ''} - {rel.published_at}
                  </option>
                {/each}
              </select>
            {:else}
              <input
                type="text"
                bind:value={selectedTag}
                disabled={app.updating}
                placeholder="e.g. v2.8.8, v2.8.9..."
                class="w-full bg-[#0c1416] border border-[rgba(233,226,210,0.12)] text-[#e9e2d2] text-xs font-mono rounded-[3px] p-2.5 outline-none focus:border-[#e8a33d]"
              />
            {/if}
          </div>

          <p class="text-xs text-[#8c887a] leading-relaxed">
            Membuss downloads the verified binary in a sandboxed staging directory, validates its integrity, stops the daemon, and atomically promotes the binary with auto-rollback protection.
          </p>

          {#if app.updating}
            <div class="p-3 bg-[rgba(232,163,61,0.08)] border border-[rgba(232,163,61,0.2)] rounded-[3px] text-xs text-[#f4cd8a] space-y-2 font-mono">
              <div class="flex items-center justify-between">
                <span class="flex items-center gap-2">
                  <Icon name="refresh" size={14} class="animate-spin text-[#e8a33d] shrink-0" />
                  <span>{app.updateMessage || 'Applying version update...'}</span>
                </span>
                {#if app.updateProgress > 0}
                  <span class="font-bold">{app.updateProgress}%</span>
                {/if}
              </div>
              {#if app.updateProgress > 0}
                <div class="w-full bg-[rgba(233,226,210,0.1)] h-1.5 rounded-full overflow-hidden">
                  <div
                    class="bg-[#e8a33d] h-full transition-all duration-300 rounded-full"
                    style="width: {Math.max(5, Math.min(100, app.updateProgress))}%"
                  ></div>
                </div>
              {/if}
            </div>
          {/if}
        </div>

        <!-- Modal Footer -->
        <div class="pt-3 border-t border-[rgba(233,226,210,0.08)] flex items-center justify-end gap-2">
          <button class="btn-rack text-xs" onclick={close} disabled={app.updating}>
            Cancel
          </button>
          <button class="btn-ochre text-xs" onclick={handleInstall} disabled={app.updating}>
            <Icon name="download" size={14} />
            <span>{app.updating ? 'Installing...' : 'Install Version'}</span>
          </button>
        </div>
      </div>
    </div>
  </div>
{/if}
