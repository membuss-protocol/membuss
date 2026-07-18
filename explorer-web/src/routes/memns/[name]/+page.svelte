<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { apiFetch } from '$lib/api';
	import { base } from '$app/paths';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import ActionMenu from '$lib/components/ActionMenu.svelte';
	import Icon from '@iconify/svelte';

	interface MemRoute {
		target: string;
		weight: number;
		label: string;
	}

	interface MemLogEntry {
		sequence: number;
		value: string;
		timestamp: string;
		message: string;
	}

	interface MemNSData {
		Title: string;
		Name: string;
		Value: string;
		CleanValue: string;
		IsMID: boolean;
		Sequence: number;
		ExpiresAt: string;
		TTL: string;
		Routes: MemRoute[] | null;
		Delegates: string[] | null;
		Changelog: MemLogEntry[] | null;
	}

	let data = $state<MemNSData | null>(null);
	let loading = $state(true);
	let error = $state<string | null>(null);
	// 'unresolved' — the name isn't in the DHT (never published, expired, or not
	// yet propagated). 'failed' — an actual lookup/transport error worth showing raw.
	let errorKind = $state<'unresolved' | 'failed'>('failed');
	let retrying = $state(false);

	function classify(msg: string): 'unresolved' | 'failed' {
		const m = msg.toLowerCase();
		if (m.includes('not found') || m.includes('routing') || m.includes('no record') || m.includes('404')) {
			return 'unresolved';
		}
		return 'failed';
	}

	async function loadRecord() {
		try {
			const name = page.params.name;
			const res = await apiFetch(`/memns/${name}`);
			data = res;
			error = null;
			loading = false;
		} catch (err) {
			const msg = err instanceof Error ? err.message : 'Failed to resolve MemNS record';
			error = msg;
			errorKind = classify(msg);
			loading = false;
		} finally {
			retrying = false;
		}
	}

	async function retry() {
		retrying = true;
		loading = true;
		await loadRecord();
	}

	function formatDate(dateStr: string): string {
		try {
			const d = new Date(dateStr);
			return d.toISOString().replace('T', ' ').slice(0, 19) + ' UTC';
		} catch {
			return dateStr;
		}
	}

	onMount(() => {
		loadRecord();
	});
</script>

<div class="flex flex-col gap-6">
	<!-- Page Header -->
	<div class="border-b border-slate-800 pb-4 flex items-center justify-between gap-4">
		<div>
			<div class="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded bg-amber-950/60 border border-amber-800/40 text-[10px] text-amber-400 font-mono tracking-wider uppercase">
				Mutable Target (MemNS)
			</div>
			<h1 class="text-2xl font-bold text-slate-50 mt-1 break-all select-all">/{page.params.name}</h1>
		</div>
		{#if data}
			<ActionMenu
				target={page.params.name ?? ''}
				kind="memns"
				compact={false}
				shareOnly={true}
			/>
		{/if}
	</div>

	{#if loading && !data}
		<div class="grid grid-cols-1 gap-6">
			<div class="bg-slate-900 border border-slate-800 rounded-xl p-6 flex flex-col gap-4">
				<Skeleton width="14rem" height="0.9rem" />
				<div class="border-t border-slate-800 pt-4 flex flex-col gap-3">
					<div class="bg-slate-950/40 border border-slate-750 p-3 rounded-lg flex flex-col gap-2">
						<Skeleton width="7rem" height="0.6rem" />
						<Skeleton width="80%" height="0.9rem" />
					</div>
					<div class="grid grid-cols-2 md:grid-cols-4 gap-4">
						{#each Array(4) as _}
							<div class="flex flex-col gap-2">
								<Skeleton width="4rem" height="0.6rem" />
								<Skeleton width="5rem" height="0.8rem" />
							</div>
						{/each}
					</div>
				</div>
			</div>
			<div class="bg-slate-900 border border-slate-800 rounded-xl p-6 flex flex-col gap-4">
				<Skeleton width="10rem" height="0.9rem" />
				{#each Array(3) as _}
					<Skeleton width="100%" height="1.5rem" rounded="rounded-lg" />
				{/each}
			</div>
		</div>
	{:else if error && errorKind === 'unresolved'}
		<!-- Name isn't in the DHT: never published, expired, or not yet propagated. -->
		<div class="bg-slate-900 border border-slate-800 rounded-xl p-8 flex flex-col items-center text-center gap-4">
			<div class="flex h-14 w-14 items-center justify-center rounded-full bg-amber-950/40 border border-amber-800/40">
				<Icon icon="ph:signpost" class="text-3xl text-amber-500" />
			</div>
			<div class="flex flex-col gap-1.5 max-w-md">
				<h3 class="text-slate-200 font-bold text-sm">No record resolves for this name yet</h3>
				<p class="text-slate-500 text-xs leading-relaxed">
					This MemNS name isn't currently in the DHT. It may never have been published, its
					record may have expired past its TTL, or a fresh publish hasn't propagated to this node
					yet. Once a key publishes a value here, the resolved target will appear.
				</p>
			</div>
			<div class="flex items-center gap-2 mt-1">
				<button
					onclick={retry}
					disabled={retrying}
					class="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-lg bg-slate-850 hover:bg-slate-800 disabled:opacity-50 text-slate-200 border border-slate-750 text-xs font-bold transition-colors active:scale-[0.98]"
				>
					<Icon icon="ph:arrow-clockwise" class={`text-sm ${retrying ? 'animate-spin' : ''}`} />
					{retrying ? 'Resolving…' : 'Retry resolution'}
				</button>
				<a
					href={`${base}/memns`}
					class="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-lg text-slate-400 hover:text-slate-200 text-xs font-bold transition-colors"
				>
					Manage keys
				</a>
			</div>
			<code class="text-[10px] text-slate-600 font-mono break-all mt-1">{error}</code>
		</div>
	{:else if error}
		<!-- Genuine lookup / transport failure. -->
		<div class="bg-red-950/20 border border-red-800/40 rounded-xl p-6 flex flex-col items-center text-center gap-3">
			<Icon icon="ph:warning-octagon" class="text-3xl text-red-400" />
			<div class="flex flex-col gap-1 max-w-md">
				<h3 class="text-red-300 font-bold text-sm">Resolution failed</h3>
				<p class="text-red-400/70 text-xs font-mono break-all">{error}</p>
			</div>
			<button
				onclick={retry}
				disabled={retrying}
				class="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-lg bg-red-950/40 hover:bg-red-950/60 disabled:opacity-50 text-red-300 border border-red-900/40 text-xs font-bold transition-colors active:scale-[0.98] mt-1"
			>
				<Icon icon="ph:arrow-clockwise" class={`text-sm ${retrying ? 'animate-spin' : ''}`} />
				{retrying ? 'Retrying…' : 'Retry'}
			</button>
		</div>
	{:else if data}
		<div class="grid grid-cols-1 gap-6">
			<!-- Details Panel -->
			<div class="bg-slate-900 border border-slate-800 rounded-xl p-6 flex flex-col gap-4">
			<h3 class="font-bold text-sm text-slate-400 font-mono border-b border-slate-800 pb-2">
				Resolution Parameters
			</h3>
				<dl class="grid grid-cols-1 md:grid-cols-4 gap-4 text-xs font-mono">
					<div class="flex flex-col gap-1 md:col-span-4 bg-slate-950/40 border border-slate-750 p-3 rounded-lg">
						<span class="text-slate-500 uppercase text-[9px]">Resolved Value</span>
						{#if data.IsMID}
							<a 
								href={`${base}/mid/${data.CleanValue}`} 
								class="text-cyan-400 text-sm font-bold hover:underline break-all"
							>
								{data.Value}
							</a>
						{:else}
							<span class="text-slate-300 text-sm font-bold break-all">{data.Value}</span>
						{/if}
					</div>

					<div class="flex flex-col gap-1">
						<span class="text-slate-500 uppercase text-[9px]">Sequence Number</span>
						<span class="text-slate-200 text-sm font-bold">{data.Sequence}</span>
					</div>

					<div class="flex flex-col gap-1">
						<span class="text-slate-500 uppercase text-[9px]">Remaining TTL</span>
						<span class="text-slate-200 text-sm font-bold">{data.TTL}</span>
					</div>

					<div class="flex flex-col gap-1 md:col-span-2">
						<span class="text-slate-500 uppercase text-[9px]">Record Expiration</span>
						<span class="text-slate-200 text-sm font-bold">{formatDate(data.ExpiresAt)}</span>
					</div>
				</dl>
			</div>

			<!-- Routing Rules -->
			{#if data.Routes && data.Routes.length > 0}
				<div class="bg-slate-900 border border-slate-800 rounded-xl overflow-hidden">
					<div class="px-6 py-4 bg-slate-950/40 border-b border-slate-800">
						<h3 class="font-bold text-sm text-slate-300">Weighted Routing Rule Engine</h3>
					</div>
					<div class="overflow-x-auto">
						<table class="w-full text-left border-collapse text-sm">
							<thead>
								<tr class="border-b border-slate-800/60 text-slate-500 font-mono text-xs uppercase bg-slate-950/20">
									<th class="py-3 px-6 font-semibold">Route Label</th>
									<th class="py-3 px-6 font-semibold">Destination Target MID</th>
									<th class="py-3 px-6 font-semibold w-24 text-right">Weight</th>
								</tr>
							</thead>
							<tbody class="divide-y divide-slate-750/40 font-mono text-xs">
								{#each data.Routes as r}
									<tr class="hover:bg-slate-750/25 transition-colors">
										<td class="py-3.5 px-6 font-semibold text-slate-200">{r.label || 'n/a'}</td>
										<td class="py-3.5 px-6">
											<a href={`${base}/mid/${r.target}`} class="text-cyan-400 hover:underline">
												{r.target}
											</a>
										</td>
										<td class="py-3.5 px-6 text-slate-300 text-right font-bold">{r.weight}%</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				</div>
			{/if}

			<!-- Delegates -->
			{#if data.Delegates && data.Delegates.length > 0}
				<div class="bg-slate-900 border border-slate-800 rounded-xl p-6 flex flex-col gap-4">
					<h3 class="font-bold text-sm text-slate-400 font-mono border-b border-slate-800 pb-2">
						Delegated Signing Keys
					</h3>
					<ul class="flex flex-col gap-2 font-mono text-xs text-slate-300">
						{#each data.Delegates as key}
							<li class="bg-slate-950/60 border border-slate-750 px-4 py-2.5 rounded-lg select-all break-all">
								{key}
							</li>
						{/each}
					</ul>
				</div>
			{/if}

			<!-- Publish History Timeline -->
			<div class="bg-slate-900 border border-slate-800 rounded-xl overflow-hidden">
				<div class="px-6 py-4 bg-slate-950/40 border-b border-slate-800">
					<h3 class="font-bold text-sm text-slate-300">Publish History Timeline (MemLog)</h3>
				</div>
				
				{#if data.Changelog && data.Changelog.length > 0}
					<div class="overflow-x-auto">
						<table class="w-full text-left border-collapse text-sm">
							<thead>
								<tr class="border-b border-slate-800/60 text-slate-500 font-mono text-xs uppercase bg-slate-950/20">
									<th class="py-3 px-6 font-semibold w-16">Seq</th>
									<th class="py-3 px-6 font-semibold w-48">Timestamp</th>
									<th class="py-3 px-6 font-semibold">Value / Route Target</th>
									<th class="py-3 px-6 font-semibold text-right">Commit Message</th>
								</tr>
							</thead>
							<tbody class="divide-y divide-slate-750/40 font-mono text-xs">
								{#each data.Changelog as entry}
									<tr class="hover:bg-slate-750/25 transition-colors">
										<td class="py-3.5 px-6 text-slate-400 font-bold">{entry.sequence}</td>
										<td class="py-3.5 px-6 text-slate-500">{formatDate(entry.timestamp)}</td>
										<td class="py-3.5 px-6 break-all max-w-sm">
											{#if entry.value.startsWith('/mem/') || entry.value.startsWith('mem1')}
												{@const cleanVal = entry.value.replace('/mem/', '')}
												<a href={`${base}/mid/${cleanVal}`} class="text-cyan-400 hover:underline">
													{entry.value}
												</a>
											{:else}
												<span class="text-slate-350">{entry.value}</span>
											{/if}
										</td>
										<td class="py-3.5 px-6 text-slate-400 text-right font-sans italic">
											{entry.message || '—'}
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{:else}
					<div class="py-8 text-center text-slate-550 flex flex-col items-center justify-center gap-1.5">
						<Icon icon="ph:clock-counter-clockwise" class="text-3xl text-slate-600" />
						<p class="text-xs text-slate-500">No timeline events recorded for this record</p>
					</div>
				{/if}
			</div>
		</div>
	{/if}
</div>
