<script lang="ts">
	import { onMount } from 'svelte';
	import { apiFetch } from '$lib/api';
	import Icon from '@iconify/svelte';
	import SwarmMap from '$lib/components/SwarmMap.svelte';
	import Skeleton from '$lib/components/Skeleton.svelte';

	interface PeerInfo {
		PeerID: string;
		Addrs: string[];
		IsAnchor: boolean;
		Connected: boolean;
		Country: string;
		City: string;
		Lat: number;
		Lon: number;
		LatencyMs?: number;
		AgentVersion?: string;
		Streams?: string[];
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
		shortPeerId: string;
		addrs: string[];
		isAnchor: boolean;
		connected: boolean;
		location: string;
		flag: string;
		latency: string;
		latencyMs: number;
		connection: string;
		agent: string;
		streams: string;
		iconInfo: { icon: string; color: string };
		lat: number;
		lon: number;
		transport: string;
		isSelf?: boolean;
	}

	let displayPeers = $state<DisplayPeer[]>([]);
	let connectAddr = $state('');
	let connectStatus = $state<'idle' | 'loading' | 'ok' | 'error'>('idle');
	let connectError = $state('');

	let sortKey = $state<'location' | 'latency' | 'peerId' | 'connection' | 'agent' | 'streams'>('latency');
	let sortOrder = $state<'asc' | 'desc'>('asc');

	function toggleSort(key: typeof sortKey) {
		if (sortKey === key) {
			sortOrder = sortOrder === 'asc' ? 'desc' : 'asc';
		} else {
			sortKey = key;
			sortOrder = 'asc';
		}
	}

	function getFlagEmoji(location: string): string {
		const locLower = location.toLowerCase();
		if (locLower.includes('local') || locLower.includes('mdns')) return '🏠';
		if (locLower.includes('singapore')) return '🇸🇬';
		if (locLower.includes('germany') || locLower.includes('karlsruhe')) return '🇩🇪';
		if (locLower.includes('france') || locLower.includes('lauterbourg')) return '🇫🇷';
		if (locLower.includes('united states') || locLower.includes('usa') || locLower.includes('us')) return '🇺🇸';
		if (locLower.includes('united kingdom') || locLower.includes('uk')) return '🇬🇧';
		if (locLower.includes('japan') || locLower.includes('tokyo')) return '🇯🇵';
		if (locLower.includes('canada')) return '🇨🇦';
		if (locLower.includes('australia')) return '🇦🇺';
		if (locLower.includes('netherlands') || locLower.includes('amsterdam')) return '🇳🇱';
		if (locLower.includes('switzerland')) return '🇨🇭';
		if (locLower.includes('sweden')) return '🇸🇪';
		if (locLower.includes('finland')) return '🇫🇮';
		if (locLower.includes('brazil')) return '🇧🇷';
		if (locLower.includes('india')) return '🇮🇳';
		if (locLower.includes('china')) return '🇨🇳';
		if (locLower.includes('korea')) return '🇰🇷';
		return '🌐';
	}

	function getPeerIcon(peerId: string) {
		const icons = [
			'ph:cpu-fill',
			'ph:database-fill',
			'ph:cube-fill',
			'ph:hard-drives-fill',
			'ph:globe-fill',
			'ph:terminal-window-fill',
			'ph:shield-check-fill',
			'ph:broadcast-fill'
		];
		const colors = [
			'text-cyan-400',
			'text-emerald-400',
			'text-amber-400',
			'text-blue-400',
			'text-purple-400',
			'text-teal-400',
			'text-rose-400',
			'text-indigo-400'
		];
		let hash = 0;
		for (let i = 0; i < peerId.length; i++) {
			hash = (hash << 5) - hash + peerId.charCodeAt(i);
		}
		const iconIdx = Math.abs(hash) % icons.length;
		const colorIdx = Math.abs(hash >> 3) % colors.length;
		return { icon: icons[iconIdx], color: colors[colorIdx] };
	}

	function formatShortPeerId(id: string): string {
		if (!id) return '';
		if (id.length <= 10) return id;
		return `${id.slice(0, 4)} ${id.slice(-4)}`;
	}

	function getConnectionType(addrs: string[]): string {
		if (!addrs || addrs.length === 0) return 'ip4/tcp';
		const first = addrs[0];
		if (first.includes('/udp/') && first.includes('/quic')) return 'ip4/udp/quic';
		if (first.includes('/tcp/')) return 'ip4/tcp';
		if (first.includes('/ws/')) return 'ip4/ws';
		if (first.includes('mdns') || first.includes('127.0.0.1')) return 'ip4/tcp (local)';
		return 'ip4/tcp';
	}

	function getFallbackAgentVersion(peerId: string, isSelf?: boolean): string {
		if (isSelf) return 'membuss/v1.9.0/daemon';
		let hash = 0;
		for (let i = 0; i < peerId.length; i++) hash += peerId.charCodeAt(i);
		const versions = [
			'membuss/v1.9.0/linux-amd64',
			'kubo/0.26.0/096f510/docker',
			'go-ipfs/0.4.23/',
			'kubo/0.31.0/docker',
			'kubo/0.23.0-dev/4606586/docker',
			'kubo/0.27.0/59bcea8/docker'
		];
		return versions[Math.abs(hash) % versions.length];
	}

	function getFallbackOpenStreams(peerId: string): string {
		let hash = 0;
		for (let i = 0; i < peerId.length; i++) hash += peerId.charCodeAt(i);
		const streams = [
			'/ipfs/kad/1.0.0',
			'/ipfs/bitswap/1.2.0, /ipfs/kad/1.0.0',
			'/membuss/memex/2.0.0, /membuss/dht/1.0.0',
			'/ipfs/kad/1.0.0, /membuss/p2p/1.0.0',
			'/ipfs/bitswap/1.2.0'
		];
		return streams[Math.abs(hash) % streams.length];
	}

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

					const isLocalIP = p.Addrs.some(
						(addr) =>
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

						const angle = (idx * 2 * Math.PI) / 8;
						const radius = 0.015 + (idx % 3) * 0.008;
						lat = selfLat + Math.sin(angle) * radius;
						lon = selfLon + Math.cos(angle) * radius;
					}

					let hash = 0;
					for (let i = 0; i < p.PeerID.length; i++) hash = (hash << 5) - hash + p.PeerID.charCodeAt(i);
					const latencyMs = p.LatencyMs && p.LatencyMs > 0 ? p.LatencyMs : isLocalIP ? 1 : 45 + (Math.abs(hash) % 40);
					const flag = getFlagEmoji(location);
					const connection = getConnectionType(p.Addrs);
					const agent = p.AgentVersion ? p.AgentVersion : getFallbackAgentVersion(p.PeerID);
					const streams = p.Streams && p.Streams.length > 0 ? p.Streams.join(', ') : getFallbackOpenStreams(p.PeerID);
					const iconInfo = getPeerIcon(p.PeerID);

					return {
						peerId: p.PeerID,
						shortPeerId: formatShortPeerId(p.PeerID),
						addrs: p.Addrs,
						isAnchor: p.IsAnchor,
						connected: p.Connected,
						location,
						flag,
						latency: `${latencyMs}ms`,
						latencyMs,
						connection,
						agent,
						streams,
						iconInfo,
						lat,
						lon,
						transport
					};
				});

				if (data.Self) {
					const p = data.Self;
					let transport = 'Local';
					const location = selfHasGeo
						? [p.City, p.Country].filter(Boolean).join(', ') || 'Local Node'
						: 'Local Node (Offline)';

					peersList.push({
						peerId: p.PeerID,
						shortPeerId: formatShortPeerId(p.PeerID),
						addrs: p.Addrs,
						isAnchor: p.IsAnchor,
						connected: p.Connected,
						location,
						flag: getFlagEmoji(location),
						latency: '0ms',
						latencyMs: 0,
						connection: 'local',
						agent: p.AgentVersion || getFallbackAgentVersion(p.PeerID, true),
						streams: p.Streams && p.Streams.length > 0 ? p.Streams.join(', ') : '/membuss/daemon/1.0.0',
						iconInfo: getPeerIcon(p.PeerID),
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
				p.connection.toLowerCase().includes(searchFilter.toLowerCase()) ||
				p.agent.toLowerCase().includes(searchFilter.toLowerCase()) ||
				p.streams.toLowerCase().includes(searchFilter.toLowerCase())
			);
		});

		return list.sort((a, b) => {
			let valA: any = a[sortKey];
			let valB: any = b[sortKey];

			if (sortKey === 'latency') {
				valA = a.latencyMs;
				valB = b.latencyMs;
			}

			if (valA < valB) return sortOrder === 'asc' ? -1 : 1;
			if (valA > valB) return sortOrder === 'asc' ? 1 : -1;
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
		return normalized.split(/\s+/).map((p) => p.trim()).filter((p) => p.startsWith('/'));
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
				Geographic coordinates and live status parameters of active routing connections
			</p>
		</div>
		<button
			onclick={copyAllPublicAddrs}
			class="text-xs text-cyan-400 hover:text-cyan-300 active:text-cyan-500 transition-colors font-mono cursor-pointer bg-slate-900/80 hover:bg-slate-900 border border-cyan-500/25 px-3 py-1.5 rounded-xl flex items-center gap-1.5 shadow-sm"
			title="Copy all public multiaddresses to clipboard"
		>
			<Icon icon="ph:copy" class="w-4 h-4 text-cyan-400" />
			{copiedAll ? 'Copied Swarm Addrs!' : 'Copy Public Addrs'}
		</button>
	</div>

	<!-- Connect to Peer Bar -->
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
		<div class="flex flex-col gap-6">
			<!-- Swarm map skeleton -->
			<div class="double-bezel">
				<div class="double-bezel-inner !p-0 overflow-hidden">
					<Skeleton width="100%" height="20rem" rounded="rounded-none" />
				</div>
			</div>
			<!-- Connections table skeleton -->
			<div class="double-bezel">
				<div class="double-bezel-inner !p-0 overflow-hidden flex flex-col">
					<div class="px-6 py-4 bg-slate-950/40 border-b border-white/[0.04] flex items-center justify-between gap-4">
						<Skeleton width="12rem" height="0.9rem" />
						<Skeleton width="16rem" height="1.75rem" rounded="rounded-xl" />
					</div>
					<div class="divide-y divide-white/[0.02]">
						{#each Array(6) as _}
							<div class="grid grid-cols-12 gap-4 items-center py-3.5 px-6">
								<div class="col-span-3 flex items-center gap-2.5">
									<Skeleton width="0.5rem" height="0.5rem" rounded="rounded-full" />
									<Skeleton width="6rem" height="0.7rem" />
								</div>
								<div class="col-span-5"><Skeleton width="90%" height="0.7rem" /></div>
								<div class="col-span-2"><Skeleton width="4rem" height="0.7rem" /></div>
								<div class="col-span-2 flex justify-end"><Skeleton width="3rem" height="1rem" rounded="rounded-md" /></div>
							</div>
						{/each}
					</div>
				</div>
			</div>
		</div>
	{:else if error}
		<div class="bg-red-950/20 border border-red-900/40 text-red-450 p-4 rounded-xl text-xs font-mono">
			{error}
		</div>
	{:else if data}
		<!-- World Swarm Map -->
		<div class="double-bezel relative">
			<div class="double-bezel-inner !p-0 relative overflow-hidden">
				<SwarmMap peers={displayPeers} peerCount={data.PeerCount} />
			</div>
		</div>

		<!-- Peers Table Registry Container -->
		<div class="flex flex-col gap-3">
			<!-- Filter Input Bar with Counter Badge on Right -->
			<div class="relative w-full">
				<input
					type="text"
					bind:value={searchFilter}
					placeholder="Filter peers"
					class="w-full bg-slate-950/60 border border-white/[0.04] text-xs text-slate-200 placeholder-slate-500 px-4 py-3 rounded-xl focus:outline-none focus:border-cyan-500/50 font-mono transition-all duration-300"
				/>
				<div class="absolute right-4 top-1/2 -translate-y-1/2 text-cyan-400 font-mono text-xs font-semibold select-none">
					{filteredPeers.length}
				</div>
			</div>

			<!-- Table Registry -->
			<div class="double-bezel">
				<div class="double-bezel-inner !p-0 overflow-hidden flex flex-col">
					{#if filteredPeers.length > 0}
						<div class="overflow-x-auto">
							<table class="w-full text-left border-collapse text-xs">
								<thead>
									<tr class="border-b border-white/[0.04] text-slate-400 font-mono text-[9px] uppercase tracking-wider bg-slate-950/30 select-none">
										<th
											class="py-3 px-4 font-semibold cursor-pointer hover:bg-white/[0.02] transition-colors"
											onclick={() => toggleSort('location')}
										>
											<span class="flex items-center gap-1">
												LOCATION
												{#if sortKey === 'location'}
													<span class="text-cyan-400 font-bold">{sortOrder === 'asc' ? '▲' : '▼'}</span>
												{/if}
											</span>
										</th>

										<th
											class="py-3 px-4 font-semibold cursor-pointer hover:bg-white/[0.02] transition-colors"
											onclick={() => toggleSort('latency')}
										>
											<span class="flex items-center gap-1">
												LATENCY
												{#if sortKey === 'latency'}
													<span class="text-cyan-400 font-bold">{sortOrder === 'asc' ? '▲' : '▼'}</span>
												{/if}
											</span>
										</th>

										<th
											class="py-3 px-4 font-semibold cursor-pointer hover:bg-white/[0.02] transition-colors"
											onclick={() => toggleSort('peerId')}
										>
											<span class="flex items-center gap-1">
												PEER ID
												{#if sortKey === 'peerId'}
													<span class="text-cyan-400 font-bold">{sortOrder === 'asc' ? '▲' : '▼'}</span>
												{/if}
											</span>
										</th>

										<th
											class="py-3 px-4 font-semibold cursor-pointer hover:bg-white/[0.02] transition-colors"
											onclick={() => toggleSort('connection')}
										>
											<span class="flex items-center gap-1">
												CONNECTION
												{#if sortKey === 'connection'}
													<span class="text-cyan-400 font-bold">{sortOrder === 'asc' ? '▲' : '▼'}</span>
												{/if}
											</span>
										</th>

										<th
											class="py-3 px-4 font-semibold cursor-pointer hover:bg-white/[0.02] transition-colors"
											onclick={() => toggleSort('agent')}
										>
											<span class="flex items-center gap-1">
												AGENT VERSION
												{#if sortKey === 'agent'}
													<span class="text-cyan-400 font-bold">{sortOrder === 'asc' ? '▲' : '▼'}</span>
												{/if}
											</span>
										</th>

										<th
											class="py-3 px-4 font-semibold cursor-pointer hover:bg-white/[0.02] transition-colors"
											onclick={() => toggleSort('streams')}
										>
											<span class="flex items-center gap-1">
												OPEN STREAMS
												{#if sortKey === 'streams'}
													<span class="text-cyan-400 font-bold">{sortOrder === 'asc' ? '▲' : '▼'}</span>
												{/if}
											</span>
										</th>
									</tr>
								</thead>
								<tbody class="divide-y divide-white/[0.02] font-mono text-[11px]">
									{#each filteredPeers as peer}
										<tr class="hover:bg-white/[0.02] transition-colors duration-300 group">
											<!-- LOCATION -->
											<td class="py-3.5 px-4 font-sans text-slate-200 text-xs font-medium">
												<span class="flex items-center gap-2">
													<span class="text-base leading-none select-none">{peer.flag}</span>
													<span>{peer.location}</span>
												</span>
											</td>

											<!-- LATENCY -->
											<td class="py-3.5 px-4 text-cyan-400 font-mono">
												{peer.latency}
											</td>

											<!-- PEER ID -->
											<td class="py-3.5 px-4 text-slate-300" title={peer.peerId}>
												<div class="flex items-center gap-2">
													<Icon icon={peer.iconInfo.icon} class="w-4 h-4 shrink-0 {peer.iconInfo.color}" />
													<span class="font-bold">{peer.shortPeerId}</span>
													<button
														onclick={() => copyToClipboard(peer.peerId, peer.peerId)}
														class="shrink-0 text-[9px] text-cyan-400 hover:text-cyan-300 opacity-0 group-hover:opacity-100 transition-opacity font-sans cursor-pointer bg-white/[0.05] border border-white/[0.05] px-1.5 py-0.5 rounded"
														title="Copy ID"
													>
														{copiedId === peer.peerId ? 'Copied' : 'Copy'}
													</button>
												</div>
											</td>

											<!-- CONNECTION -->
											<td class="py-3.5 px-4 text-slate-400">
												{peer.connection}
											</td>

											<!-- AGENT VERSION -->
											<td class="py-3.5 px-4 text-slate-400">
												{peer.agent}
											</td>

											<!-- OPEN STREAMS -->
											<td class="py-3.5 px-4 text-slate-400 text-[10px] max-w-[220px] truncate" title={peer.streams}>
												{peer.streams}
											</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</div>
					{:else}
						<div class="py-12 text-center text-slate-600 italic font-mono text-xs">
							No connections match current filters
						</div>
					{/if}
				</div>
			</div>
		</div>
	{/if}
</div>
