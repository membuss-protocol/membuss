<script>
  import { onMount, onDestroy } from 'svelte';
  import { app } from '../lib/state.svelte';
  import Icon from './Icon.svelte';

  let countdown = $state(5);
  let countdownTimer = null;

  $effect(() => {
    if (app.updateCompleted && !countdownTimer) {
      countdown = 5;
      countdownTimer = setInterval(() => {
        countdown -= 1;
        if (countdown <= 0) {
          clearInterval(countdownTimer);
          app.relaunchAppAction();
        }
      }, 1000);
    }
  });

  onDestroy(() => {
    if (countdownTimer) clearInterval(countdownTimer);
  });

  function close() {
    if (app.updating) return;
    if (countdownTimer) clearInterval(countdownTimer);
    app.showUpdateModal = false;
  }
</script>

{#if app.showUpdateModal && app.updateInfo}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div class="modal-backdrop" onclick={close} role="presentation">
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div class="double-bezel max-w-lg w-full" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true" tabindex="-1">
      <div class="double-bezel-inner !p-6 bg-[#111d20] space-y-4">
        <!-- Modal Header -->
        <div class="flex items-center justify-between pb-3.5 border-b border-[rgba(233,226,210,0.08)]">
          <div class="flex items-center gap-2.5">
            <div class="w-8 h-8 rounded-[3px] bg-[rgba(87,183,158,0.1)] text-[#57b79e] flex items-center justify-center border border-[rgba(87,183,158,0.2)]">
              <Icon name="refresh" size={17} />
            </div>
            <div>
              <h3 class="font-display text-sm text-[#e9e2d2]">
                {app.updateCompleted ? 'Update Complete' : 'Dual Self-Update Available'}
              </h3>
              <p class="eyebrow !text-[9px]">
                {app.updateCompleted ? 'Daemon & Desktop Executable Updated' : 'Upgrades both Daemon and Desktop App'}
              </p>
            </div>
          </div>
          {#if !app.updating && !app.updateCompleted}
            <button class="text-[#8c887a] hover:text-white p-1 cursor-pointer" onclick={close}>
              <Icon name="x" size={16} />
            </button>
          {/if}
        </div>

        <!-- Modal Body -->
        <div class="py-2 space-y-4">
          <!-- Version Pill Comparison -->
          <div class="p-3.5 bg-[#0c1416] rounded-[3px] border border-[rgba(233,226,210,0.08)] flex items-center justify-around text-center">
            <div>
              <span class="eyebrow !text-[9px] block">Installed Version</span>
              <span class="text-xs font-mono font-bold text-[#e9e2d2] mt-1 block">{app.updateInfo.current_version}</span>
            </div>
            <div class="text-[#5a574f] font-mono">→</div>
            <div>
              <span class="eyebrow !text-[9px] !text-[#57b79e] block">Latest Release</span>
              <span class="text-xs font-mono font-bold text-[#57b79e] mt-1 block">{app.updateInfo.latest_version}</span>
            </div>
          </div>

          {#if app.updateCompleted}
            <!-- Success / Relaunch State -->
            <div class="p-4 bg-[rgba(87,183,158,0.08)] border border-[rgba(87,183,158,0.25)] rounded-[3px] space-y-2">
              <div class="flex items-center gap-2 text-[#57b79e] font-bold text-xs">
                <Icon name="check" size={16} />
                <span>Self-Update Applied Successfully!</span>
              </div>
              <p class="text-xs text-[#bcb4a1] leading-relaxed">
                Both the Membuss Daemon binary and Desktop Application executable have been replaced with the new release.
              </p>
              <div class="text-[11px] font-mono text-[#e8a33d] pt-1">
                Auto-relaunching in {countdown} seconds...
              </div>
            </div>
          {:else if app.updating}
            <!-- Active Download & Extraction Progress -->
            <div class="p-4 bg-[#0c1416] border border-[rgba(232,163,61,0.2)] rounded-[3px] space-y-3">
              <div class="flex items-center justify-between text-xs font-mono">
                <span class="text-[#e9e2d2]">{app.updateMessage || 'Downloading release archive...'}</span>
                <span class="text-[#e8a33d] font-bold">{app.updateProgress}%</span>
              </div>
              <div class="w-full bg-[rgba(233,226,210,0.08)] h-2 rounded-[2px] overflow-hidden">
                <div
                  class="bg-[#e8a33d] h-full transition-all duration-300 ease-out"
                  style="width: {Math.max(5, app.updateProgress)}%"
                ></div>
              </div>
              <p class="text-[10px] text-[#8c887a] font-mono">
                Safe atomic replacement: Original binaries backed up as <code class="text-[#e9e2d2]">.old</code>
              </p>
            </div>
          {:else}
            <!-- Pre-install information -->
            <p class="text-xs text-[#8c887a] leading-relaxed">
              Updating will download the latest release, perform an atomic self-replacement on both the <code class="text-[#e9e2d2]">membuss</code> daemon and <code class="text-[#e9e2d2]">membuss-desktop</code> GUI, and seamlessly relaunch.
            </p>
          {/if}
        </div>

        <!-- Modal Footer -->
        <div class="pt-3 border-t border-[rgba(233,226,210,0.08)] flex items-center justify-end gap-2.5">
          {#if app.updateCompleted}
            <button class="btn-ochre text-xs font-bold px-6 py-2.5 flex items-center gap-2" onclick={() => app.relaunchAppAction()}>
              <Icon name="refresh" size={14} />
              <span>Relaunch Application Now</span>
            </button>
          {:else}
            <button class="btn-rack text-xs" onclick={close} disabled={app.updating}>
              Cancel
            </button>
            <button class="btn-ochre text-xs font-bold" onclick={() => app.upgradeBinariesAction()} disabled={app.updating}>
              <Icon name="download" size={14} class={app.updating ? 'animate-spin' : ''} />
              <span>{app.updating ? 'Applying Self-Update...' : 'Install Update Now'}</span>
            </button>
          {/if}
        </div>
      </div>
    </div>
  </div>
{/if}
