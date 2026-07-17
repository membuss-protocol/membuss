<script lang="ts">
	import './layout.css';
	import favicon from '$lib/assets/favicon.svg';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { onMount } from 'svelte';
	import { apiFetch } from '$lib/api';
	import Icon from '@iconify/svelte';
	import Toasts from '$lib/components/Toasts.svelte';

	let { children } = $props();

	let searchQuery = $state('');
	let stats = $state<{ peerCount: number; storeBytes: number } | null>(null);

	// Ordered by priority, clustered by domain: overview → content → network → system
	const navItems = [
		{ name: 'Status', path: '/', icon: 'ph:gauge-light', group: 'overview' },
		{ name: 'Files', path: '/files', icon: 'ph:folder-open-light', group: 'content' },
		{ name: 'Explore', path: '/explore', icon: 'ph:git-branch-light', group: 'content' },
		{ name: 'MemNS', path: '/memns', icon: 'ph:identification-card-light', group: 'content' },
		{ name: 'Peers', path: '/peers', icon: 'ph:circle-notch-light', group: 'network' },
		{ name: 'Tunnel', path: '/tunnel', icon: 'ph:link-light', group: 'network' },
		{ name: 'Node Info', path: '/node', icon: 'ph:gear-six-light', group: 'system' }
	];

	function handleSearch(e: Event) {
		e.preventDefault();
		let q = searchQuery.trim();
		if (!q) return;

		q = q.replace(/\s+/g, '');
		
		// Clean leading and trailing slashes to avoid double-slashes in router
		let clean = q;
		while (clean.startsWith('/')) {
			clean = clean.slice(1);
		}
		while (clean.endsWith('/')) {
			clean = clean.slice(0, -1);
		}
		
		const lowercaseClean = clean.toLowerCase();

		if (
			lowercaseClean.startsWith('memns/') ||
			lowercaseClean.startsWith('memns:')
		) {
			const name = clean.slice(6);
			goto(`${base}/memns/${name}`);
		} else if (
			lowercaseClean.startsWith('k51') ||
			lowercaseClean.startsWith('k3')
		) {
			goto(`${base}/memns/${clean}`);
		} else if (
			lowercaseClean.startsWith('mem/') &&
			(lowercaseClean.startsWith('mem/mem1') || clean.length > 16)
		) {
			const midVal = clean.slice(4);
			goto(`${base}/mid/${midVal}`);
		} else if (
			lowercaseClean.startsWith('mem') &&
			(lowercaseClean.startsWith('mem1') || clean.length > 12)
		) {
			goto(`${base}/mid/${clean}`);
		} else if (clean.includes('.')) {
			goto(`${base}/memlink/${clean}`);
		} else {
			// Fallback: default to MemNS record name
			goto(`${base}/memns/${clean}`);
		}
		searchQuery = '';
	}

	onMount(() => {
		apiFetch('/').then((data) => {
			stats = { peerCount: data.PeerCount || 0, storeBytes: data.StoreBytes || 0 };
		}).catch(() => {});
	});
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<title>Membuss Explorer</title>
</svelte:head>

<div class="min-h-screen premium-bg text-slate-100 flex flex-col font-sans selection:bg-cyan-400/25 selection:text-slate-50">
	<!-- Top Bar / Navigation -->
	<header class="sticky top-0 z-40 bg-slate-950/70 backdrop-blur-xl border-b border-slate-700 px-6 md:px-12 py-4 flex items-center justify-between transition-all duration-500">
		<div class="flex items-center gap-10">
			<!-- Logo / Brand -->
			<a href={`${base}/`} class="flex flex-col group" aria-label="Membuss home">
				<span class="font-display text-base leading-none text-slate-50 group-hover:text-cyan-400 transition-colors duration-500">MEMBUSS</span>
				<span class="text-[9px] text-slate-500 font-mono tracking-[0.22em] mt-1 uppercase">decentralized content network</span>
			</a>

			<!-- Nav links -->
			<nav class="hidden md:flex items-center gap-1">
				{#each navItems as item, i}
					{@const isActive = item.path === '/'
						? page.url.pathname === `${base}` || page.url.pathname === `${base}/`
						: page.url.pathname.startsWith(`${base}${item.path}`)}
					{#if i > 0 && navItems[i - 1].group !== item.group}
						<span class="mx-1.5 h-4 w-px bg-slate-700" aria-hidden="true"></span>
					{/if}
					<a
						href={`${base}${item.path}`}
						class={`relative px-3 py-2 text-xs font-medium transition-colors duration-300 flex items-center gap-2 ${
							isActive
								? 'text-cyan-400'
								: 'text-slate-400 hover:text-slate-200'
						}`}
					>
						<Icon icon={item.icon} class="w-4 h-4" />
						<span>{item.name}</span>
						{#if isActive}
							<span class="absolute -bottom-[17px] left-0 right-0 h-[2px] bg-cyan-400"></span>
						{/if}
					</a>
				{/each}
			</nav>
		</div>

		<!-- Search & System Status -->
		<div class="hidden sm:flex items-center gap-6">
			<form onsubmit={handleSearch} class="relative flex items-center h-9">
				<Icon icon="ph:magnifying-glass" class="absolute left-3 w-4 h-4 text-slate-500 pointer-events-none" />
				<input
					type="text"
					bind:value={searchQuery}
					placeholder="Resolve MID · MemNS · domain"
					class="w-56 lg:w-72 h-full bg-slate-950/50 border border-slate-700 rounded-[4px] text-slate-200 placeholder-slate-500 text-xs pl-9 pr-3 focus:outline-none focus:border-cyan-400/60 transition-colors duration-300 font-mono"
				/>
			</form>

			<!-- Live peer readout — minimal inline gauge -->
			<div class="flex items-center gap-2 font-mono whitespace-nowrap" title={stats && stats.peerCount > 0 ? 'Connected to swarm' : 'Searching for peers'}>
				<span class="relative flex h-1.5 w-1.5">
					<span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
					<span class="relative inline-flex rounded-full h-1.5 w-1.5 bg-emerald-500"></span>
				</span>
				<span class="text-[10px] uppercase tracking-[0.18em] text-slate-500">Peers</span>
				<span class="text-sm font-semibold text-slate-100 tabular-nums">{stats ? stats.peerCount : '--'}</span>
			</div>
		</div>
	</header>

	<!-- Mobile Navigation Bar -->
	<div class="md:hidden border-b border-slate-800 bg-slate-950/40 px-4 py-2 flex items-center justify-around gap-1 overflow-x-auto">
		{#each navItems as item}
			{@const isActive = item.path === '/'
				? page.url.pathname === `${base}` || page.url.pathname === `${base}/`
				: page.url.pathname.startsWith(`${base}${item.path}`)}
			<a
				href={`${base}${item.path}`}
				class={`px-3 py-1.5 text-xs font-medium whitespace-nowrap transition-colors duration-200 flex items-center gap-1.5 border-b-2 ${
					isActive
						? 'text-cyan-400 border-cyan-400'
						: 'text-slate-400 hover:text-slate-200 border-transparent'
				}`}
			>
				<Icon icon={item.icon} class="w-4 h-4" />
				<span>{item.name}</span>
			</a>
		{/each}
	</div>

	<!-- Mobile Search Bar -->
	<div class="sm:hidden border-b border-slate-800 bg-slate-950/20 p-3">
		<form onsubmit={handleSearch} class="relative">
			<input
				type="text"
				bind:value={searchQuery}
				placeholder="Jump to MID, MemNS, or domain..."
				class="w-full bg-slate-950/60 border border-slate-700 text-slate-200 placeholder-slate-500 text-xs px-3.5 py-2.5 rounded-sm focus:outline-none focus:border-cyan-400/70 font-mono"
			/>
			<button type="submit" class="absolute right-3.5 top-3 text-slate-500">
				<Icon icon="ph:magnifying-glass-light" class="w-4 h-4" />
			</button>
		</form>
	</div>

	<!-- Main Content Area -->
	<main class="flex-grow max-w-7xl w-full mx-auto p-6 md:p-12 flex flex-col gap-12">
		{@render children()}
	</main>

	<!-- Footer -->
	<footer class="border-t border-slate-800 bg-slate-950/40 py-8 px-6 md:px-12 text-[10px] text-slate-500 font-mono flex flex-col sm:flex-row items-center justify-between gap-4">
		<div class="flex items-center gap-3">
			<span class="tracking-wider">MEMBUSS &copy; {new Date().getFullYear()} — DECENTRALIZED CONTENT NETWORK</span>
		</div>
		<div class="flex items-center gap-2 text-slate-600">
			<span class="w-1.5 h-1.5 rounded-full bg-emerald-400"></span>
			<span class="tracking-wider">SERVED BY MEM-GATE PROXY LAYER</span>
		</div>
	</footer>

	<Toasts />
</div>
