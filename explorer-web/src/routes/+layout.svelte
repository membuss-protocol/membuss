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

	const navItems = [
		{ name: 'Status', path: '/', icon: 'ph:gauge-light' },
		{ name: 'Files', path: '/files', icon: 'ph:folder-open-light' },
		{ name: 'MemNS', path: '/memns', icon: 'ph:identification-card-light' },
		{ name: 'Explore', path: '/explore', icon: 'ph:git-branch-light' },
		{ name: 'Peers', path: '/peers', icon: 'ph:circle-notch-light' },
		{ name: 'Node Info', path: '/node', icon: 'ph:gear-six-light' }
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
		} else if (lowercaseClean.startsWith('mem1')) {
			goto(`${base}/mid/${clean}`);
		} else if (lowercaseClean.startsWith('mem/mem1')) {
			const midVal = clean.slice(4);
			goto(`${base}/mid/${midVal}`);
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

<div class="min-h-screen premium-bg text-slate-100 flex flex-col font-sans selection:bg-cyan-500/30 selection:text-cyan-200">
	<!-- Top Bar / Navigation -->
	<header class="sticky top-0 z-40 bg-slate-950/30 backdrop-blur-xl border-b border-white/[0.04] px-6 md:px-12 py-4 flex items-center justify-between transition-all duration-500">
		<div class="flex items-center gap-10">
			<!-- Logo / Brand -->
			<a href={`${base}/`} class="flex items-center gap-3 group">
				<div class="w-9 h-9 rounded-xl bg-gradient-to-tr from-cyan-500 to-emerald-400 flex items-center justify-center font-bold text-slate-950 text-sm group-hover:scale-105 transition-all duration-500 shadow-[0_0_20px_rgba(6,182,212,0.15)] ring-1 ring-white/10">
					M
				</div>
				<div class="flex flex-col">
					<span class="font-bold text-base leading-none tracking-tight text-slate-100 group-hover:text-cyan-400 transition-colors duration-500">Membuss</span>
					<span class="text-[9px] text-slate-500 font-mono tracking-widest mt-0.5 uppercase">distributed storage</span>
				</div>
			</a>

			<!-- Nav links -->
			<nav class="hidden md:flex items-center gap-1">
				{#each navItems as item}
					{@const isActive = item.path === '/'
						? page.url.pathname === `${base}` || page.url.pathname === `${base}/`
						: page.url.pathname.startsWith(`${base}${item.path}`)}
					<a
						href={`${base}${item.path}`}
						class={`px-3.5 py-2 rounded-xl text-xs font-medium transition-all duration-500 flex items-center gap-2 ${
							isActive
								? 'bg-white/[0.05] text-cyan-400 border border-white/[0.05] shadow-[inset_0_1px_1px_rgba(255,255,255,0.05)]'
								: 'text-slate-400 hover:text-slate-200 hover:bg-white/[0.02] border border-transparent'
						}`}
					>
						<Icon icon={item.icon} class="w-4 h-4" />
						<span>{item.name}</span>
					</a>
				{/each}
			</nav>
		</div>

		<!-- Search & System Status -->
		<div class="flex items-center gap-5">
			<form onsubmit={handleSearch} class="relative hidden sm:block">
				<input
					type="text"
					bind:value={searchQuery}
					placeholder="Jump to MID, MemNS, or domain..."
					class="w-64 lg:w-80 bg-slate-900/40 border border-white/[0.04] text-slate-200 placeholder-slate-500 text-xs px-4 py-2 rounded-xl focus:outline-none focus:border-cyan-500/50 focus:ring-1 focus:ring-cyan-500/20 font-mono transition-all duration-500"
				/>
				<button type="submit" class="absolute right-3.5 top-2 text-slate-500 hover:text-cyan-450 transition-colors duration-300">
					<Icon icon="ph:magnifying-glass-light" class="w-4 h-4" />
				</button>
			</form>

			<!-- Quick Node Status Info -->
			<div class="flex items-center gap-2.5 px-3.5 py-2 bg-emerald-500/5 border border-emerald-500/10 rounded-xl text-[10px] font-mono text-emerald-450 shadow-[0_0_15px_rgba(16,185,129,0.02)]">
				<span class="relative flex h-1.5 w-1.5">
					<span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
					<span class="relative inline-flex rounded-full h-1.5 w-1.5 bg-emerald-500"></span>
				</span>
				<span>SWARM:</span>
				<span class="text-slate-200 font-bold">{stats ? stats.peerCount : '--'} CONNECTED</span>
			</div>
		</div>
	</header>

	<!-- Mobile Navigation Bar -->
	<div class="md:hidden border-b border-white/[0.04] bg-slate-950/20 px-4 py-2 flex items-center justify-around gap-1 overflow-x-auto">
		{#each navItems as item}
			{@const isActive = item.path === '/'
				? page.url.pathname === `${base}` || page.url.pathname === `${base}/`
				: page.url.pathname.startsWith(`${base}${item.path}`)}
			<a
				href={`${base}${item.path}`}
				class={`px-3 py-1.5 rounded-lg text-xs font-medium whitespace-nowrap transition-all duration-300 flex items-center gap-1.5 ${
					isActive
						? 'bg-white/[0.05] text-cyan-400 border border-white/[0.05]'
						: 'text-slate-400 hover:text-slate-200'
				}`}
			>
				<Icon icon={item.icon} class="w-4 h-4" />
				<span>{item.name}</span>
			</a>
		{/each}
	</div>

	<!-- Mobile Search Bar -->
	<div class="sm:hidden border-b border-white/[0.04] bg-slate-950/10 p-3">
		<form onsubmit={handleSearch} class="relative">
			<input
				type="text"
				bind:value={searchQuery}
				placeholder="Jump to MID, MemNS, or domain..."
				class="w-full bg-slate-900/40 border border-white/[0.04] text-slate-200 placeholder-slate-500 text-xs px-3.5 py-2.5 rounded-xl focus:outline-none focus:border-cyan-500/50 font-mono"
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
	<footer class="border-t border-white/[0.04] bg-slate-950/20 py-8 px-6 md:px-12 text-center text-[10px] text-slate-500 font-mono flex flex-col sm:flex-row items-center justify-between gap-4">
		<div>
			MEMBUSS DECENTRALIZED NETWORK &copy; {new Date().getFullYear()}
		</div>
		<div class="flex items-center gap-2 text-slate-600">
			<Icon icon="ph:circle-fill" class="w-1.5 h-1.5 text-cyan-500" />
			<span>SERVED BY MEM-GATE PUBLIC PROXY LAYER</span>
		</div>
	</footer>

	<Toasts />
</div>
