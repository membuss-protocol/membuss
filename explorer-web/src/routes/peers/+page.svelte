<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { tick } from 'svelte';
	import { apiFetch } from '$lib/api';
	import { browser } from '$app/environment';
	import Icon from '@iconify/svelte';

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
	let mapEl = $state<HTMLDivElement>();
	let mapReady = $state(false);

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

	let L: any;
	let map: any;
	let markersLayer: any;

	function updateMapData() {
		if (!mapReady || !map || !L || !markersLayer) return;

		// Clear previous layers
		markersLayer.clearLayers();

		// Sort so the self node is processed first and gets true coordinates, and peers get offset
		const sortedPeers = [...displayPeers].sort((a, b) => {
			if (a.isSelf) return -1;
			if (b.isSelf) return 1;
			return 0;
		});

		// Deduplicate and jitter overlapping coordinates
		const coordCounts = new Map<string, number>();
		const validPeers = sortedPeers
			.filter((p) => p.lat !== 0 || p.lon !== 0)
			.map((p) => {
				const key = `${p.lat.toFixed(4)},${p.lon.toFixed(4)}`;
				const count = coordCounts.get(key) || 0;
				coordCounts.set(key, count + 1);

				let lat = p.lat;
				let lon = p.lon;

				if (count > 0) {
					// Deterministic radial offset around coordinate center
					const angle = (count * 2 * Math.PI) / 8;
					const jitterDist = 0.006 + Math.floor(count / 8) * 0.003;
					lat += Math.sin(angle) * jitterDist;
					lon += Math.cos(angle) * jitterDist;
				}

				return { ...p, lat, lon };
			});

		if (validPeers.length === 0) return;

		const leafletMarkers: any[] = [];

		validPeers.forEach((p) => {
			let iconClass = 'network-marker';
			let dotClass = 'marker-dot';
			let pulseClass = 'marker-pulse';

			if (p.isSelf) {
				iconClass = 'network-marker self';
				dotClass = 'marker-dot self';
				pulseClass = 'marker-pulse self';
			} else if (p.isAnchor) {
				iconClass = 'network-marker anchor';
				dotClass = 'marker-dot anchor';
				pulseClass = 'marker-pulse anchor';
			}

			const customIcon = L.divIcon({
				className: iconClass,
				html: `
					<div class="${pulseClass}"></div>
					<div class="${dotClass}"></div>
				`,
				iconSize: [24, 24],
				iconAnchor: [12, 12]
			});

			const popupContent = `
				<div class="p-2 font-sans text-xs bg-slate-950 text-slate-205 border border-slate-800 rounded-lg flex flex-col gap-1.5 min-w-[200px]">
					<div class="flex items-center justify-between border-b border-slate-800 pb-1">
						<span class="font-mono text-[10px] text-slate-500">PEER ID</span>
						<span class="px-1.5 py-0.2 rounded text-[9px] font-bold ${p.isSelf ? 'bg-emerald-950 text-emerald-400 border border-emerald-900/40' : p.isAnchor ? 'bg-pink-950 text-pink-400 border border-pink-900/40' : 'bg-cyan-955 text-cyan-400 border border-cyan-900/40'}">
							${p.isSelf ? 'me' : p.isAnchor ? 'Anchor Node' : 'Node'}
						</span>
					</div>
					<div class="font-mono text-[10px] truncate font-bold text-slate-200 select-all" title="${p.peerId}">
						${p.peerId}
					</div>
					<div class="flex justify-between text-[11px] mt-0.5">
						<span class="text-slate-500">Location</span>
						<span class="text-slate-300 font-medium">${p.location}</span>
					</div>
					<div class="flex justify-between text-[11px]">
						<span class="text-slate-500">Transport</span>
						<span class="text-slate-350 font-mono text-[10px]">${p.transport}</span>
					</div>
				</div>
			`;

			const marker = L.marker([p.lat, p.lon], { icon: customIcon })
				.bindPopup(popupContent, {
					closeButton: false,
					className: 'custom-leaflet-popup'
				});

			markersLayer.addLayer(marker);
			leafletMarkers.push(marker);
		});

		// Connect lines mesh topology
		if (validPeers.length >= 2) {
			for (let i = 0; i < validPeers.length; i++) {
				for (let j = i + 1; j < validPeers.length; j++) {
					const line = L.polyline(
						[[validPeers[i].lat, validPeers[i].lon], [validPeers[j].lat, validPeers[j].lon]],
						{
							color: '#00f0ff',
							weight: 1.5,
							opacity: 0.25,
							className: 'mesh-line'
						}
					);
					markersLayer.addLayer(line);
				}
			}
		}

		try {
			map.invalidateSize();
			const group = L.featureGroup(leafletMarkers);
			map.fitBounds(group.getBounds().pad(0.18), { maxZoom: 9 });
		} catch (_) {}
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

				const peersList = data.Peers.map((p, idx) => {
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

			await tick();
			updateMapData();
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

	function copyToClipboard(text: string, id: string) {
		navigator.clipboard.writeText(text).then(() => {
			copiedId = id;
			setTimeout(() => {
				if (copiedId === id) copiedId = null;
			}, 1500);
		});
	}

	async function connectToPeer() {
		if (!connectAddr.trim()) return;
		connectStatus = 'loading';
		connectError = '';
		try {
			const data = await apiFetch('/peers/connect', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ multiaddr: connectAddr.trim() })
			});
			if (data.ok) {
				connectStatus = 'ok';
				connectAddr = '';
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

	async function initMap() {
		if (!mapEl || map) return;
		try {
			L = await import('leaflet');
			if (!mapEl) return;

			map = L.map(mapEl, {
				center: [20, 0],
				zoom: 3,
				minZoom: 3,
				maxZoom: 9,
				zoomControl: true,
				attributionControl: false,
				maxBounds: [[-90, -180], [90, 180]],
				maxBoundsViscosity: 1.0
			});

			L.tileLayer('https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png', {
				maxZoom: 9,
				minZoom: 3,
				noWrap: true
			}).addTo(map);

			markersLayer = L.layerGroup().addTo(map);
			mapReady = true;
			setTimeout(() => {
				if (map) map.invalidateSize();
			}, 100);
			updateMapData();
		} catch (err) {
			console.error('Leaflet initialization failed:', err);
		}
	}

	$effect(() => {
		if (browser && mapEl && !map) {
			initMap();
		}
	});

	onMount(() => {
		loadPeers();
		const interval = setInterval(loadPeers, 10000);
		return () => {
			clearInterval(interval);
		};
	});

	onDestroy(() => {
		if (map) {
			map.remove();
			map = null;
			mapReady = false;
		}
	});
</script>

<svelte:head>
	<link rel="stylesheet" href="https://unpkg.com/leaflet@1.9.4/dist/leaflet.css" crossorigin="" />
</svelte:head>

<div class="flex flex-col gap-6">
	<!-- Page Header -->
	<div class="border-b border-slate-800 pb-4">
		<h1 class="text-2xl font-bold text-slate-50">Active Swarm Map</h1>
		<p class="text-xs text-slate-500 mt-1">
			Geographic coordinates and status parameters of active routing connections
		</p>
	</div>

	<!-- Connect to Peer -->
	<div class="bg-slate-900 border border-slate-800 rounded-xl p-4 flex flex-col sm:flex-row gap-3">
		<input
			type="text"
			bind:value={connectAddr}
			placeholder="Paste peer multiaddr, e.g. /ip4/1.2.3.4/tcp/4001/p2p/12D3KooW..."
			class="flex-1 bg-slate-950/60 border border-slate-850 text-xs px-3.5 py-2 rounded-lg focus:outline-none focus:border-cyan-500 font-mono"
			onkeydown={(e) => {
				if (e.key === 'Enter') connectToPeer();
			}}
		/>
		<button
			onclick={connectToPeer}
			disabled={connectStatus === 'loading' || !connectAddr.trim()}
			class="px-4 py-2 text-xs font-semibold rounded-lg transition-colors
				{connectStatus === 'loading'
					? 'bg-slate-800 text-slate-500 cursor-wait'
					: 'bg-cyan-600 hover:bg-cyan-500 text-white cursor-pointer'}"
		>
			{connectStatus === 'loading' ? 'Connecting...' : 'Connect'}
		</button>
	</div>
	{#if connectStatus === 'ok'}
		<div class="text-xs text-cyan-400 font-mono -mt-4">Peer connected successfully</div>
	{/if}
	{#if connectStatus === 'error'}
		<div class="text-xs text-red-400 font-mono -mt-4">Failed: {connectError}</div>
	{/if}

	{#if loading && !data}
		<div class="space-y-6 animate-pulse">
			<div class="h-60 bg-slate-900 rounded-lg w-full"></div>
			<div class="h-32 bg-slate-900 rounded-lg w-full"></div>
		</div>
	{:else if error}
		<div
			class="bg-red-950/20 border border-red-800/40 text-red-400 p-4 rounded-xl text-xs font-mono"
		>
			{error}
		</div>
	{:else if data}
		<!-- 2D Interactive Swarm Map -->
		<div
			class="bg-slate-900 border border-slate-800 rounded-xl shadow-xl relative overflow-hidden"
		>
			<!-- Peer counter overlay -->
			<div
				class="absolute top-4 left-4 z-[1000] flex flex-col items-start pointer-events-none select-none"
			>
				<span
					class="text-3xl md:text-4xl font-bold text-slate-50 tracking-tight leading-none"
				>
					{data.PeerCount}
				</span>
				<span
					class="text-[9px] text-cyan-400 font-mono tracking-widest uppercase mt-1 bg-slate-950/90 border border-cyan-800 px-2 py-0.5 rounded shadow"
				>
					peers in swarm
				</span>
			</div>
			<!-- Map container -->
			<div bind:this={mapEl} class="h-[450px] w-full rounded-xl overflow-hidden"></div>
		</div>

		<!-- Peers Table Registry -->
		<div class="bg-slate-900 border border-slate-800 rounded-xl overflow-hidden flex flex-col gap-4">
			<div
				class="px-6 py-4 bg-slate-950/40 border-b border-slate-800/80 flex flex-col sm:flex-row sm:items-center justify-between gap-4"
			>
				<h3 class="font-bold text-sm text-slate-300">Swarm Connections</h3>
				<div class="relative w-full sm:w-64">
					<input
						type="text"
						bind:value={searchFilter}
						placeholder="Filter by country or ID..."
						class="w-full bg-slate-950/60 border border-slate-850 text-xs px-3.5 py-1.5 rounded-lg focus:outline-none focus:border-cyan-500"
					/>
				</div>
			</div>

			{#if filteredPeers.length > 0}
				<div class="overflow-x-auto">
					<table class="w-full text-left border-collapse text-xs">
						<thead>
							<tr
								class="border-b border-slate-800/60 text-slate-500 font-mono text-[10px] uppercase bg-slate-950/20"
							>
								<th class="py-2.5 px-6 font-semibold w-1/4">Location</th>
								<th class="py-2.5 px-6 font-semibold w-1/3">Peer ID</th>
								<th class="py-2.5 px-6 font-semibold w-32">Transport</th>
								<th class="py-2.5 px-6 font-semibold text-right">Anchor</th>
							</tr>
						</thead>
						<tbody class="divide-y divide-slate-850/40 font-mono text-[11px]">
							{#each filteredPeers as peer}
								<tr class="hover:bg-slate-850/25 transition-colors group">
									<td
										class="py-3.5 px-6 font-sans text-slate-200 text-xs font-semibold"
									>
										{peer.location}
									</td>

									<td class="py-3.5 px-6 text-slate-400">
										<div class="flex items-center gap-2">
											<span>{peer.peerId}</span>
											<button
												onclick={() => copyToClipboard(peer.peerId, peer.peerId)}
												class="text-[10px] text-slate-650 hover:text-slate-350 opacity-0 group-hover:opacity-100 transition-opacity"
												title="Copy ID"
											>
												{copiedId === peer.peerId ? 'Copied' : 'Copy'}
											</button>
										</div>
									</td>

									<td class="py-3.5 px-6 text-slate-400">{peer.transport}</td>

									<td class="py-3.5 px-6 text-right font-sans">
										{#if peer.isSelf}
											<span
												class="px-2 py-0.5 rounded text-[9px] font-bold font-mono bg-emerald-950/40 text-emerald-400 border border-emerald-800/30 uppercase"
											>
												self
											</span>
										{:else if peer.isAnchor}
											<span
												class="px-2 py-0.5 rounded text-[9px] font-bold font-mono bg-pink-950/40 text-pink-400 border border-pink-800/30 uppercase"
											>
												anchor
											</span>
										{:else}
											<span class="text-slate-600 font-mono text-xs">no</span>
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
	{/if}
</div>

<style>
	:global(.leaflet-container) {
		background: #090f1c !important;
	}

	:global(.network-marker) {
		position: relative;
		width: 24px;
		height: 24px;
	}

	:global(.marker-dot) {
		position: absolute;
		width: 8px;
		height: 8px;
		top: 8px;
		left: 8px;
		border-radius: 50%;
		background-color: #00f0ff;
		box-shadow: 0 0 8px #00f0ff;
		z-index: 10;
	}

	:global(.marker-dot.anchor) {
		background-color: #ff00aa;
		box-shadow: 0 0 8px #ff00aa;
	}

	:global(.marker-dot.self) {
		background-color: #10b981;
		box-shadow: 0 0 8px #10b981;
		z-index: 12;
	}

	:global(.marker-pulse) {
		position: absolute;
		width: 24px;
		height: 24px;
		top: 0;
		left: 0;
		border-radius: 50%;
		border: 1px solid rgba(0, 240, 255, 0.4);
		animation: pulse 1.8s infinite ease-out;
		pointer-events: none;
		z-index: 1;
	}

	:global(.marker-pulse.anchor) {
		border-color: rgba(255, 0, 170, 0.4);
		animation: pulse-anchor 1.8s infinite ease-out;
	}

	:global(.marker-pulse.self) {
		border-color: rgba(16, 185, 129, 0.4);
		animation: pulse-self 1.8s infinite ease-out;
	}

	@keyframes pulse {
		0% {
			transform: scale(0.3);
			opacity: 1;
		}
		100% {
			transform: scale(1.5);
			opacity: 0;
		}
	}

	@keyframes pulse-anchor {
		0% {
			transform: scale(0.3);
			opacity: 1;
		}
		100% {
			transform: scale(1.5);
			opacity: 0;
		}
	}

	@keyframes pulse-self {
		0% {
			transform: scale(0.3);
			opacity: 1;
		}
		100% {
			transform: scale(1.5);
			opacity: 0;
		}
	}

	:global(.custom-leaflet-popup .leaflet-popup-content-wrapper) {
		background: #0f172a !important;
		border: 1px solid rgba(255, 255, 255, 0.08) !important;
		box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.5) !important;
		border-radius: 12px !important;
		padding: 0 !important;
	}

	:global(.custom-leaflet-popup .leaflet-popup-content) {
		margin: 0 !important;
		padding: 0 !important;
		color: #e2e8f0 !important;
	}

	:global(.custom-leaflet-popup .leaflet-popup-tip) {
		background: #0f172a !important;
		border: 1px solid rgba(255, 255, 255, 0.08) !important;
	}

	:global(.mesh-line) {
		stroke-dasharray: 4, 8;
		animation: mesh-flow 1.5s infinite linear;
	}

	@keyframes mesh-flow {
		from {
			stroke-dashoffset: 24;
		}
		to {
			stroke-dashoffset: 0;
		}
	}
</style>
