<script lang="ts">
	import { toasts, removeToast } from '$lib/toast';
	import Icon from '@iconify/svelte';
	import { flip } from 'svelte/animate';
	import { fade, slide } from 'svelte/transition';
</script>

<div class="fixed bottom-5 right-5 z-[100] flex flex-col gap-3 max-w-sm w-full pointer-events-none">
	{#each $toasts as t (t.id)}
		<div
			animate:flip={{ duration: 250 }}
			in:slide={{ duration: 200, axis: 'y' }}
			out:fade={{ duration: 150 }}
			class="pointer-events-auto flex items-start gap-3 p-4 rounded-xl border bg-slate-900/95 backdrop-blur-md shadow-xl select-none transition-all duration-200 hover:scale-[1.01] {t.type === 'error' ? 'border-red-500/40' : t.type === 'success' ? 'border-emerald-500/40' : 'border-slate-800/80'}"
		>
			<!-- Icon -->
			<span class="text-lg shrink-0 mt-0.5">
				{#if t.type === 'error'}
					<Icon icon="ph:warning-circle-fill" class="text-red-400" />
				{:else if t.type === 'success'}
					<Icon icon="ph:check-circle-fill" class="text-emerald-400" />
				{:else}
					<Icon icon="ph:info-fill" class="text-cyan-400" />
				{/if}
			</span>

			<!-- Content -->
			<div class="flex-grow flex flex-col gap-0.5">
				<span class="text-[11px] font-mono text-slate-500 uppercase tracking-wider font-semibold">
					{#if t.type === 'error'}
						Error Detected
					{:else if t.type === 'success'}
						Operation Succeeded
					{:else}
						Notification
					{/if}
				</span>
				<p class="text-xs text-slate-200 font-sans leading-relaxed break-words pr-2 font-medium">
					{t.message}
				</p>
			</div>

			<!-- Close Button -->
			<button
				onclick={() => removeToast(t.id)}
				class="text-slate-500 hover:text-slate-300 transition-colors p-0.5 rounded hover:bg-slate-800/60 shrink-0 self-start"
			>
				<Icon icon="ph:x" class="w-3.5 h-3.5" />
			</button>
		</div>
	{/each}
</div>
