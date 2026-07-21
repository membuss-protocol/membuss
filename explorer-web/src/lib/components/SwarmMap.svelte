<script lang="ts">
	import { geoEquirectangular, geoPath, geoGraticule10 } from 'd3-geo';
	import { feature, mesh } from 'topojson-client';
	import worldTopo from '$lib/assets/countries-110m.json';

	interface MapPeer {
		peerId: string;
		location: string;
		lat: number;
		lon: number;
		isAnchor: boolean;
		isSelf?: boolean;
	}

	let { peers = [], peerCount = 0 }: { peers?: MapPeer[]; peerCount?: number } = $props();

	// ── Projection (IPFS-webui approach: equirectangular + topojson land) ─────
	const VB_W = 1000;
	const VB_H = 500;
	const projection = geoEquirectangular()
		.scale(VB_H / Math.PI)
		.translate([VB_W / 2, VB_H / 2]);
	const pathGen = geoPath(projection as any);

	const topo: any = worldTopo;
	const landPath = pathGen(feature(topo, topo.objects.land) as any) ?? '';
	const bordersPath = pathGen(mesh(topo, topo.objects.countries, (a: any, b: any) => a !== b) as any) ?? '';
	const graticulePath = pathGen(geoGraticule10()) ?? '';

	const project = (lat: number, lon: number): { x: number; y: number } | null => {
		const r = projection([lon, lat]);
		if (!r) return null;
		return { x: r[0], y: r[1] };
	};

	// ── Cluster peers into ~3° geographic cells ───────────────────────────────
	type Cluster = {
		x: number;
		y: number;
		peers: MapPeer[];
		anchors: number;
		kind: 'anchor' | 'peer';
		label: string;
	};

	let clusters = $derived.by(() => {
		const map = new Map<string, Cluster>();
		let self: { x: number; y: number; peer: MapPeer } | null = null;
		for (const p of peers) {
			if (p.lat === 0 && p.lon === 0) continue;
			const pt = project(p.lat, p.lon);
			if (!pt) continue;
			if (p.isSelf) {
				self = { x: pt.x, y: pt.y, peer: p };
				continue;
			}
			const key = `${Math.round(p.lat / 3)}:${Math.round(p.lon / 3)}`;
			const c = map.get(key);
			if (c) {
				c.peers.push(p);
				if (p.isAnchor) c.anchors++;
			} else {
				map.set(key, {
					x: pt.x,
					y: pt.y,
					peers: [p],
					anchors: p.isAnchor ? 1 : 0,
					kind: 'peer',
					label: p.location || 'Unknown'
				});
			}
		}
		const list: Cluster[] = [...map.values()].map((c) => ({
			...c,
			kind: c.anchors > 0 ? 'anchor' : 'peer'
		}));
		return { list, self };
	});

	// IPFS-webui density tiers — small, readable dots that don't swallow the map
	const dotSize = (n: number) => (n < 3 ? 3 : n < 10 ? 4.5 : n < 50 ? 6 : 8);
	const COLOR = { peer: '#e8a33d', anchor: '#57b79e', self: '#f4efe2' };

	// ── 3D-looking transmission arcs from self to each cluster ────────────────
	function arcPath(a: { x: number; y: number }, b: { x: number; y: number }, lift: number) {
		const mx = (a.x + b.x) / 2;
		const my = (a.y + b.y) / 2;
		return `M ${a.x} ${a.y} Q ${mx} ${my - lift} ${b.x} ${b.y}`;
	}

	let links = $derived.by(() => {
		if (!clusters.self) return [];
		const s = clusters.self;
		return clusters.list.map((c, i) => {
			const dist = Math.hypot(c.x - s.x, c.y - s.y);
			const lift = Math.min(150, 30 + dist * 0.3);
			return {
				id: `lnk${i}`,
				top: arcPath(s, c, lift),
				kind: c.kind,
				dur: (2.2 + (dist / VB_W) * 3).toFixed(2),
				delay: ((i % 9) * 0.3).toFixed(2)
			};
		});
	});

	// ── Hover popover: lists every peer in the hovered cluster ────────────────
	const MAX_LIST = 6;
	let hover = $state<{ x: number; y: number; cluster: Cluster } | null>(null);
	let overPopover = false;
	let closeTimer: ReturnType<typeof setTimeout> | null = null;

	function enter(c: Cluster) {
		if (closeTimer) clearTimeout(closeTimer);
		hover = { x: c.x, y: c.y, cluster: c };
	}
	function scheduleClose() {
		if (closeTimer) clearTimeout(closeTimer);
		closeTimer = setTimeout(() => {
			if (!overPopover) hover = null;
		}, 180);
	}

	function shortId(id: string) {
		return id.length > 16 ? `${id.slice(0, 8)}…${id.slice(-6)}` : id;
	}
</script>

<div class="swarm relative w-full overflow-hidden aspect-[2/1]">
	<!-- Count overlay centered near bottom edge -->
	<div class="pointer-events-none absolute bottom-6 left-1/2 -translate-x-1/2 z-20 flex flex-col items-center select-none text-center">
		<span class="font-display text-4xl leading-none text-slate-50 tabular-nums md:text-5xl">{peerCount}</span>
		<span class="eyebrow mt-1">peers in swarm</span>
	</div>

	<!-- Legend -->
	<div
		class="pointer-events-none absolute right-6 bottom-4 z-20 flex select-none items-center gap-4 font-mono text-[9px] tracking-wider text-slate-400 uppercase"
	>
		<span class="flex items-center gap-1.5"><span class="h-2 w-2 rounded-full" style="background:{COLOR.self}"></span>you</span>
		<span class="flex items-center gap-1.5"><span class="h-2 w-2 rounded-full" style="background:{COLOR.peer}"></span>peer</span>
		<span class="flex items-center gap-1.5"><span class="h-2 w-2 rounded-full" style="background:{COLOR.anchor}"></span>anchor</span>
	</div>

	<svg
		viewBox="0 0 {VB_W} {VB_H}"
		class="h-full w-full"
		preserveAspectRatio="xMidYMid meet"
		role="img"
		aria-label="World map of {peerCount} connected swarm peers"
	>
		<defs>
			<!-- Grid-dot fill for landmasses -->
			<pattern id="landdots" width="6" height="6" patternUnits="userSpaceOnUse">
				<circle cx="1.4" cy="1.4" r="1.1" fill="#e9e2d2" opacity="0.22" />
			</pattern>
			<radialGradient id="glow-peer" cx="50%" cy="50%" r="50%">
				<stop offset="0%" stop-color="#e8a33d" stop-opacity="0.5" />
				<stop offset="100%" stop-color="#e8a33d" stop-opacity="0" />
			</radialGradient>
			<radialGradient id="glow-anchor" cx="50%" cy="50%" r="50%">
				<stop offset="0%" stop-color="#57b79e" stop-opacity="0.5" />
				<stop offset="100%" stop-color="#57b79e" stop-opacity="0" />
			</radialGradient>
			<radialGradient id="glow-self" cx="50%" cy="50%" r="50%">
				<stop offset="0%" stop-color="#f4efe2" stop-opacity="0.6" />
				<stop offset="100%" stop-color="#f4efe2" stop-opacity="0" />
			</radialGradient>
			<linearGradient id="arc-peer" x1="0" y1="0" x2="0" y2="1">
				<stop offset="0%" stop-color="#f7cd8a" />
				<stop offset="100%" stop-color="#b9761c" />
			</linearGradient>
			<linearGradient id="arc-anchor" x1="0" y1="0" x2="0" y2="1">
				<stop offset="0%" stop-color="#8fe0cb" />
				<stop offset="100%" stop-color="#35836d" />
			</linearGradient>
		</defs>

		<!-- Graticule -->
		<path d={graticulePath} fill="none" stroke="#e9e2d2" stroke-width="0.4" opacity="0.05" />
		<!-- Land: dot-filled, hairline borders -->
		<path d={landPath} fill="url(#landdots)" stroke="none" />
		<path d={bordersPath} fill="none" stroke="#e9e2d2" stroke-width="0.4" opacity="0.08" />

		<!-- Transmission arcs -->
		{#each links as l (l.id)}
			<path d={l.top} fill="none" stroke="#0c1416" stroke-width="3.2" opacity="0.65" stroke-linecap="round" />
			<path id={l.id} d={l.top} fill="none" stroke="url(#arc-{l.kind})" stroke-width="1.4" stroke-linecap="round" opacity="0.85" />
			<circle r="2.6" fill={l.kind === 'anchor' ? COLOR.anchor : COLOR.peer}>
				<animateMotion dur="{l.dur}s" begin="{l.delay}s" repeatCount="indefinite" rotate="auto">
					<mpath href="#{l.id}" />
				</animateMotion>
				<animate attributeName="opacity" values="0;1;1;0" dur="{l.dur}s" begin="{l.delay}s" repeatCount="indefinite" />
			</circle>
		{/each}

		<!-- Cluster nodes -->
		{#each clusters.list as c}
			<g
				role="button"
				tabindex="-1"
				class="cursor-pointer"
				onmouseenter={() => enter(c)}
				onmouseleave={scheduleClose}
			>
				<circle cx={c.x} cy={c.y} r={dotSize(c.peers.length) * 2.6} fill="url(#glow-{c.kind})" />
				{#if c.peers.length >= 10}
					<circle class="pulse" cx={c.x} cy={c.y} r={dotSize(c.peers.length)} fill="none" stroke={COLOR[c.kind]} stroke-width="1" />
				{/if}
				<circle cx={c.x} cy={c.y} r={dotSize(c.peers.length)} fill={COLOR[c.kind]} stroke="#0c1416" stroke-width="0.8" />
				{#if c.peers.length > 1}
					<text x={c.x} y={c.y + 2.6} text-anchor="middle" class="font-mono" font-size="7" font-weight="700" fill="#0c1416">{c.peers.length}</text>
				{/if}
			</g>
		{/each}

		<!-- Self node -->
		{#if clusters.self}
			<g>
				<circle cx={clusters.self.x} cy={clusters.self.y} r="20" fill="url(#glow-self)" />
				<circle class="pulse" cx={clusters.self.x} cy={clusters.self.y} r="7" fill="none" stroke="#f4efe2" stroke-width="1.2" />
				<circle cx={clusters.self.x} cy={clusters.self.y} r="4.5" fill="#f4efe2" stroke="#0c1416" stroke-width="1.2" />
			</g>
		{/if}
	</svg>

	<!-- Cluster popover -->
	{#if hover}
		<div
			role="tooltip"
			class="absolute z-30 w-64 -translate-x-1/2 -translate-y-full rounded-[5px] border border-slate-700 bg-slate-950/97 shadow-2xl backdrop-blur-sm"
			style="left: {(hover.x / VB_W) * 100}%; top: calc({(hover.y / VB_H) * 100}% - 14px);"
			onmouseenter={() => {
				overPopover = true;
				if (closeTimer) clearTimeout(closeTimer);
			}}
			onmouseleave={() => {
				overPopover = false;
				hover = null;
			}}
		>
			<div class="flex items-center justify-between gap-2 border-b border-slate-800 px-3 py-2">
				<span class="flex items-center gap-2 font-sans text-xs font-semibold text-slate-100">
					<span class="h-2 w-2 rounded-full" style="background:{COLOR[hover.cluster.kind]}"></span>
					{hover.cluster.label}
				</span>
				<span class="eyebrow !text-[9px]">{hover.cluster.peers.length} node{hover.cluster.peers.length === 1 ? '' : 's'}</span>
			</div>
			<div class="flex max-h-44 flex-col overflow-y-auto py-1">
				{#each hover.cluster.peers.slice(0, MAX_LIST) as p}
					<div class="flex items-center gap-2 px-3 py-1.5 hover:bg-white/[0.04]">
						<span
							class="h-1.5 w-1.5 shrink-0 rounded-full"
							style="background:{p.isAnchor ? COLOR.anchor : COLOR.peer}"
						></span>
						<code class="truncate font-mono text-[10px] text-slate-300" title={p.peerId}>{shortId(p.peerId)}</code>
						{#if p.isAnchor}<span class="ml-auto shrink-0 text-[8px] font-bold text-emerald-400 uppercase">anchor</span>{/if}
					</div>
				{/each}
				{#if hover.cluster.peers.length > MAX_LIST}
					<div class="px-3 py-1.5 font-mono text-[9px] text-slate-500">
						+ {hover.cluster.peers.length - MAX_LIST} more in this region
					</div>
				{/if}
			</div>
		</div>
	{/if}
</div>

<style>
	.pulse {
		transform-box: fill-box;
		transform-origin: center;
		animation: pulse 2.4s ease-out infinite;
	}
	@keyframes pulse {
		0% {
			transform: scale(0.6);
			opacity: 0.8;
		}
		100% {
			transform: scale(2.8);
			opacity: 0;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.pulse {
			animation: none;
		}
	}
</style>
