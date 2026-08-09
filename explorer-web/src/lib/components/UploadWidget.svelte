<script lang="ts">
	import { onMount } from 'svelte';
	import { uploader, type UploadTask } from '$lib/uploader';
	import { base } from '$app/paths';
	import Icon from '@iconify/svelte';

	let tasks = $state<UploadTask[]>([]);

	onMount(() => {
		const unsubscribe = uploader.subscribe(() => {
			tasks = uploader.allTasks;
		});
		return unsubscribe;
	});

	function formatBytes(bytes: number): string {
		if (bytes === 0) return '0 B';
		const k = 1024;
		const sizes = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
		const i = Math.floor(Math.log(bytes) / Math.log(k));
		return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
	}

	function copyToClipboard(text: string) {
		navigator.clipboard.writeText(text);
	}
</script>

{#if tasks.length > 0}
	<div class="fixed bottom-6 right-6 z-50 flex flex-col gap-3 max-w-md w-full sm:w-96 shadow-2xl font-sans text-slate-100 transition-all duration-300">
		{#each tasks as task (task.id)}
			<div class="bg-slate-900/95 backdrop-blur-xl border border-slate-700/80 rounded-xl overflow-hidden shadow-2xl ring-1 ring-white/10 flex flex-col">
				<!-- Header Bar -->
				<div class="px-4 py-3 bg-slate-950/70 flex items-center justify-between border-b border-slate-800">
					<div class="flex items-center gap-2.5 truncate">
						{#if task.phase === 'uploading'}
							<Icon icon="ph:spinner-gap-bold" class="w-4 h-4 text-cyan-400 animate-spin shrink-0" />
						{:else if task.phase === 'indexing'}
							<Icon icon="ph:circle-notch-bold" class="w-4 h-4 text-amber-400 animate-spin shrink-0" />
						{:else if task.phase === 'done'}
							<Icon icon="ph:check-circle-fill" class="w-4 h-4 text-emerald-400 shrink-0" />
						{:else}
							<Icon icon="ph:x-circle-fill" class="w-4 h-4 text-rose-400 shrink-0" />
						{/if}
						<span class="text-xs font-semibold tracking-wide truncate text-slate-100" title={task.title}>
							{task.title}
						</span>
					</div>

					<div class="flex items-center gap-1 shrink-0">
						<button
							onclick={() => uploader.toggleMinimize(task.id)}
							class="p-1 hover:bg-slate-800 text-slate-400 hover:text-slate-200 rounded transition-colors"
							title={task.minimized ? 'Expand Panel' : 'Minimize Panel'}
						>
							<Icon icon={task.minimized ? 'ph:caret-up-bold' : 'ph:caret-down-bold'} class="w-3.5 h-3.5" />
						</button>
						<button
							onclick={() => uploader.removeTask(task.id)}
							class="p-1 hover:bg-slate-800 text-slate-400 hover:text-rose-400 rounded transition-colors"
							title="Close Task"
						>
							<Icon icon="ph:x-bold" class="w-3.5 h-3.5" />
						</button>
					</div>
				</div>

				{#if !task.minimized}
					<!-- Task Progress Summary -->
					<div class="p-4 flex flex-col gap-3">
						<div class="flex items-center justify-between text-[11px] font-mono text-slate-400">
							<span class="truncate pr-2">{task.statusText}</span>
							<span class="font-bold text-slate-200 shrink-0">{task.percent}%</span>
						</div>

						<!-- Progress Bar Container -->
						<div class="w-full h-2 bg-slate-950 rounded-full overflow-hidden border border-slate-800 relative">
							<div
								class={`h-full transition-all duration-300 rounded-full ${
									task.phase === 'done'
										? 'bg-emerald-400'
										: task.phase === 'error'
											? 'bg-rose-400'
											: task.phase === 'indexing'
												? 'bg-gradient-to-r from-amber-400 to-cyan-400 animate-pulse'
												: 'bg-cyan-400'
								}`}
								style={`width: ${task.percent}%`}
							></div>
						</div>

						<div class="flex items-center justify-between text-[10px] font-mono text-slate-500">
							<span>{formatBytes(task.loadedBytes)} / {formatBytes(task.totalBytes)}</span>
							<span>{task.items.length} {task.items.length === 1 ? 'item' : 'items'}</span>
						</div>

						<!-- Itemized File List -->
						<div class="mt-1 border-t border-slate-800/80 pt-2 max-h-48 overflow-y-auto flex flex-col gap-1.5 custom-scrollbar">
							{#each task.items as item}
								<div class="flex items-center justify-between text-[11px] py-1 px-1.5 rounded hover:bg-slate-800/40 transition-colors">
									<div class="flex items-center gap-2 truncate pr-2">
										<Icon icon="ph:file-text-light" class="w-3.5 h-3.5 text-slate-400 shrink-0" />
										<span class="truncate text-slate-300 font-mono text-[10.5px]" title={item.name}>{item.name}</span>
									</div>
									<div class="flex items-center gap-2 shrink-0 font-mono text-[10px]">
										<span class="text-slate-500">{formatBytes(item.size)}</span>
										{#if item.status === 'done'}
											<span class="text-emerald-400 flex items-center gap-0.5">
												<Icon icon="ph:check-bold" class="w-3 h-3" />
											</span>
											{#if item.mid}
												<button
													onclick={() => copyToClipboard(item.mid!)}
													class="p-0.5 text-slate-400 hover:text-cyan-400 transition-colors"
													title="Copy MID"
												>
													<Icon icon="ph:copy-bold" class="w-3 h-3" />
												</button>
												<a
													href={`${base}/mid/${item.mid}`}
													class="text-cyan-400 hover:underline text-[10px]"
												>
													View
												</a>
											{/if}
										{:else if item.status === 'indexing'}
											<span class="text-amber-400">Indexing</span>
										{:else if item.status === 'error'}
											<span class="text-rose-400">Error</span>
										{:else}
											<span class="text-cyan-400">Uploading</span>
										{/if}
									</div>
								</div>
							{/each}
						</div>
					</div>
				{/if}
			</div>
		{/each}
	</div>
{/if}
