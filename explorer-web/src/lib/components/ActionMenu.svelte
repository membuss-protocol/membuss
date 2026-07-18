<script lang="ts">
	/*
	  ActionMenu — the "rack drawer"
	  ------------------------------
	  One calm trigger per content record. Opening it reveals a compact machined
	  card with the management verbs (Pin/Unpin, Share, Inspect, Delete). Choosing
	  Share slides the drawer sideways to a second pane of three copy targets —
	  the raw address, the link on THIS node, and the link on the PUBLIC gateway —
	  each showing the exact string it will place on the clipboard. Follows the
	  node-console identity: teal-ink panel, ochre primary, verdigris confirm,
	  brick destructive.
	*/
	import Icon from '@iconify/svelte';

	const PUBLIC_GATEWAY = 'https://gateway.membuss.dpdns.org';
	const PANEL_W = 268;

	type Kind = 'content' | 'memns';

	let {
		target,
		kind = 'content',
		isDir = false,
		sealed = undefined,
		inspectHref = undefined,
		onToggleSeal = undefined,
		onDelete = undefined,
		compact = true,
		shareOnly = false
	}: {
		target: string;
		kind?: Kind;
		isDir?: boolean;
		sealed?: boolean;
		inspectHref?: string;
		onToggleSeal?: () => void;
		onDelete?: () => void;
		compact?: boolean;
		shareOnly?: boolean;
	} = $props();

	let open = $state(false);
	let pane = $state<'menu' | 'share'>('menu');
	let copiedKey = $state<string | null>(null);

	let triggerEl = $state<HTMLButtonElement | null>(null);
	let panelX = $state(0);
	let panelY = $state(0);
	let openUp = $state(false);

	function localLink(): string {
		const origin = typeof window !== 'undefined' ? window.location.origin : '';
		if (kind === 'memns') return `${origin}/memns/${target}`;
		return `${origin}/mem/${target}${isDir ? '/' : ''}`;
	}
	function publicLink(): string {
		if (kind === 'memns') return `${PUBLIC_GATEWAY}/memns/${target}`;
		return `${PUBLIC_GATEWAY}/mem/${target}${isDir ? '/' : ''}`;
	}
	function rawId(): string {
		return kind === 'memns' ? `/memns/${target}` : target;
	}

	const shareTargets = $derived([
		{
			key: 'id',
			icon: 'ph:fingerprint',
			title: kind === 'memns' ? 'Copy name' : 'Copy content ID',
			value: rawId()
		},
		{
			key: 'local',
			icon: 'ph:house',
			title: 'Copy local link',
			value: localLink()
		},
		{
			key: 'public',
			icon: 'ph:globe-hemisphere-west',
			title: 'Copy public link',
			value: publicLink()
		}
	]);

	function place() {
		if (!triggerEl) return;
		const r = triggerEl.getBoundingClientRect();
		const vw = window.innerWidth;
		const vh = window.innerHeight;
		let x = r.right - PANEL_W;
		if (x < 8) x = 8;
		if (x + PANEL_W > vw - 8) x = vw - 8 - PANEL_W;
		panelX = x;
		const spaceBelow = vh - r.bottom;
		openUp = spaceBelow < 300 && r.top > spaceBelow;
		panelY = openUp ? r.top : r.bottom;
	}

	function toggle() {
		if (open) {
			close();
			return;
		}
		// Position before the panel mounts — the trigger is already in the DOM,
		// so we can read its rect now and avoid a first-open flash from (0,0).
		place();
		pane = shareOnly ? 'share' : 'menu';
		copiedKey = null;
		open = true;
	}

	function close() {
		open = false;
		copiedKey = null;
	}

	function copy(key: string, value: string) {
		navigator.clipboard.writeText(value).then(() => {
			copiedKey = key;
			setTimeout(() => {
				if (copiedKey === key) copiedKey = null;
			}, 1600);
		});
	}

	function runAndClose(fn?: () => void) {
		close();
		fn?.();
	}

	// Keep the drawer pinned to its trigger while the page or an inner list
	// scrolls (capture catches nested scrollers the window event misses).
	$effect(() => {
		if (!open) return;
		const reposition = () => place();
		window.addEventListener('scroll', reposition, true);
		window.addEventListener('resize', reposition);
		return () => {
			window.removeEventListener('scroll', reposition, true);
			window.removeEventListener('resize', reposition);
		};
	});
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && open && close()} />

<button
	bind:this={triggerEl}
	onclick={toggle}
	aria-haspopup="menu"
	aria-expanded={open}
	class={compact
		? `inline-flex h-7 w-7 items-center justify-center rounded-lg border text-slate-400 transition-colors active:scale-95 ${open ? 'border-cyan-500/40 bg-cyan-500/10 text-cyan-400' : 'border-slate-750 bg-slate-850 hover:border-slate-650 hover:text-slate-200'}`
		: `inline-flex items-center gap-1.5 rounded-lg border px-3.5 py-2 text-xs font-bold transition-colors active:scale-[0.98] ${open ? 'border-cyan-500/40 bg-cyan-500/10 text-cyan-400' : 'border-slate-750 bg-slate-850 text-slate-200 hover:border-slate-650'}`}
	title={shareOnly ? 'Share' : 'Manage'}
>
	{#if compact}
		<Icon icon="ph:dots-three-vertical-bold" class="text-base" />
	{:else}
		<Icon icon={shareOnly ? 'ph:share-network' : 'ph:sliders-horizontal'} class="text-sm" />
		{shareOnly ? 'Share' : 'Manage'}
	{/if}
</button>

{#if open}
	<!-- Outside-click catcher -->
	<button
		class="fixed inset-0 z-40 cursor-default bg-transparent"
		aria-label="Close menu"
		onclick={close}
		tabindex="-1"
	></button>

	<div
		class="fixed z-50 origin-top animate-in fade-in zoom-in-95 duration-150"
		style="left:{panelX}px; top:{panelY}px; width:{PANEL_W}px; transform: translateY({openUp
			? 'calc(-100% - 6px)'
			: '6px'});"
		role="menu"
	>
		<div
			class="overflow-hidden rounded-xl border border-slate-750 bg-slate-900 shadow-2xl shadow-black/50"
		>
			<div
				class="flex w-[200%] transition-transform duration-300 ease-[cubic-bezier(0.22,1,0.36,1)]"
				style="transform: translateX({pane === 'share' ? '-50%' : '0'});"
			>
				<!-- Pane 1 — verbs -->
				<div class="w-1/2 shrink-0 p-1.5">
					<div class="eyebrow px-2.5 pb-1.5 pt-1">Manage record</div>

					{#if sealed !== undefined && onToggleSeal}
						<button
							role="menuitem"
							onclick={() => runAndClose(onToggleSeal)}
							class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-xs font-semibold text-slate-200 transition-colors hover:bg-slate-800"
						>
							<Icon
								icon={sealed ? 'ph:push-pin-slash' : 'ph:push-pin'}
								class={`text-base ${sealed ? 'text-amber-500' : 'text-emerald-400'}`}
							/>
							{sealed ? 'Unpin from this node' : 'Pin to this node'}
						</button>
					{/if}

					<button
						role="menuitem"
						onclick={() => (pane = 'share')}
						class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-xs font-semibold text-slate-200 transition-colors hover:bg-slate-800"
					>
						<Icon icon="ph:share-network" class="text-base text-cyan-400" />
						Share
						<Icon icon="ph:caret-right" class="ml-auto text-sm text-slate-500" />
					</button>

					{#if inspectHref}
						<a
							role="menuitem"
							href={inspectHref}
							class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-xs font-semibold text-slate-200 transition-colors hover:bg-slate-800"
						>
							<Icon icon="ph:magnifying-glass" class="text-base text-slate-400" />
							Inspect
						</a>
					{/if}

					{#if onDelete}
						<div class="my-1 border-t border-slate-800"></div>
						<button
							role="menuitem"
							onclick={() => runAndClose(onDelete)}
							class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-xs font-semibold text-red-400 transition-colors hover:bg-red-950/40"
						>
							<Icon icon="ph:trash" class="text-base" />
							Delete from this node
						</button>
					{/if}
				</div>

				<!-- Pane 2 — share targets -->
				<div class="w-1/2 shrink-0 p-1.5">
					<div class="flex items-center gap-1 px-1 pb-1.5 pt-1">
						{#if !shareOnly}
							<button
								onclick={() => (pane = 'menu')}
								class="flex h-6 w-6 items-center justify-center rounded-md text-slate-400 transition-colors hover:bg-slate-800 hover:text-slate-200"
								aria-label="Back to actions"
							>
								<Icon icon="ph:caret-left-bold" class="text-sm" />
							</button>
						{/if}
						<span class="eyebrow">Share this {kind === 'memns' ? 'name' : 'record'}</span>
					</div>

					{#each shareTargets as t (t.key)}
						{@const done = copiedKey === t.key}
						<button
							onclick={() => copy(t.key, t.value)}
							class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left transition-colors hover:bg-slate-800"
						>
							<Icon
								icon={done ? 'ph:check-circle-fill' : t.icon}
								class={`shrink-0 text-base ${done ? 'text-emerald-400' : 'text-slate-400'}`}
							/>
							<span class="flex min-w-0 flex-col">
								<span
									class={`text-xs font-semibold ${done ? 'text-emerald-400' : 'text-slate-200'}`}
								>
									{done ? 'Copied to clipboard' : t.title}
								</span>
								<span class="truncate font-mono text-[10px] text-slate-500">{t.value}</span>
							</span>
						</button>
					{/each}
				</div>
			</div>
		</div>
	</div>
{/if}
