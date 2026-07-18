<script lang="ts">
	import { onDestroy, untrack } from 'svelte';
	import { base } from '$app/paths';
	import { formatBytes } from '$lib/api';
	import Icon from '@iconify/svelte';

	let { mid }: { mid: string } = $props();

	// ---- palette (canvas can't read Tailwind classes, so literal on-theme hex) ----
	const INK = '#0c1416';
	const OCHRE = '#e8a33d'; // this node / root
	const OCHRE_DIM = '#b9761c';
	const VERDIGRIS = '#57b79e'; // healthy / leaf
	const BONE = '#e9e2d2';
	const BONE_FAINT = 'rgba(233,226,210,0.35)';
	const HAIR = 'rgba(233,226,210,0.14)';

	interface DagNodeT {
		id: string; // mid
		size: number | null;
		links: string[] | null; // children mids once loaded
		loaded: boolean;
		loading: boolean;
		error: string | null;
		depth: number;
		// force-graph writes x/y/vx/vy onto these
		x?: number;
		y?: number;
	}
	interface DagLinkT {
		source: string | DagNodeT;
		target: string | DagNodeT;
	}

	// raw state — see project memory on Svelte-5 identity gotchas with force sims
	let nodes = $state.raw<DagNodeT[]>([]);
	let links = $state.raw<DagLinkT[]>([]);
	let selected = $state.raw<DagNodeT | null>(null);
	let hovered = $state.raw<DagNodeT | null>(null);
	let rev = $state(0); // bumped when we mutate raw objects in place, forces derived re-read
	let orientation = $state<'radialout' | 'td' | null>('radialout');
	let expandingAll = $state(false);
	let copied = $state(false);

	let W = $state(0);
	let H = $state(0);
	let container: HTMLDivElement | null = $state(null);

	let graph: any = null;
	const nodeById = new Map<string, DagNodeT>();

	// derived read of the selected node that re-runs when rev bumps (in-place mutations)
	let sel = $derived.by(() => {
		rev; // dependency
		return selected;
	});

	function gatewayBase() {
		return base.replace('/explorer', '');
	}

	function makeNode(id: string, depth: number): DagNodeT {
		return {
			id,
			size: null,
			links: null,
			loaded: false,
			loading: false,
			error: null,
			depth
		};
	}

	function commit() {
		// reassign to trigger $state.raw reactivity, and hand fresh arrays to force-graph
		nodes = [...nodes];
		links = [...links];
		rev++;
		if (graph) graph.graphData({ nodes, links });
	}

	async function loadNode(node: DagNodeT) {
		if (node.loaded || node.loading) return;
		node.loading = true;
		rev++;
		try {
			const res = await fetch(
				`${gatewayBase()}/mem/${encodeURIComponent(node.id)}?format=dag-json`
			);
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			const data = (await res.json()) as { size: number | null; links: string[] | null };
			node.size = data.size ?? null;
			node.links = data.links ?? [];
			node.loaded = true;

			// attach children, collapsing shared MIDs to a single node (dedup made visible)
			for (const childId of node.links) {
				let child = nodeById.get(childId);
				if (!child) {
					child = makeNode(childId, node.depth + 1);
					nodeById.set(childId, child);
					nodes.push(child);
				}
				const exists = links.some(
					(l) =>
						(typeof l.source === 'object' ? l.source.id : l.source) === node.id &&
						(typeof l.target === 'object' ? l.target.id : l.target) === childId
				);
				if (!exists) links.push({ source: node.id, target: childId });
			}
		} catch (err) {
			node.error = err instanceof Error ? err.message : 'fetch failed';
		} finally {
			node.loading = false;
			commit();
		}
	}

	function onNodeClick(node: DagNodeT) {
		selected = node;
		rev++;
		if (!node.loaded) loadNode(node);
		if (graph && node.x != null && node.y != null) {
			graph.centerAt(node.x, node.y, 700);
			graph.zoom(Math.max(graph.zoom(), 2.4), 700);
		}
	}

	async function expandVisible() {
		if (expandingAll) return;
		expandingAll = true;
		// snapshot current frontier and load in a bounded wave so we don't hammer the gateway
		const frontier = nodes.filter((n) => !n.loaded && !n.loading);
		for (const n of frontier) {
			await loadNode(n);
		}
		expandingAll = false;
	}

	function fit() {
		graph?.zoomToFit(600, 60);
	}

	function reset() {
		if (!graph) return;
		nodeById.clear();
		const root = makeNode(mid, 0);
		nodeById.set(mid, root);
		nodes = [root];
		links = [];
		selected = root;
		hovered = null;
		rev++;
		graph.graphData({ nodes, links });
		loadNode(root).then(() => setTimeout(fit, 400));
	}

	async function copyMid() {
		if (!sel) return;
		try {
			await navigator.clipboard.writeText(sel.id);
			copied = true;
			setTimeout(() => (copied = false), 1200);
		} catch {
			/* clipboard blocked — no-op */
		}
	}

	function roleOf(n: DagNodeT): 'root' | 'branch' | 'leaf' | 'frontier' {
		if (n.depth === 0) return 'root';
		if (!n.loaded) return 'frontier';
		return n.links && n.links.length > 0 ? 'branch' : 'leaf';
	}

	function nodeRadius(n: DagNodeT): number {
		const bytes = n.size ?? 0;
		// radius ∝ log(bytes); clamp so tiny/huge blocks stay legible
		return 4 + Math.min(9, Math.log10(bytes + 1) * 1.6);
	}

	// ---- graph lifecycle: seed depends ONLY on mid, body wrapped in untrack ----
	$effect(() => {
		mid; // the ONLY reactive dependency of this effect
		const el = container;
		if (!el) return;

		let root!: DagNodeT;
		untrack(() => {
			// tear down any previous instance (mid changed)
			if (graph) {
				graph._destructor?.();
				graph = null;
			}
			nodeById.clear();
			root = makeNode(mid, 0);
			nodeById.set(mid, root);
			nodes = [root];
			links = [];
			selected = root;
			hovered = null;
			rev++;
		});

		let cancelled = false;

		import('force-graph').then(({ default: ForceGraph }) => {
			if (cancelled || !el) return;
			const reduce =
				typeof matchMedia !== 'undefined' &&
				matchMedia('(prefers-reduced-motion: reduce)').matches;

			graph = new ForceGraph(el)
				.backgroundColor('rgba(0,0,0,0)')
				.width(el.clientWidth)
				.height(el.clientHeight)
				.nodeId('id')
				.nodeRelSize(1)
				.dagMode(orientation as any)
				.dagLevelDistance(60)
				.d3AlphaDecay(0.028)
				.d3VelocityDecay(0.32)
				.cooldownTime(6000)
				.linkColor(() => HAIR)
				.linkWidth(1)
				.linkDirectionalParticles(reduce ? 0 : 2)
				.linkDirectionalParticleWidth(2)
				.linkDirectionalParticleColor(() => OCHRE)
				.linkDirectionalParticleSpeed(0.006)
				.onNodeClick((n: any) => onNodeClick(n))
				.onNodeHover((n: any) => {
					hovered = n || null;
					rev++;
					if (el) el.style.cursor = n ? 'pointer' : 'grab';
				})
				.onBackgroundClick(() => {
					selected = null;
					rev++;
				})
				.nodeCanvasObject((node: any, ctx: CanvasRenderingContext2D, scale: number) => {
					const r = nodeRadius(node);
					const role = roleOf(node);
					const isSel = selected?.id === node.id;
					const isHov = hovered?.id === node.id;
					const x = node.x ?? 0;
					const y = node.y ?? 0;

					// selection / hover halo
					if (isSel || isHov) {
						ctx.beginPath();
						ctx.arc(x, y, r + 4, 0, 2 * Math.PI);
						ctx.fillStyle = isSel ? 'rgba(232,163,61,0.18)' : 'rgba(233,226,210,0.08)';
						ctx.fill();
					}

					ctx.beginPath();
					ctx.arc(x, y, r, 0, 2 * Math.PI);

					if (role === 'frontier') {
						// hollow dashed ring — an unexplored block
						ctx.fillStyle = INK;
						ctx.fill();
						ctx.setLineDash([2, 2]);
						ctx.lineWidth = 1.2 / scale;
						ctx.strokeStyle = BONE_FAINT;
						ctx.stroke();
						ctx.setLineDash([]);
					} else {
						ctx.fillStyle =
							role === 'root' ? OCHRE : role === 'branch' ? OCHRE_DIM : VERDIGRIS;
						ctx.fill();
						if (isSel) {
							ctx.lineWidth = 1.5 / scale;
							ctx.strokeStyle = BONE;
							ctx.stroke();
						}
					}

					// loading spinner tick
					if (node.loading) {
						ctx.beginPath();
						const a = (Date.now() / 200) % (2 * Math.PI);
						ctx.arc(x, y, r + 3, a, a + Math.PI * 1.2);
						ctx.lineWidth = 1.4 / scale;
						ctx.strokeStyle = OCHRE;
						ctx.stroke();
					}

					// label only for root, selected, hovered, or when zoomed in
					if (isSel || isHov || role === 'root' || scale > 3) {
						const label = node.id.slice(0, 10) + '…';
						const fs = Math.max(9, 11 / scale);
						ctx.font = `${fs}px 'IBM Plex Mono', monospace`;
						ctx.textAlign = 'center';
						ctx.textBaseline = 'top';
						const tw = ctx.measureText(label).width;
						ctx.fillStyle = 'rgba(12,20,22,0.82)';
						ctx.fillRect(x - tw / 2 - 3, y + r + 2, tw + 6, fs + 3);
						ctx.fillStyle = isSel || role === 'root' ? OCHRE : BONE;
						ctx.fillText(label, x, y + r + 3.5);
					}
				})
				.nodePointerAreaPaint((node: any, color: string, ctx: CanvasRenderingContext2D) => {
					ctx.beginPath();
					ctx.arc(node.x ?? 0, node.y ?? 0, nodeRadius(node) + 4, 0, 2 * Math.PI);
					ctx.fillStyle = color;
					ctx.fill();
				})
				.graphData({ nodes, links });

			// keep particle animation ticking so loading spinners redraw
			if (!reduce) graph.onEngineTick(() => {});

			loadNode(root).then(() => setTimeout(fit, 500));
		});

		return () => {
			cancelled = true;
			if (graph) {
				graph._destructor?.();
				graph = null;
			}
		};
	});

	// resize + orientation are applied imperatively (do NOT re-seed)
	$effect(() => {
		const w = W,
			h = H;
		if (graph && w && h) graph.width(w).height(h);
	});
	$effect(() => {
		const o = orientation;
		if (graph) {
			graph.dagMode(o as any);
			graph.d3ReheatSimulation?.();
			setTimeout(fit, 500);
		}
	});

	onDestroy(() => {
		if (graph) {
			graph._destructor?.();
			graph = null;
		}
	});
</script>

<div class="flex flex-col gap-3">
	<!-- Controls -->
	<div class="flex flex-wrap items-center gap-2">
		<div class="flex items-center rounded-lg border border-slate-800 bg-slate-950/50 p-0.5">
			<button
				onclick={() => (orientation = 'radialout')}
				class={`rounded-md px-2.5 py-1.5 font-mono text-[10px] tracking-wide transition-colors ${orientation === 'radialout' ? 'bg-slate-800 text-cyan-400' : 'text-slate-500 hover:text-slate-300'}`}
				title="Radial layout"
			>
				<Icon icon="ph:circles-three" class="inline text-sm" /> Radial
			</button>
			<button
				onclick={() => (orientation = 'td')}
				class={`rounded-md px-2.5 py-1.5 font-mono text-[10px] tracking-wide transition-colors ${orientation === 'td' ? 'bg-slate-800 text-cyan-400' : 'text-slate-500 hover:text-slate-300'}`}
				title="Top-down layered"
			>
				<Icon icon="ph:tree-structure" class="inline text-sm" /> Layered
			</button>
			<button
				onclick={() => (orientation = null)}
				class={`rounded-md px-2.5 py-1.5 font-mono text-[10px] tracking-wide transition-colors ${orientation === null ? 'bg-slate-800 text-cyan-400' : 'text-slate-500 hover:text-slate-300'}`}
				title="Free-floating force"
			>
				<Icon icon="ph:atom" class="inline text-sm" /> Free
			</button>
		</div>

		<button
			onclick={expandVisible}
			disabled={expandingAll}
			class="flex items-center gap-1.5 rounded-lg border border-slate-800 bg-slate-950/50 px-3 py-1.5 font-mono text-[10px] tracking-wide text-slate-400 transition-colors hover:text-cyan-400 disabled:opacity-50"
			title="Fetch links for every unexplored block currently shown"
		>
			{#if expandingAll}
				<span
					class="h-2.5 w-2.5 animate-spin rounded-full border border-cyan-500/30 border-t-cyan-400"
				></span>
				Expanding…
			{:else}
				<Icon icon="ph:arrows-out" class="text-sm" /> Expand frontier
			{/if}
		</button>

		<button
			onclick={fit}
			class="flex items-center gap-1.5 rounded-lg border border-slate-800 bg-slate-950/50 px-3 py-1.5 font-mono text-[10px] tracking-wide text-slate-400 transition-colors hover:text-cyan-400"
			title="Fit graph to view"
		>
			<Icon icon="ph:crop" class="text-sm" /> Fit
		</button>

		<button
			onclick={reset}
			class="ml-auto flex items-center gap-1.5 rounded-lg border border-slate-800 bg-slate-950/50 px-3 py-1.5 font-mono text-[10px] tracking-wide text-slate-500 transition-colors hover:text-slate-300"
			title="Collapse back to the root block"
		>
			<Icon icon="ph:arrow-counter-clockwise" class="text-sm" /> Reset
		</button>
	</div>

	<div class="relative">
		<!-- Canvas mount -->
		<div
			bind:this={container}
			bind:clientWidth={W}
			bind:clientHeight={H}
			class="h-[440px] w-full cursor-grab overflow-hidden rounded-xl border border-slate-850 bg-slate-950/40 active:cursor-grabbing sm:h-[520px]"
			style="background-image: radial-gradient(circle at 1px 1px, rgba(233,226,210,0.035) 1px, transparent 0); background-size: 22px 22px;"
		></div>

		<!-- Legend -->
		<div
			class="pointer-events-none absolute bottom-3 left-3 flex flex-col gap-1.5 rounded-lg border border-slate-800 bg-slate-950/80 px-3 py-2.5 font-mono text-[10px] text-slate-400 backdrop-blur-sm"
		>
			<div class="flex items-center gap-2">
				<span class="inline-block h-2.5 w-2.5 rounded-full" style="background:{OCHRE}"></span> Payload
				root
			</div>
			<div class="flex items-center gap-2">
				<span class="inline-block h-2.5 w-2.5 rounded-full" style="background:{OCHRE_DIM}"></span>
				Branch block
			</div>
			<div class="flex items-center gap-2">
				<span class="inline-block h-2.5 w-2.5 rounded-full" style="background:{VERDIGRIS}"></span>
				Leaf block
			</div>
			<div class="flex items-center gap-2">
				<span
					class="inline-block h-2.5 w-2.5 rounded-full border border-dashed"
					style="border-color:{BONE_FAINT}"
				></span>
				Unexplored
			</div>
		</div>

		<!-- Inspector -->
		{#if sel}
			<div
				class="absolute top-3 right-3 flex w-[240px] max-w-[70%] flex-col gap-2.5 rounded-lg border border-slate-800 bg-slate-950/85 p-3.5 backdrop-blur-sm"
			>
				<div class="flex items-center justify-between">
					<span class="eyebrow">
						{#if sel.depth === 0}
							Payload root
						{:else if !sel.loaded}
							Unexplored block
						{:else if sel.links && sel.links.length > 0}
							Branch block
						{:else}
							Leaf block
						{/if}
					</span>
					<span class="font-mono text-[10px] text-slate-600">depth {sel.depth}</span>
				</div>

				<div class="flex items-start gap-1.5">
					<code class="flex-1 font-mono text-[11px] break-all text-slate-200 select-all">{sel.id}</code>
					<button
						onclick={copyMid}
						class="shrink-0 text-slate-500 transition-colors hover:text-cyan-400"
						title="Copy MID"
					>
						<Icon icon={copied ? 'ph:check' : 'ph:copy'} class="text-sm" />
					</button>
				</div>

				<dl class="grid grid-cols-2 gap-y-1.5 font-mono text-[10px]">
					<dt class="text-slate-500">Size</dt>
					<dd class="text-right text-slate-300">
						{sel.size != null ? formatBytes(sel.size) : '—'}
					</dd>
					<dt class="text-slate-500">Links</dt>
					<dd class="text-right text-slate-300">
						{sel.loaded ? (sel.links?.length ?? 0) : '—'}
					</dd>
					<dt class="text-slate-500">State</dt>
					<dd class="text-right {sel.error ? 'text-red-400' : 'text-emerald-400'}">
						{sel.error ? 'error' : sel.loading ? 'loading' : sel.loaded ? 'resolved' : 'frontier'}
					</dd>
				</dl>

				{#if sel.error}
					<p class="font-mono text-[10px] text-red-400">{sel.error}</p>
				{/if}

				<div class="flex gap-2 pt-0.5">
					{#if !sel.loaded && !sel.loading}
						<button
							onclick={() => sel && loadNode(sel)}
							class="flex-1 rounded-md bg-cyan-500 px-2 py-1.5 font-mono text-[10px] font-bold text-slate-950 transition-colors hover:bg-cyan-400"
						>
							Explore links
						</button>
					{/if}
					<a
						href={`${base}/mid/${sel.id}`}
						class="flex-1 rounded-md border border-slate-800 bg-slate-900 px-2 py-1.5 text-center font-mono text-[10px] text-slate-300 transition-colors hover:text-cyan-400"
					>
						Open detail
					</a>
				</div>
			</div>
		{/if}
	</div>

	<p class="font-mono text-[10px] leading-relaxed text-slate-600">
		Drag to reposition · scroll to zoom · click a block to explore its Merkle links. Shared blocks
		collapse to one node, so deduplication is drawn as it exists on the network.
	</p>
</div>
