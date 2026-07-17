<script lang="ts">
	import { onMount } from 'svelte';
	import { apiFetch } from '$lib/api';
	import Icon from '@iconify/svelte';
	import SwarmMap from '$lib/components/SwarmMap.svelte';

	interface PeerInfo {
		PeerID: string;
		Addrs: string[];
		IsAnchor: boolean;
		Connected: boolean;
		Country: string;
		City: string;
		Lat: number;
		Lon: number;
	}

	interface PeersData {
		Title: string;
		PeerCount: number;
		Peers: PeerInfo[];
		Self?: PeerInfo;
	}

	let data = $state<PeersData | null>(null);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let copiedId = $state<string | null>(null);
	let searchFilter = $state('');

	interface DisplayPeer {
		peerId: string;
		addrs: string[];
		isAnchor: boolean;
		connected: boolean;
		location: string;
		lat: number;
		lon: number;
		transport: string;
		isSelf?: boolean;
	}

	let displayPeers = $state<DisplayPeer[]>([]);
	let connectAddr = $state('');
	let connectStatus = $state<'idle' | 'loading' | 'ok' | 'error'>('idle');
	let connectError = $state('');

	async function loadPeers() {
		try {
			const res = await apiFetch('/peers');
			data = res;

			if (data && data.Peers) {
				let selfLat = 23.81;
				let selfLon = 90.41;
				let selfHasGeo = false;

				if (data.Self && data.Self.Lat !== 0) {
					selfLat = data.Self.Lat;
					selfLon = data.Self.Lon;
					selfHasGeo = true;
				}

				const peersList: DisplayPeer[] = data.Peers.map((p, idx) => {
					let transport = 'QUIC (UDP)';
					if (p.Addrs && p.Addrs.length > 0) {
						if (p.Addrs[0].includes('/tcp/')) transport = 'TCP';
						else if (p.Addrs[0].includes('/ws/')) transport = 'WebSockets';
					}

					let location = [p.City, p.Country].filter(Boolean).join(', ') || 'Unknown';
					let lat = p.Lat;
					let lon = p.Lon;

					// Check if peer has local IP addresses (private ranges or localhost)
					const isLocalIP = p.Addrs.some(addr => 
						addr.includes('/ip4/192.168.') || 
						addr.includes('/ip4/10.') || 
						addr.includes('/ip4/172.16.') || 
						addr.includes('/ip4/172.17.') || 
						addr.includes('/ip4/172.18.') || 
						addr.includes('/ip4/172.19.') || 
						addr.includes('/ip4/172.2') || 
						addr.includes('/ip4/172.3') || 
						addr.includes('/ip4/127.0.0.1')
					);

					if ((lat === 0 && lon === 0) || isLocalIP) {
						location = 'Local Network (mDNS)';
						transport = 'mDNS (Local)';
						
						// Cluster local node slightly offset from self node position
						const angle = (idx * 2 * Math.PI) / 8;
						const radius = 0.015 + (idx % 3) * 0.008;
						lat = selfLat + Math.sin(angle) * radius;
						lon = selfLon + Math.cos(angle) * radius;
					}

					return {
						peerId: p.PeerID,
						addrs: p.Addrs,
						isAnchor: p.IsAnchor,
						connected: p.Connected,
						location,
						lat,
						lon,
						transport
					};
				});

				if (data.Self) {
					const p = data.Self;
					let transport = 'Local';
					const location = selfHasGeo
						? ([p.City, p.Country].filter(Boolean).join(', ') || 'Local Node')
						: 'Local Node (Offline)';
					peersList.push({
						peerId: p.PeerID,
						addrs: p.Addrs,
						isAnchor: p.IsAnchor,
						connected: p.Connected,
						location,
						lat: selfLat,
						lon: selfLon,
						transport,
						isSelf: true
					});
				}

				displayPeers = peersList;
			}
			loading = false;
		} catch (err) {
			error = err instanceof Error ? err.message : 'Failed to query peer swarm';
			loading = false;
		}
	}

	let filteredPeers = $derived.by(() => {
		const list = displayPeers.filter((p) => {
			return (
				!searchFilter ||
				p.peerId.toLowerCase().includes(searchFilter.toLowerCase()) ||
				p.location.toLowerCase().includes(searchFilter.toLowerCase()) ||
				p.transport.toLowerCase().includes(searchFilter.toLowerCase())
			);
		});

		// Sort self node to the very top, followed by standard nodes
		return list.sort((a, b) => {
			if (a.isSelf && !b.isSelf) return -1;
			if (!a.isSelf && b.isSelf) return 1;
			return 0;
		});
	});

	let copiedAll = $state(false);

	function copyAllPublicAddrs() {
		const publicAddrs: string[] = [];
		displayPeers.forEach((p) => {
			if (p.isSelf) return;
			if (!p.addrs) return;
			p.addrs.forEach((addr) => {
				const lower = addr.toLowerCase();
				const isPrivate = 
					lower.includes('/127.0.0.1') ||
					lower.includes('/::1') ||
					lower.includes('/localhost') ||
					lower.includes('/192.168.') ||
					lower.includes('/10.') ||
					lower.includes('/172.16.') ||
					lower.includes('/172.17.') ||
					lower.includes('/172.18.') ||
					lower.includes('/172.19.') ||
					lower.includes('/172.20.') ||
					lower.includes('/172.21.') ||
					lower.includes('/172.22.') ||
					lower.includes('/172.23.') ||
					lower.includes('/172.24.') ||
					lower.includes('/172.25.') ||
					lower.includes('/172.26.') ||
					lower.includes('/172.27.') ||
					lower.includes('/172.28.') ||
					lower.includes('/172.29.') ||
					lower.includes('/172.30.') ||
					lower.includes('/172.31.') ||
					lower.includes('/0.0.0.0') ||
					lower.includes('/fe80::') ||
					lower.includes('/p2p-circuit') ||
					lower.includes('mdns') ||
					lower.includes('local');
				
				if (!isPrivate) {
					let fullAddr = addr;
					if (!fullAddr.includes('/p2p/')) {
						if (fullAddr.endsWith('/')) {
							fullAddr = fullAddr.slice(0, -1);
						}
						fullAddr = `${fullAddr}/p2p/${p.peerId}`;
					}
					publicAddrs.push(fullAddr);
				}
			});
		});

		if (publicAddrs.length === 0) {
			alert('No public swarm addresses found.');
			return;
		}

		const text = publicAddrs.join(', ');
		navigator.clipboard.writeText(text).then(() => {
			copiedAll = true;
			setTimeout(() => {
				copiedAll = false;
			}, 2000);
		});
	}

	function parseMultiaddrs(input: string): string[] {
		let normalized = input.replace(/[,;\r\n]+/g, ' ');
		normalized = normalized.replace(/([^\s])(\/(ip4|ip6|dns|dns4|dns6|dnsaddr)\/)/g, '$1 $2');
		return normalized.split(/\s+/).map(p => p.trim()).filter(p => p.startsWith('/'));
	}

	function copyToClipboard(text: string, id: string) {
		navigator.clipboard.writeText(text).then(() => {
			copiedId = id;
			setTimeout(() => {
				if (copiedId === id) copiedId = null;
			}, 1500);
		});
	}

	async function connectToPeer() {
		const rawInput = connectAddr.trim();
		if (!rawInput) return;

		const parsedAddrs = parseMultiaddrs(rawInput);
		if (parsedAddrs.length === 0) {
			connectStatus = 'error';
			connectError = 'No valid multiaddress found (must start with /)';
			return;
		}

		connectStatus = 'loading';
		connectError = '';

		try {
			const data = await apiFetch('/peers/connect', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ multiaddrs: parsedAddrs })
			});
			if (data.ok) {
				if (data.errors && data.errors.length > 0) {
					connectStatus = 'ok';
					connectAddr = '';
					connectError = `Connected to ${data.success_count}/${parsedAddrs.length} peers. Errors: ${data.errors.join('; ')}`;
				} else {
					connectStatus = 'ok';
					connectAddr = '';
				}
				loadPeers();
			} else {
				connectStatus = 'error';
				connectError = data.error || 'Connection failed';
			}
		} catch (e) {
			connectStatus = 'error';
			connectError = e instanceof Error ? e.message : 'Request failed';
		}
	}

	onMount(() => {
		loadPeers();
		const interval = setInterval(loadPeers, 10000);
		return () => {
			clearInterval(interval);
		};
	});
</script>

<div class="flex flex-col gap-6 animate-fade-in-up" style="animation-delay: 0ms">
	<!-- Page Header -->
	<div class="border-b border-white/[0.04] pb-5 flex flex-col md:flex-row justify-between items-start md:items-end gap-4">
		<div>
			<span class="text-[9px] text-cyan-400 uppercase tracking-widest font-mono font-semibold">Mesh Swarm Explorer</span>
			<h1 class="font-display text-2xl text-slate-50 mt-1">Active Swarm Map</h1>
			<p class="text-xs text-slate-500 mt-1">
				Geographic coordinates and status parameters of active routing connections
			</p>
		</div>
	</div>

	<!-- Connect to Peer -->
	<div class="double-bezel">
		<div class="double-bezel-inner flex flex-col sm:flex-row gap-4 items-center">
			<input
				type="text"
				bind:value={connectAddr}
				placeholder="Paste peer multiaddr, e.g. /ip4/1.2.3.4/tcp/4001/p2p/12D3KooW..."
				class="w-full flex-1 bg-slate-950/60 border border-white/[0.04] text-xs px-4 py-2.5 rounded-xl focus:outline-none focus:border-cyan-500/50 font-mono transition-all duration-300"
				onkeydown={(e) => {
					if (e.key === 'Enter') connectToPeer();
				}}
			/>
			<button
				onclick={connectToPeer}
				disabled={connectStatus === 'loading' || !connectAddr.trim()}
				class="w-full sm:w-auto px-5 py-2.5 text-xs font-semibold rounded-xl transition-all duration-300 cursor-pointer
					{connectStatus === 'loading'
						? 'bg-slate-800 text-slate-500 cursor-wait'
						: 'bg-cyan-500 hover:bg-cyan-400 hover:scale-[1.02] active:scale-[0.98] text-slate-950 shadow-[0_4px_12px_rgba(232,163,61,0.18)]'}"
			>
				{connectStatus === 'loading' ? 'Connecting...' : 'Connect'}
			</button>
		</div>
	</div>
	{#if connectStatus === 'ok'}
		<div class="text-xs text-cyan-400 font-mono px-2 -mt-2">Peer connected successfully</div>
	{/if}
	{#if connectStatus === 'error'}
		<div class="text-xs text-red-400 font-mono px-2 -mt-2">Failed: {connectError}</div>
	{/if}

	{#if loading && !data}
		<div class="space-y-6 animate-pulse">
			<div class="h-60 bg-slate-900 rounded-lg w-full"></div>
			<div class="h-32 bg-slate-900 rounded-lg w-full"></div>
		</div>
	{:else if error}
		<div
			class="bg-red-950/20 border border-red-900/40 text-red-450 p-4 rounded-xl text-xs font-mono"
		>
			{error}
		</div>
	{:else if data}
		<!-- Custom SVG swarm topology — density heat, no external map tiles -->
		<div class="double-bezel relative">
			<div class="double-bezel-inner !p-0 relative overflow-hidden">
				<SwarmMap peers={displayPeers} peerCount={data.PeerCount} />
			</div>
		</div>

		<!-- Peers Table Registry -->
		<div class="double-bezel">
			<div class="double-bezel-inner !p-0 overflow-hidden flex flex-col">
				<div
					class="px-6 py-4 bg-slate-950/40 border-b border-white/[0.04] flex flex-col sm:flex-row sm:items-center justify-between gap-4"
				>
					<div class="flex items-center gap-3">
						<h3 class="font-bold text-sm text-slate-350 font-mono">Swarm Connections</h3>
						<button
							onclick={copyAllPublicAddrs}
							class="text-[10px] text-cyan-400 hover:text-cyan-300 active:text-cyan-550 transition-colors font-mono cursor-pointer bg-slate-900/60 hover:bg-slate-900/90 border border-cyan-500/20 px-2 py-0.5 rounded-lg flex items-center gap-1 shadow-sm"
							title="Copy all public multiaddresses to clipboard"
						>
							<Icon icon="ph:copy" class="w-3.5 h-3.5 text-cyan-400" />
							{copiedAll ? 'Copied Swarm!' : 'Copy Public Addrs'}
						</button>
					</div>
					<div class="relative w-full sm:w-64">
						<input
							type="text"
							bind:value={searchFilter}
							placeholder="Filter by country or ID..."
							class="w-full bg-slate-950/60 border border-white/[0.04] text-xs px-3.5 py-1.5 rounded-xl focus:outline-none focus:border-cyan-500/50"
						/>
					</div>
				</div>

				{#if filteredPeers.length > 0}
					<div class="overflow-x-auto">
						<table class="w-full text-left border-collapse text-xs">
							<thead>
								<tr
									class="border-b border-white/[0.04] text-slate-500 font-mono text-[9px] uppercase tracking-wider bg-slate-950/20"
								>
									<th class="py-3 px-6 font-semibold w-1/4">Location</th>
									<th class="py-3 px-6 font-semibold w-1/3">Peer ID</th>
									<th class="py-3 px-6 font-semibold w-32">Transport</th>
									<th class="py-3 px-6 font-semibold text-right">Anchor</th>
								</tr>
							</thead>
							<tbody class="divide-y divide-white/[0.02] font-mono text-[11px]">
								{#each filteredPeers as peer}
									<tr class="hover:bg-white/[0.02] transition-colors duration-300 group">
										<td class="py-3.5 px-6 font-sans text-slate-200 text-xs font-medium">
											<span class="flex items-center gap-2.5">
												<span
													class="h-2 w-2 rounded-full shrink-0"
													style="background: {peer.isSelf ? '#f4efe2' : peer.isAnchor ? '#57b79e' : '#e8a33d'}"
												></span>
												{peer.location}
											</span>
										</td>

										<td class="py-3.5 px-6 text-slate-400 max-w-[150px] md:max-w-[250px] truncate" title={peer.peerId}>
											<div class="flex items-center gap-2 min-w-0">
												<span class="truncate">{peer.peerId}</span>
												<button
													onclick={() => copyToClipboard(peer.peerId, peer.peerId)}
													class="shrink-0 text-[9px] text-cyan-400 hover:text-cyan-300 opacity-0 group-hover:opacity-100 transition-opacity font-sans cursor-pointer bg-white/[0.05] border border-white/[0.05] px-1.5 py-0.5 rounded"
													title="Copy ID"
												>
													{copiedId === peer.peerId ? 'Copied' : 'Copy'}
												</button>
											</div>
										</td>

										<td class="py-3.5 px-6 text-slate-400">{peer.transport}</td>

										<td class="py-3.5 px-6 text-right font-sans">
											{#if peer.isSelf}
												<span class="px-2 py-0.5 rounded text-[9px] font-bold font-mono bg-slate-800 text-slate-100 border border-white/[0.08] uppercase">self</span>
											{:else if peer.isAnchor}
												<span class="px-2 py-0.5 rounded text-[9px] font-bold font-mono bg-emerald-500/10 text-emerald-400 border border-emerald-500/25 uppercase">anchor</span>
											{:else}
												<span class="text-slate-650 font-mono text-xs">no</span>
											{/if}
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{:else}
					<div class="py-12 text-center text-slate-600 italic">
						No connections match current filters
					</div>
				{/if}
			</div>
		</div>
	{/if}
</div>

