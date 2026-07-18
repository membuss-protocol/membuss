<script lang="ts">
	import DagNode from './DagNode.svelte';
	import DagGraph from './DagGraph.svelte';
	import Icon from '@iconify/svelte';

	let { mid, mode = $bindable<'tree' | 'graph'>('graph') }: {
		mid: string;
		mode?: 'tree' | 'graph';
	} = $props();
</script>

<div class="flex flex-col gap-4">
	<!-- View switch -->
	<div class="flex items-center justify-between gap-3">
		<div class="flex items-center rounded-lg border border-slate-800 bg-slate-950/50 p-0.5">
			<button
				onclick={() => (mode = 'graph')}
				class={`flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-semibold transition-colors ${
					mode === 'graph' ? 'bg-slate-800 text-cyan-400' : 'text-slate-500 hover:text-slate-300'
				}`}
			>
				<Icon icon="ph:graph" class="text-sm" /> Graph
			</button>
			<button
				onclick={() => (mode = 'tree')}
				class={`flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-semibold transition-colors ${
					mode === 'tree' ? 'bg-slate-800 text-cyan-400' : 'text-slate-500 hover:text-slate-300'
				}`}
			>
				<Icon icon="ph:tree-view" class="text-sm" /> Tree
			</button>
		</div>
		<span class="hidden font-mono text-[10px] text-slate-600 sm:block">
			{mode === 'graph' ? 'Force-directed block lattice' : 'Recursive Merkle hierarchy'}
		</span>
	</div>

	{#if mode === 'graph'}
		<DagGraph {mid} />
	{:else}
		<div
			class="max-h-[520px] overflow-y-auto rounded-xl border border-slate-850 bg-slate-950/40 p-4"
		>
			<DagNode {mid} depth={0} />
		</div>
	{/if}
</div>
