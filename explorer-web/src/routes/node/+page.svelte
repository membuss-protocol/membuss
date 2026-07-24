<script lang="ts">
	import { onMount } from 'svelte';
	import { apiFetch, formatBytes } from '$lib/api';
	import { base } from '$app/paths';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import Icon from '@iconify/svelte';

	interface NodeInfo {
		PeerID: string;
		Addrs: string[];
		Version: string;
		Build: string;
		AnchorMode: boolean;
	}

	interface KeyringKey {
		name: string;
		memns_name: string;
		type: string;
		created_at: string;
	}

	interface NodeData {
		Title: string;
		NodeInfo: NodeInfo;
		StoreBytes: number;
		SealedCount: number;
		Keys: KeyringKey[];
	}

	let data = $state<NodeData | null>(null);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let copiedId = $state<string | null>(null);
	let flashing = $state(false);
	let flashSuccess = $state(false);

	let showConfirmModal = $state(false);
	let confirmStep = $state(1);

	function triggerFlashNode() {
		confirmStep = 1;
		showConfirmModal = true;
	}

	async function proceedFlashNode() {
		if (confirmStep === 1) {
			confirmStep = 2;
			return;
		}
		showConfirmModal = false;
		flashing = true;
		try {
			await apiFetch('/node/flash', { method: 'POST' });
			flashSuccess = true;
			setTimeout(() => {
				flashSuccess = false;
				loadNode();
			}, 2000);
		} catch (err) {
			error = err instanceof Error ? err.message : 'Failed to flash node';
		} finally {
			flashing = false;
		}
	}

	async function loadNode() {
		try {
			const res = await apiFetch('/node');
			data = res;
			loading = false;
		} catch (err) {
			error = err instanceof Error ? err.message : 'Failed to load node details';
			loading = false;
		}
	}

	function copyToClipboard(text: string, id: string) {
		navigator.clipboard.writeText(text).then(() => {
			copiedId = id;
			setTimeout(() => {
				if (copiedId === id) copiedId = null;
			}, 1500);
		});
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
		loadNode();
	});

	type AddrCategory = 'all' | 'public' | 'lan' | 'local' | 'link-local';

	let activeAddrCategory = $state<AddrCategory>('all');
	let addrSearchQuery = $state('');
	let showAllAddrs = $state(false);

	function getAddrScope(addr: string): 'public' | 'lan' | 'local' | 'link-local' {
		if (addr.includes('127.0.0.1') || addr.includes('::1')) return 'local';
		if (addr.includes('169.254.')) return 'link-local';
		if (
			addr.includes('192.168.') ||
			addr.includes('10.') ||
			addr.includes('172.16.') ||
			addr.includes('172.17.') ||
			addr.includes('172.18.') ||
			addr.includes('172.19.') ||
			addr.includes('172.20.') ||
			addr.includes('172.31.')
		) return 'lan';
		return 'public';
	}

	function getTransportBadge(addr: string): { label: string; color: string } {
		if (addr.includes('p2p-circuit')) return { label: 'Circuit Relay', color: 'bg-purple-950/60 border-purple-800/40 text-purple-400' };
		if (addr.includes('quic')) return { label: 'QUIC', color: 'bg-cyan-950/60 border-cyan-800/40 text-cyan-400' };
		if (addr.includes('ws')) return { label: 'WebSocket', color: 'bg-amber-950/60 border-amber-800/40 text-amber-400' };
		if (addr.includes('tcp')) return { label: 'TCP', color: 'bg-emerald-950/60 border-emerald-800/40 text-emerald-400' };
		return { label: 'Custom', color: 'bg-slate-900 border-slate-800 text-slate-400' };
	}

	let filteredAddrs = $derived.by(() => {
		const addrs = data?.NodeInfo?.Addrs || [];
		return addrs.filter(addr => {
			const scope = getAddrScope(addr);
			const matchesCategory = 
				activeAddrCategory === 'all' || 
				(activeAddrCategory === 'public' && scope === 'public') ||
				(activeAddrCategory === 'lan' && scope === 'lan') ||
				(activeAddrCategory === 'local' && scope === 'local') ||
				(activeAddrCategory === 'link-local' && scope === 'link-local');
			const matchesSearch = !addrSearchQuery || addr.toLowerCase().includes(addrSearchQuery.toLowerCase());
			return matchesCategory && matchesSearch;
		});
	});

	let displayAddrs = $derived.by(() => {
		if (showAllAddrs || addrSearchQuery || activeAddrCategory !== 'all') {
			return filteredAddrs;
		}
		return filteredAddrs.slice(0, 8);
	});
</script>

<div class="flex flex-col gap-6">
	<!-- Page Header -->
	<div class="border-b border-slate-800 pb-4 flex flex-col sm:flex-row sm:items-center justify-between gap-4">
		<div>
			<h1 class="font-display text-2xl text-slate-50">Local Daemon Node Parameters</h1>
			<p class="text-xs text-slate-500 mt-1">Host node keys, listener network bindings, and publisher keyring records</p>
		</div>
		{#if data}
			<div class="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg bg-slate-900 border border-slate-800 text-xs font-mono text-slate-300">
				<span class="w-2 h-2 rounded-full bg-emerald-400 animate-pulse"></span>
				Daemon v{data.NodeInfo.Version}
			</div>
		{/if}
	</div>

	{#if loading && !data}
		<div class="grid grid-cols-1 md:grid-cols-2 gap-6">
			{#each Array(2) as _}
				<div class="bg-slate-900 border border-slate-800/80 rounded-2xl p-6 flex flex-col gap-4">
					<Skeleton width="12rem" height="0.9rem" />
					<div class="border-t border-slate-800 pt-4 flex flex-col gap-3">
						{#each Array(4) as _}
							<div class="grid grid-cols-3 gap-3 items-center">
								<Skeleton width="5rem" height="0.7rem" />
								<div class="col-span-2"><Skeleton width="85%" height="0.7rem" /></div>
							</div>
						{/each}
					</div>
				</div>
			{/each}
			<div class="bg-slate-900 border border-slate-800/80 rounded-2xl p-6 flex flex-col gap-4 md:col-span-2">
				<Skeleton width="14rem" height="0.9rem" />
				<div class="border-t border-slate-800 pt-4 flex flex-col gap-3">
					{#each Array(3) as _}
						<Skeleton width="100%" height="1.4rem" rounded="rounded-md" />
					{/each}
				</div>
			</div>
		</div>
	{:else if error}
		<div class="bg-red-950/20 border border-red-800/40 text-red-400 p-4 rounded-xl text-xs font-mono">
			{error}
		</div>
	{:else if data}
		<div class="grid grid-cols-1 md:grid-cols-2 gap-6">
			<!-- Identity Card -->
			<div class="bg-slate-900 border border-slate-800/80 rounded-2xl p-6 flex flex-col gap-4 shadow-lg">
				<h3 class="font-bold text-sm text-slate-400 font-mono border-b border-slate-800 pb-2 flex items-center justify-between">
					<span>Identity & Credentials</span>
					<Icon icon="ph:fingerprint-bold" class="text-slate-500 text-base" />
				</h3>
				<dl class="grid grid-cols-3 gap-y-3 text-xs leading-relaxed">
					<dt class="text-slate-500 font-mono">Peer ID</dt>
					<dd class="col-span-2 font-mono text-slate-300 break-all select-all flex items-center gap-1.5">
						<span>{data.NodeInfo.PeerID}</span>
						<button 
							onclick={() => copyToClipboard(data!.NodeInfo.PeerID, 'peerid')}
							class="text-[10px] text-cyan-400 hover:text-cyan-300 font-bold hover:underline shrink-0 ml-auto"
						>
							{copiedId === 'peerid' ? 'Copied ✓' : 'Copy'}
						</button>
					</dd>

					<dt class="text-slate-500 font-mono">Daemon Version</dt>
					<dd class="col-span-2 text-slate-300 font-mono">{data.NodeInfo.Version}</dd>

					<dt class="text-slate-500 font-mono">Build Target</dt>
					<dd class="col-span-2 text-slate-300 font-mono uppercase">{data.NodeInfo.Build}</dd>

					<dt class="text-slate-500 font-mono">Anchor Engine</dt>
					<dd class="col-span-2">
						<span class={`font-bold px-2 py-0.5 rounded text-[10px] ${data.NodeInfo.AnchorMode ? 'bg-emerald-950/60 border border-emerald-800/40 text-emerald-400' : 'bg-slate-950 border border-slate-850 text-slate-500'}`}>
							{data.NodeInfo.AnchorMode ? 'ACTIVE (SYNC ALL)' : 'INACTIVE (STANDARD)'}
						</span>
					</dd>
				</dl>
			</div>

			<!-- Storage Metrics -->
			<div class="bg-slate-900 border border-slate-800/80 rounded-2xl p-6 flex flex-col gap-4 justify-between shadow-lg">
				<div>
					<h3 class="font-bold text-sm text-slate-400 font-mono border-b border-slate-800 pb-2 flex items-center justify-between">
						<span>Storage Footprint</span>
						<Icon icon="ph:database-bold" class="text-slate-500 text-base" />
					</h3>
					<dl class="grid grid-cols-3 gap-y-3 text-xs leading-relaxed mt-4">
						<dt class="text-slate-500 font-mono">Database size</dt>
						<dd class="col-span-2 text-slate-200 font-bold font-mono">
							{formatBytes(data.StoreBytes)} <span class="text-slate-500 text-[10px] font-normal font-sans">({data.StoreBytes} bytes)</span>
						</dd>

						<dt class="text-slate-500 font-mono">Pinned Roots</dt>
						<dd class="col-span-2 text-slate-200 font-mono">{data.SealedCount} sealed Content IDs</dd>
					</dl>
				</div>

				<div class="mt-4 pt-4 border-t border-slate-800">
					<button
						onclick={triggerFlashNode}
						disabled={flashing || flashSuccess}
						class="w-full flex items-center justify-center gap-2 px-4 py-2.5 text-xs font-semibold rounded-xl border transition-all duration-300
							{flashSuccess 
								? 'bg-emerald-500/20 border-emerald-500/40 text-emerald-400' 
								: flashing 
									? 'bg-red-500/10 border-red-500/20 text-red-400 cursor-not-allowed animate-pulse' 
									: 'bg-red-950/20 hover:bg-red-950/50 border-red-800/40 hover:border-red-600 text-red-400 hover:text-red-300'}"
					>
						<Icon icon={flashSuccess ? 'ph:check-bold' : flashing ? 'eos-icons:loading' : 'ph:fire'} class="text-sm" />
						{flashSuccess ? 'Node Flashed Successfully' : flashing ? 'Flashing Database...' : 'Flash Node (Wipe Database)'}
					</button>
				</div>
			</div>

			<!-- Listen Interfaces & Multiaddresses -->
			<div class="bg-slate-900 border border-slate-800 rounded-xl p-6 md:col-span-2 flex flex-col gap-5 shadow-xl">
				<div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-800 pb-3">
					<div class="flex items-center gap-2">
						<Icon icon="ph:broadcast-bold" class="text-cyan-400 text-base" />
						<h3 class="font-bold text-sm text-slate-300">
							Listen Interfaces & Multiaddresses
						</h3>
						<span class="px-2 py-0.5 rounded-full bg-slate-800 text-[10px] font-mono text-slate-400">
							{data.NodeInfo.Addrs ? data.NodeInfo.Addrs.length : 0} bindings
						</span>
					</div>

					<div class="relative">
						<input
							type="text"
							bind:value={addrSearchQuery}
							placeholder="Filter address..."
							class="bg-slate-950/80 border border-slate-800 text-slate-200 text-xs px-3 py-1 rounded-lg focus:outline-none focus:border-cyan-500/80 font-mono w-full sm:w-48"
						/>
					</div>
				</div>

				<!-- Category Filter Tabs -->
				<div class="flex flex-wrap items-center gap-1.5 border-b border-slate-850 pb-3 text-xs">
					<button
						onclick={() => activeAddrCategory = 'all'}
						class={`px-3 py-1 rounded-lg font-semibold transition-colors ${activeAddrCategory === 'all' ? 'bg-cyan-500/20 text-cyan-400 border border-cyan-500/40' : 'text-slate-400 hover:text-slate-200 bg-slate-950/40 border border-slate-800'}`}
					>
						All ({data.NodeInfo.Addrs ? data.NodeInfo.Addrs.length : 0})
					</button>
					<button
						onclick={() => activeAddrCategory = 'public'}
						class={`px-3 py-1 rounded-lg font-semibold transition-colors ${activeAddrCategory === 'public' ? 'bg-cyan-500/20 text-cyan-400 border border-cyan-500/40' : 'text-slate-400 hover:text-slate-200 bg-slate-950/40 border border-slate-800'}`}
					>
						Public / Relay
					</button>
					<button
						onclick={() => activeAddrCategory = 'lan'}
						class={`px-3 py-1 rounded-lg font-semibold transition-colors ${activeAddrCategory === 'lan' ? 'bg-cyan-500/20 text-cyan-400 border border-cyan-500/40' : 'text-slate-400 hover:text-slate-200 bg-slate-950/40 border border-slate-800'}`}
					>
						LAN
					</button>
					<button
						onclick={() => activeAddrCategory = 'local'}
						class={`px-3 py-1 rounded-lg font-semibold transition-colors ${activeAddrCategory === 'local' ? 'bg-cyan-500/20 text-cyan-400 border border-cyan-500/40' : 'text-slate-400 hover:text-slate-200 bg-slate-950/40 border border-slate-800'}`}
					>
						Localhost
					</button>
					<button
						onclick={() => activeAddrCategory = 'link-local'}
						class={`px-3 py-1 rounded-lg font-semibold transition-colors ${activeAddrCategory === 'link-local' ? 'bg-cyan-500/20 text-cyan-400 border border-cyan-500/40' : 'text-slate-400 hover:text-slate-200 bg-slate-950/40 border border-slate-800'}`}
					>
						Link-Local (Auto-IP)
					</button>
				</div>

				{#if displayAddrs && displayAddrs.length > 0}
					<ul class="flex flex-col gap-2">
						{#each displayAddrs as addr}
							{@const badge = getTransportBadge(addr)}
							{@const scope = getAddrScope(addr)}
							<li class="bg-slate-950/60 border border-slate-800 px-4 py-2.5 rounded-lg font-mono text-xs text-slate-300 flex flex-col sm:flex-row sm:items-center justify-between gap-2 group hover:border-slate-700 transition-colors">
								<div class="flex items-center gap-2 min-w-0">
									<span class={`px-2 py-0.5 rounded text-[9px] font-bold uppercase shrink-0 border ${badge.color}`}>
										{badge.label}
									</span>
									<span class="select-all break-all text-slate-200">{addr}</span>
								</div>
								<button 
									onclick={() => copyToClipboard(addr, addr)}
									class="shrink-0 self-end sm:self-auto text-[10px] px-2.5 py-1 rounded bg-slate-850 hover:bg-slate-800 text-cyan-400 border border-slate-750 font-bold transition-all active:scale-[0.98]"
								>
									{copiedId === addr ? 'Copied ✓' : 'Copy'}
								</button>
							</li>
						{/each}
					</ul>

					{#if filteredAddrs.length > 8 && !addrSearchQuery && activeAddrCategory === 'all'}
						<button
							onclick={() => showAllAddrs = !showAllAddrs}
							class="w-full py-2.5 text-center text-xs font-bold font-mono text-cyan-400 hover:text-cyan-300 bg-slate-950/40 hover:bg-slate-950 border border-slate-800 rounded-lg transition-colors flex items-center justify-center gap-1"
						>
							<Icon icon={showAllAddrs ? 'ph:caret-up-bold' : 'ph:caret-down-bold'} class="text-sm" />
							{showAllAddrs ? 'Collapse List' : `Show all ${filteredAddrs.length} listener interfaces`}
						</button>
					{/if}
				{:else}
					<div class="text-slate-500 italic text-xs py-6 text-center bg-slate-950/40 border border-slate-850 rounded-lg">
						No active listener interfaces match the current filter.
					</div>
				{/if}
			</div>

			<!-- Cryptographic KeyRing -->
			<div class="bg-slate-900 border border-slate-800 rounded-xl overflow-hidden md:col-span-2">
				<div class="px-6 py-4 bg-slate-950/40 border-b border-slate-800 flex items-center justify-between">
					<h3 class="font-bold text-sm text-slate-300">Cryptographic KeyRing</h3>
					<span class="px-2.5 py-0.5 rounded bg-slate-800 text-xs font-mono text-slate-400">
						{data.Keys ? data.Keys.length : 0} identity key{data.Keys && data.Keys.length === 1 ? '' : 's'}
					</span>
				</div>

				{#if data.Keys && data.Keys.length > 0}
					<div class="overflow-x-auto">
						<table class="w-full text-left border-collapse text-sm">
							<thead>
								<tr class="border-b border-slate-800/60 text-slate-500 font-mono text-xs uppercase bg-slate-950/20">
									<th class="py-3 px-6 font-semibold">Key Name</th>
									<th class="py-3 px-6 font-semibold">MemNS Domain</th>
									<th class="py-3 px-6 font-semibold w-24">Key Type</th>
									<th class="py-3 px-6 font-semibold w-48 text-right">Created At</th>
								</tr>
							</thead>
							<tbody class="divide-y divide-slate-800/40">
							{#each data.Keys as key}
								<tr class="hover:bg-slate-800/25 transition-colors">
									<!-- Key Name -->
									<td class="py-3.5 px-6 font-bold text-slate-200 font-mono text-xs">{key.name}</td>
									
									<!-- MemNS Domain -->
									<td class="py-3.5 px-6 font-mono text-xs">
										{#if key.memns_name}
											<a 
												href={`${base}/memns/${key.memns_name.replace('/memns/', '')}`} 
												class="text-cyan-400 hover:underline hover:text-cyan-300"
											>
												{key.memns_name}
											</a>
										{:else}
											<span class="text-slate-600 italic">No bound domain</span>
										{/if}
									</td>

									<!-- Type -->
									<td class="py-3.5 px-6 text-slate-400 font-mono text-xs">{key.type}</td>

									<!-- Created At -->
									<td class="py-3.5 px-6 text-slate-500 text-right font-mono text-xs">
										{formatDate(key.created_at)}
									</td>
								</tr>
							{/each}
							</tbody>
						</table>
					</div>
				{:else}
					<div class="py-12 text-center text-slate-500 flex flex-col items-center justify-center gap-2">
						<Icon icon="ph:key" class="text-2xl text-slate-400" />
						<div class="text-xs font-semibold text-slate-400">Keyring is Empty</div>
						<p class="text-[11px] text-slate-600 max-w-xs">Generate a local key pair using `membuss-cli keyring generate` to anchor name records.</p>
					</div>
				{/if}
			</div>
		</div>
	{/if}
</div>

{#if showConfirmModal}
	<div class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm">
		<div class="bg-slate-900 border border-slate-800 rounded-2xl p-6 max-w-md w-full flex flex-col gap-4 shadow-2xl animate-in fade-in zoom-in-95 duration-200">
			<h3 class="font-bold text-base text-slate-100 flex items-center gap-2">
				<Icon icon="ph:warning-bold" class="text-red-500 text-lg" />
				{#if confirmStep === 1}
					Wipe Node Database
				{:else}
					Final Confirmation Required
				{/if}
			</h3>
			<p class="text-xs text-slate-400 leading-relaxed whitespace-pre-line">
				{#if confirmStep === 1}
					🚨 WARNING: You are about to initiate FLASHNODE.
					This will completely WIPE the BadgerDB database, clearing all content, pins, cached data, and indices.
					This action is PERMANENT and CANNOT BE UNDONE.
					Are you sure you want to proceed?
				{:else}
					⚠️ FINAL CONFIRMATION:
					Are you absolutely sure you want to delete ALL data and reset this node?
				{/if}
			</p>
			<div class="flex items-center justify-end gap-3 mt-2">
				<button 
					onclick={() => showConfirmModal = false} 
					class="px-4 py-2 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 text-xs font-semibold transition-colors"
				>
					Cancel
				</button>
				<button 
					onclick={proceedFlashNode} 
					class="px-4 py-2 rounded-xl bg-red-650 hover:bg-red-700 text-slate-50 text-xs font-semibold transition-colors animate-pulse"
				>
					{#if confirmStep === 1}
						Proceed
					{:else}
						Yes, Wipe Everything
					{/if}
				</button>
			</div>
		</div>
	</div>
{/if}
