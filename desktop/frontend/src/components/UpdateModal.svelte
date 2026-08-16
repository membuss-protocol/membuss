<script>
  import { app } from '../lib/state.svelte';
  import Icon from './Icon.svelte';

  function close() {
    if (app.updating) return;
    app.showUpdateModal = false;
  }
</script>

{#if app.showUpdateModal && app.updateInfo}
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
              <h3 class="font-display text-sm text-[#e9e2d2]">Release Update Available</h3>
              <p class="eyebrow !text-[9px]">New Membuss release ready for upgrade</p>
            </div>
          </div>
          <button class="text-[#8c887a] hover:text-white p-1 cursor-pointer" onclick={close}>
            <Icon name="x" size={16} />
          </button>
        </div>

        <!-- Modal Body -->
        <div class="py-5 space-y-4">
          <div class="p-3.5 bg-[#0c1416] rounded-[3px] border border-[rgba(233,226,210,0.08)] flex items-center justify-around text-center">
            <div>
              <span class="eyebrow !text-[9px] block">Installed</span>
              <span class="text-xs font-mono font-bold text-[#e9e2d2] mt-1 block">{app.updateInfo.current_version}</span>
            </div>
            <div class="text-[#5a574f] font-mono">→</div>
            <div>
              <span class="eyebrow !text-[9px] !text-[#57b79e] block">Latest Release</span>
              <span class="text-xs font-mono font-bold text-[#57b79e] mt-1 block">{app.updateInfo.latest_version}</span>
            </div>
          </div>

          <p class="text-xs text-[#8c887a] leading-relaxed">
            Updating will stop the local daemon, download and extract the latest binaries, and resume the node seamlessly.
          </p>

          {#if app.updating}
            <div class="p-3 bg-[rgba(232,163,61,0.08)] border border-[rgba(232,163,61,0.2)] rounded-[3px] text-xs text-[#f4cd8a] flex items-center gap-2.5 font-mono">
              <Icon name="refresh" size={14} class="animate-spin text-[#e8a33d] shrink-0" />
              <span>Downloading update archive and replacing binaries...</span>
            </div>
          {/if}
        </div>

        <!-- Modal Footer -->
        <div class="pt-3 border-t border-[rgba(233,226,210,0.08)] flex items-center justify-end gap-2">
          <button class="btn-rack text-xs" onclick={close} disabled={app.updating}>
            Later
          </button>
          <button class="btn-ochre text-xs" onclick={() => app.upgradeBinariesAction()} disabled={app.updating}>
            <Icon name="download" size={14} />
            <span>{app.updating ? 'Updating...' : 'Install Update Now'}</span>
          </button>
        </div>
      </div>
    </div>
  </div>
{/if}
