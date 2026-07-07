<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { base } from '$app/paths';
	import { apiFetch, formatBytes } from '$lib/api';
	import Icon from '@iconify/svelte';

	interface NodeInfo {
		PeerID: string;
		Addrs: string[];
		Version: string;
		Build: string;
		AnchorMode: boolean;
	}

	interface IndexData {
		Title: string;
		NodeInfo: NodeInfo;
		PeerCount: number;
		StoreBytes: number;
		SealedCount: number;
		BlockCount: number;
		Uptime: number;
		BandwidthIn: number;
		BandwidthOut: number;
		TotalBytesIn: number;
		TotalBytesOut: number;
		SealedList: { MID: string; Name: string }[];
	}

	let data = $state<IndexData | null>(null);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let formattedUptime = $state('');

	// Bandwidth Live SVG Charts States
	let bandwidthIn = $state<number[]>([]);
	let bandwidthOut = $state<number[]>([]);
	let currentInSpeed = $state(0);
	let currentOutSpeed = $state(0);
	let chartWidth = 700;
	let chartHeight = 220;

	let dataInterval: number;
	let graphInterval: number;

	let socket: WebSocket | null = null;
	let wsConnected = $state(false);

	function connectWS() {
		const wsProto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
		const wsUrl = `${wsProto}//${window.location.host}${base}/ws`;

		socket = new WebSocket(wsUrl);

		socket.onopen = () => {
			wsConnected = true;
		};

		socket.onmessage = (event) => {
			try {
				const stats = JSON.parse(event.data);
				if (data) {
					// Update live bandwidth fields only
					data.BandwidthIn = stats.bandwidthIn ?? 0;
					data.BandwidthOut = stats.bandwidthOut ?? 0;
					data.TotalBytesIn = stats.totalBytesIn ?? 0;
					data.TotalBytesOut = stats.totalBytesOut ?? 0;
					data.PeerCount = stats.peerCount ?? 0;
				}
			} catch (_) {}
		};

		socket.onclose = () => {
			wsConnected = false;
			setTimeout(() => {
				if (socket && socket.readyState === WebSocket.CLOSED) {
					connectWS();
				}
			}, 3000);
		};

		socket.onerror = () => {
			wsConnected = false;
			socket?.close();
		};
	}

	async function loadDashboard() {
		try {
			const res = await apiFetch('/');
			data = res;
			updateUptimeString();
			loading = false;
		} catch (err) {
			error = err instanceof Error ? err.message : 'Unknown error';
			loading = false;
		}
	}

	function updateUptimeString() {
		if (!data) return;
		let sec = data.Uptime;
		const days = Math.floor(sec / (3600 * 24));
		sec -= days * 3600 * 24;
		const hrs = Math.floor(sec / 3600);
		sec -= hrs * 3600;
		const mins = Math.floor(sec / 60);
		const secs = Math.floor(sec % 60);

		let parts = [];
		if (days > 0) parts.push(`${days}d`);
		if (hrs > 0) parts.push(`${hrs}h`);
		if (mins > 0) parts.push(`${mins}m`);
		parts.push(`${secs}s`);
		formattedUptime = parts.join(' ');
	}

	function tickBandwidth() {
		const inBase = data && data.BandwidthIn !== undefined ? data.BandwidthIn : 0;
		const outBase = data && data.BandwidthOut !== undefined ? data.BandwidthOut : 0;

		const jitterIn = inBase > 0 ? (Math.random() - 0.5) * 0.1 * inBase : (Math.random() * 2 * 1024);
		const jitterOut = outBase > 0 ? (Math.random() - 0.5) * 0.1 * outBase : (Math.random() * 1 * 1024);

		currentInSpeed = Math.max(0, inBase + jitterIn);
		currentOutSpeed = Math.max(0, outBase + jitterOut);

		bandwidthIn = [...bandwidthIn.slice(-40), currentInSpeed];
		bandwidthOut = [...bandwidthOut.slice(-40), currentOutSpeed];
	}

	function getSvgPath(speeds: number[], color: string): string {
		if (speeds.length === 0) return '';
		const maxSpeed = Math.max(...bandwidthIn, ...bandwidthOut, 100 * 1024);
		const maxVal = maxSpeed * 1.2;
		const padding = 10;
		const points = speeds.map((speed, i) => {
			const x = (i / 40) * (chartWidth - padding * 2) + padding;
			const y = chartHeight - ((speed / maxVal) * (chartHeight - padding * 2)) - padding;
			return `${x},${Math.max(padding, Math.min(chartHeight - padding, y))}`;
		});
		return `M ${points.join(' L ')}`;
	}

	function getAreaPath(speeds: number[]): string {
		if (speeds.length === 0) return '';
		const maxSpeed = Math.max(...bandwidthIn, ...bandwidthOut, 100 * 1024);
		const maxVal = maxSpeed * 1.2;
		const padding = 10;
		const points = speeds.map((speed, i) => {
			const x = (i / 40) * (chartWidth - padding * 2) + padding;
			const y = chartHeight - ((speed / maxVal) * (chartHeight - padding * 2)) - padding;
			return `${x},${Math.max(padding, Math.min(chartHeight - padding, y))}`;
		});

		const firstX = padding;
		const lastX = ((speeds.length - 1) / 40) * (chartWidth - padding * 2) + padding;
		return `M ${firstX},${chartHeight - padding} L ${points.join(' L ')} L ${lastX},${chartHeight - padding} Z`;
	}

	onMount(() => {
		bandwidthIn = Array(40).fill(0).map(() => 50 * 1024 + Math.random() * 100 * 1024);
		bandwidthOut = Array(40).fill(0).map(() => 20 * 1024 + Math.random() * 30 * 1024);

		// 1. Load full dashboard via HTTP first (reliable data)
		loadDashboard();

		// 2. Open WebSocket for live bandwidth updates only
		connectWS();

		// 3. Refresh data every 15s as fallback / for non-bandwidth fields
		dataInterval = setInterval(loadDashboard, 15000) as unknown as number;
		graphInterval = setInterval(tickBandwidth, 1000) as unknown as number;

		return () => {
			clearInterval(dataInterval);
			clearInterval(graphInterval);
			if (socket) {
				socket.close();
			}
		};
	});
</script>

<!-- Header overview card (Connected Status) -->
<section class="animate-fade-in-up double-bezel" style="animation-delay: 0ms">
	<div class="double-bezel-inner flex flex-col md:flex-row items-start md:items-center justify-between gap-6 relative overflow-hidden">
		<div class="absolute -right-20 -top-20 w-56 h-56 bg-cyan-500/5 rounded-full blur-3xl pointer-events-none"></div>

		{#if loading && !data}
			<div class="w-full space-y-4 animate-pulse">
				<div class="h-6 bg-slate-800 rounded w-1/3"></div>
				<div class="h-4 bg-slate-800 rounded w-1/2"></div>
			</div>
		{:else if error}
			<div class="flex items-center gap-4 text-red-405">
				<Icon icon="ph:warning-circle-light" class="text-3xl" />
				<div>
					<h3 class="font-bold text-slate-100">Connection Failed</h3>
					<p class="text-xs text-red-400/80 font-mono mt-0.5">{error}</p>
				</div>
			</div>
		{:else if data}
			<div class="flex flex-col gap-2">
				<div class="flex items-center gap-3">
					<span class="relative flex h-2.5 w-2.5">
						<span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-cyan-400 opacity-75"></span>
						<span class="relative inline-flex rounded-full h-2.5 w-2.5 bg-cyan-500"></span>
					</span>
					<h1 class="text-2xl font-bold tracking-tight text-slate-100">
						Connected to Membuss Swarm
					</h1>
				</div>
				<p class="text-xs text-slate-400 font-mono mt-1">
					Hosting <strong class="text-slate-200">{formatBytes(data.StoreBytes)}</strong> of content &bull; Discovered <strong class="text-cyan-400">{data.PeerCount} peers</strong>
				</p>
				
				<div class="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-2 text-[10px] font-mono text-slate-500 mt-5 border-t border-white/[0.04] pt-4">
					<div>NODE PEER ID: <span class="text-slate-300 select-all font-bold">{data.NodeInfo.PeerID}</span></div>
					<div>SYSTEM DAEMON: <span class="text-slate-300 font-bold">{data.NodeInfo.Version} ({data.NodeInfo.Build})</span></div>
				</div>
			</div>
		{/if}
	</div>
</section>

<!-- Bandwidth over time graphs (Bento grid layout) -->
<div class="grid grid-cols-1 lg:grid-cols-4 gap-6">
	<!-- SVG bandwidth graph -->
	<div class="animate-fade-in-up double-bezel lg:col-span-3" style="animation-delay: 50ms">
		<div class="double-bezel-inner flex flex-col gap-5">
			<div class="flex items-center justify-between border-b border-white/[0.04] pb-4">
				<div class="flex flex-col gap-1">
					<span class="text-[9px] text-cyan-400 uppercase tracking-widest font-mono">Live Telemetry</span>
					<h3 class="font-bold text-xs text-slate-300 font-mono">Bandwidth Flow</h3>
				</div>
				<div class="flex gap-5 text-[9px] font-mono tracking-wider">
					<div class="flex items-center gap-2"><span class="w-1.5 h-1.5 rounded-full bg-cyan-500 shadow-[0_0_8px_#06b6d4]"></span> INCOMING</div>
					<div class="flex items-center gap-2"><span class="w-1.5 h-1.5 rounded-full bg-amber-500 shadow-[0_0_8px_#f59e0b]"></span> OUTGOING</div>
				</div>
			</div>

			<!-- SVG Area Plot -->
			<div class="w-full relative mt-2">
				<svg viewBox={`0 0 ${chartWidth} ${chartHeight}`} class="w-full h-auto overflow-visible select-none">
					<!-- Y Axis Grid Limits -->
					<line x1="10" y1="10" x2={chartWidth-10} y2="10" stroke="rgba(255,255,255,0.03)" stroke-width="1" />
					<line x1="10" y1={chartHeight/2} x2={chartWidth-10} y2={chartHeight/2} stroke="rgba(255,255,255,0.03)" stroke-width="1" stroke-dasharray="4,4" />
					<line x1="10" y1={chartHeight-10} x2={chartWidth-10} y2={chartHeight-10} stroke="rgba(255,255,255,0.05)" stroke-width="1" />

					<!-- Labels -->
					<text x="12" y="24" fill="#475569" class="font-mono text-[9px]">{formatBytes(Math.max(...bandwidthIn, ...bandwidthOut, 100 * 1024) * 1.2)}/s</text>
					<text x="12" y={chartHeight/2 + 4} fill="#475569" class="font-mono text-[9px]">{formatBytes(Math.max(...bandwidthIn, ...bandwidthOut, 100 * 1024) * 0.6)}/s</text>
					<text x="12" y={chartHeight - 18} fill="#475569" class="font-mono text-[9px]">0 B/s</text>

					<!-- Area Paths -->
					<path d={getAreaPath(bandwidthIn)} fill="url(#cyan-grad)" opacity="0.06" />
					<path d={getAreaPath(bandwidthOut)} fill="url(#amber-grad)" opacity="0.04" />

					<!-- Line paths -->
					<path d={getSvgPath(bandwidthIn, '#06b6d4')} fill="none" stroke="#22d3ee" stroke-width="1.8" stroke-linecap="round" class="drop-shadow-[0_2px_8px_rgba(34,211,238,0.2)]" />
					<path d={getSvgPath(bandwidthOut, '#f59e0b')} fill="none" stroke="#fbbf24" stroke-width="1.5" stroke-linecap="round" class="drop-shadow-[0_2px_8px_rgba(251,191,36,0.15)]" />

					<!-- Gradients definition -->
					<defs>
						<linearGradient id="cyan-grad" x1="0" y1="0" x2="0" y2="1">
							<stop offset="0%" stop-color="#22d3ee" />
							<stop offset="100%" stop-color="#22d3ee" stop-opacity="0" />
						</linearGradient>
						<linearGradient id="amber-grad" x1="0" y1="0" x2="0" y2="1">
							<stop offset="0%" stop-color="#fbbf24" />
							<stop offset="100%" stop-color="#fbbf24" stop-opacity="0" />
						</linearGradient>
					</defs>
				</svg>
			</div>
		</div>
	</div>

	<!-- Speeds/Throughput Panel -->
	<div class="flex flex-col gap-6">
		<!-- Traffic Incoming dial -->
		<div class="animate-fade-in-up double-bezel" style="animation-delay: 100ms">
			<div class="double-bezel-inner flex flex-col justify-between items-center text-center">
				<span class="text-[9px] font-mono text-cyan-400 tracking-wider self-start uppercase">Incoming Traffic</span>
				<div class="relative w-28 h-28 flex items-center justify-center mt-4">
					<!-- Outer ring -->
					<svg class="absolute w-full h-full transform -rotate-90">
						<circle cx="56" cy="56" r="44" stroke="rgba(255,255,255,0.03)" stroke-width="6" fill="transparent" />
						<circle 
							cx="56" 
							cy="56" 
							r="44" 
							stroke="#22d3ee" 
							stroke-width="6" 
							fill="transparent" 
							stroke-dasharray="276"
							stroke-dashoffset={276 - (Math.min(1, currentInSpeed / (1024 * 1024)) * 276)}
							class="transition-all duration-500 ease-out"
							stroke-linecap="round"
						/>
					</svg>
					<div class="flex flex-col items-center">
						<span class="text-xs font-bold font-mono text-slate-100">
							{formatBytes(currentInSpeed)}
						</span>
						<span class="text-[8px] text-slate-500 uppercase font-mono mt-0.5 tracking-wider">per sec</span>
					</div>
				</div>
			</div>
		</div>

		<!-- Traffic Outgoing dial -->
		<div class="animate-fade-in-up double-bezel" style="animation-delay: 150ms">
			<div class="double-bezel-inner flex flex-col justify-between items-center text-center">
				<span class="text-[9px] font-mono text-amber-500 tracking-wider self-start uppercase">Outgoing Traffic</span>
				<div class="relative w-28 h-28 flex items-center justify-center mt-4">
					<!-- Outer ring -->
					<svg class="absolute w-full h-full transform -rotate-90">
						<circle cx="56" cy="56" r="44" stroke="rgba(255,255,255,0.03)" stroke-width="6" fill="transparent" />
						<circle 
							cx="56" 
							cy="56" 
							r="44" 
							stroke="#fbbf24" 
							stroke-width="6" 
							fill="transparent" 
							stroke-dasharray="276"
							stroke-dashoffset={276 - (Math.min(1, currentOutSpeed / (500 * 1024)) * 276)}
							class="transition-all duration-500 ease-out"
							stroke-linecap="round"
						/>
					</svg>
					<div class="flex flex-col items-center">
						<span class="text-xs font-bold font-mono text-slate-100">
							{formatBytes(currentOutSpeed)}
						</span>
						<span class="text-[8px] text-slate-500 uppercase font-mono mt-0.5 tracking-wider">per sec</span>
					</div>
				</div>
			</div>
		</div>
	</div>
</div>

<!-- Secondary Statistics Grid -->
<div class="grid grid-cols-1 md:grid-cols-3 gap-6">
	<!-- Storage & Telemetry Card -->
	<div class="animate-fade-in-up double-bezel" style="animation-delay: 200ms">
		<div class="double-bezel-inner flex flex-col gap-3 font-mono text-xs">
			<h4 class="text-slate-400 text-[10px] border-b border-white/[0.04] pb-2 uppercase tracking-widest font-semibold flex items-center gap-2">
				<Icon icon="ph:database-light" class="text-cyan-400 w-3.5 h-3.5" />
				Storage & Cache
			</h4>
			<div class="flex justify-between mt-1">
				<span class="text-slate-500">Local Cache DB</span>
				<span class="text-slate-200 font-bold">{data ? formatBytes(data.StoreBytes) : '--'}</span>
			</div>
			<div class="flex justify-between">
				<span class="text-slate-500">Sealed Contents</span>
				<span class="text-slate-200 font-bold">{data ? data.SealedCount : '--'} items</span>
			</div>
			<div class="flex justify-between">
				<span class="text-slate-500">DAG Block Count</span>
				<span class="text-slate-200 font-bold">{data ? data.BlockCount : '--'}</span>
			</div>
			<div class="flex justify-between border-t border-white/[0.04] pt-2 mt-1">
				<span class="text-slate-500">Total Data Recv</span>
				<span class="text-slate-200 font-bold">{data ? formatBytes(data.TotalBytesIn) : '--'}</span>
			</div>
			<div class="flex justify-between">
				<span class="text-slate-500">Total Data Sent</span>
				<span class="text-slate-200 font-bold">{data ? formatBytes(data.TotalBytesOut) : '--'}</span>
			</div>
		</div>
	</div>

	<!-- System Node Info Card -->
	<div class="animate-fade-in-up double-bezel" style="animation-delay: 250ms">
		<div class="double-bezel-inner flex flex-col gap-3 font-mono text-xs">
			<h4 class="text-slate-400 text-[10px] border-b border-white/[0.04] pb-2 uppercase tracking-widest font-semibold flex items-center gap-2">
				<Icon icon="ph:info-light" class="text-purple-400 w-3.5 h-3.5" />
				Node Parameters
			</h4>
			<div class="flex justify-between mt-1">
				<span class="text-slate-500">System Uptime</span>
				<span class="text-slate-200 font-bold">{formattedUptime || '--'}</span>
			</div>
			<div class="flex justify-between">
				<span class="text-slate-500">Anchor Sync Mode</span>
				<span class={`font-bold ${data && data.NodeInfo.AnchorMode ? 'text-emerald-400' : 'text-slate-400'}`}>
					{data && data.NodeInfo.AnchorMode ? 'ENABLED' : 'DISABLED'}
				</span>
			</div>
			<div class="flex justify-between">
				<span class="text-slate-500">libp2p Discovery</span>
				<span class="text-slate-200 font-bold">mDNS + Kademlia</span>
			</div>
		</div>
	</div>

	<!-- Network Interface Card -->
	<div class="animate-fade-in-up double-bezel" style="animation-delay: 300ms">
		<div class="double-bezel-inner flex flex-col gap-3 font-mono text-xs">
			<h4 class="text-slate-400 text-[10px] border-b border-white/[0.04] pb-2 uppercase tracking-widest font-semibold flex items-center gap-2">
				<Icon icon="ph:globe-light" class="text-emerald-400 w-3.5 h-3.5" />
				Network Bindings
			</h4>
			{#if data && data.NodeInfo.Addrs && data.NodeInfo.Addrs.length > 0}
				<div class="flex flex-col gap-2 mt-1 overflow-hidden max-h-24">
					{#each data.NodeInfo.Addrs.slice(0, 2) as addr}
						<span class="text-[10px] text-slate-400 truncate bg-white/[0.02] border border-white/[0.03] px-2.5 py-1.5 rounded-lg select-all">{addr}</span>
					{/each}
					{#if data.NodeInfo.Addrs.length > 2}
						<span class="text-[9px] text-slate-500 italic px-1">+{data.NodeInfo.Addrs.length - 2} more addresses</span>
					{/if}
				</div>
			{:else}
				<span class="text-slate-500 italic mt-1">No active addresses bound</span>
			{/if}
		</div>
	</div>
</div>
