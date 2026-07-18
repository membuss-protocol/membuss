<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { apiFetch } from '$lib/api';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import Icon from '@iconify/svelte';

	interface TunnelStatus {
		enabled: boolean;
		status: 'active' | 'connecting' | 'inactive' | 'error';
		public_address: string;
		last_error: string;
		authtoken_configured: boolean;
		peer_id: string;
	}

	let status = $state<TunnelStatus | null>(null);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let authtokenInput = $state('');
	let showToken = $state(false);
	let copiedAddress = $state(false);
	let actionPending = $state(false);
	let isReplacingToken = $state(false);

	let pollInterval: number;

	async function fetchStatus() {
		try {
			const data = await apiFetch<TunnelStatus>('/node/tunnel');
			status = data;
			error = null;
		} catch (err) {
			error = err instanceof Error ? err.message : 'Failed to query tunnel status';
		} finally {
			loading = false;
		}
	}

	async function configureTunnel(action: 'install' | 'uninstall' | 'start' | 'stop') {
		actionPending = true;
		try {
			const body: any = { action };
			if (action === 'install') {
				body.authtoken = authtokenInput.trim();
				if (!body.authtoken) {
					alert('Please enter a valid ngrok Authtoken.');
					actionPending = false;
					return;
				}
			}
			await apiFetch('/node/tunnel', {
				method: 'POST',
				body: JSON.stringify(body),
				headers: { 'Content-Type': 'application/json' }
			});
			authtokenInput = '';
			isReplacingToken = false;
			await fetchStatus();
		} catch (err) {
			alert(err instanceof Error ? err.message : 'Failed to execute tunnel action');
		} finally {
			actionPending = false;
		}
	}

	function copyToClipboard(text: string) {
		navigator.clipboard.writeText(text).then(() => {
			copiedAddress = true;
			setTimeout(() => {
				copiedAddress = false;
			}, 2000);
		});
	}

	onMount(() => {
		fetchStatus();
		pollInterval = setInterval(fetchStatus, 3000) as unknown as number;
	});

	onDestroy(() => {
		if (pollInterval) clearInterval(pollInterval);
	});
</script>

<div class="flex flex-col gap-8 w-full max-w-4xl mx-auto py-2">
	<!-- Header -->
	<div class="flex flex-col gap-2">
		<div class="flex items-center gap-3">
			<span class="p-2.5 rounded-xl bg-slate-900 border border-slate-800 text-cyan-400">
				<Icon icon="ph:link-bold" class="text-xl" />
			</span>
			<div>
				<h1 class="font-display text-xl text-slate-50">Public Swarm Tunnel</h1>
				<p class="text-xs text-slate-550 font-mono mt-0.5">Expose local libp2p TCP listen ports via secure programmatic tunneling</p>
			</div>
		</div>
	</div>

	{#if loading && !status}
		<div class="grid grid-cols-1 md:grid-cols-3 gap-6">
			<div class="md:col-span-2 bg-slate-900/60 border border-slate-850 rounded-2xl p-6 flex flex-col gap-6">
				<div class="flex justify-between items-start">
					<Skeleton width="8rem" height="0.8rem" />
					<Skeleton width="6rem" height="1.5rem" rounded="rounded-full" />
				</div>
				<Skeleton width="60%" height="0.6rem" />
				<Skeleton width="100%" height="3rem" rounded="rounded-xl" />
				<Skeleton width="40%" height="0.8rem" />
			</div>
			<div class="bg-slate-900/60 border border-slate-850 rounded-2xl p-6 flex flex-col gap-4">
				<Skeleton width="10rem" height="0.8rem" />
				{#each Array(3) as _}
					<Skeleton width="100%" height="1.5rem" rounded="rounded-lg" />
				{/each}
			</div>
		</div>
	{:else if error}
		<div class="bg-red-950/20 border border-red-800/40 text-red-400 p-4 rounded-xl text-xs font-mono">
			{error}
		</div>
	{:else if status}
		<div class="grid grid-cols-1 md:grid-cols-3 gap-6">
			<!-- Live Tunnel Status Bento -->
			<div class="md:col-span-2 bg-slate-900/60 border border-slate-850 rounded-2xl p-6 flex flex-col justify-between shadow-xl backdrop-blur-sm relative overflow-hidden group">
				<div class="absolute inset-0 bg-radial-gradient from-cyan-500/5 via-transparent to-transparent pointer-events-none opacity-50"></div>
				
				<div class="flex flex-col gap-4 z-10">
					<div class="flex justify-between items-start">
						<h3 class="font-bold text-xs text-slate-450 font-mono uppercase tracking-wider">Tunnel State</h3>
						
						{#if status.status === 'active'}
							<span class="px-2.5 py-1 rounded-full text-[10px] font-bold font-mono bg-emerald-500/10 text-emerald-400 border border-emerald-500/30 flex items-center gap-1.5 shadow-[0_0_8px_rgba(16,185,129,0.1)]">
								<span class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-ping"></span>
								Active / Public
							</span>
						{:else if status.status === 'connecting'}
							<span class="px-2.5 py-1 rounded-full text-[10px] font-bold font-mono bg-amber-500/10 text-amber-400 border border-amber-500/30 flex items-center gap-1.5">
								<span class="w-1.5 h-1.5 rounded-full bg-amber-500 animate-pulse"></span>
								Connecting
							</span>
						{:else if status.status === 'error'}
							<span class="px-2.5 py-1 rounded-full text-[10px] font-bold font-mono bg-red-500/10 text-red-400 border border-red-500/30 flex items-center gap-1.5">
								<span class="w-1.5 h-1.5 rounded-full bg-red-500"></span>
								Error
							</span>
						{:else}
							<span class="px-2.5 py-1 rounded-full text-[10px] font-bold font-mono bg-slate-800 text-slate-500 border border-slate-700/50 flex items-center gap-1.5">
								<span class="w-1.5 h-1.5 rounded-full bg-slate-600"></span>
								Inactive
							</span>
						{/if}
					</div>

					{#if status.status === 'active' && status.public_address}
						<div class="flex flex-col gap-2 mt-2">
							<span class="text-[9px] font-mono text-slate-550 uppercase tracking-wider">Public Swarm Multiaddress</span>
							
							<div class="flex items-center gap-2 p-3 bg-slate-950/80 border border-slate-850 rounded-xl font-mono text-xs text-slate-300 select-all group/addr">
								<Icon icon="ph:globe-bold" class="text-cyan-400 shrink-0 text-base" />
								<span class="truncate flex-grow">
									/dns4/{status.public_address.replace('tcp://', '').split(':')[0]}/tcp/{status.public_address.replace('tcp://', '').split(':')[1]}/p2p/{status.peer_id}
								</span>
								<button 
									onclick={() => copyToClipboard(`/dns4/${status.public_address.replace('tcp://', '').split(':')[0]}/tcp/${status.public_address.replace('tcp://', '').split(':')[1]}/p2p/${status.peer_id}`)}
									class="p-1 rounded bg-slate-900 hover:bg-slate-800 border border-slate-800 text-slate-400 hover:text-slate-200 transition-colors"
									title="Copy Multiaddr"
								>
									<Icon icon={copiedAddress ? 'ph:check-bold' : 'ph:copy-bold'} class="text-xs" />
								</button>
							</div>

							<p class="text-[10px] text-slate-550 leading-normal mt-1">
								This public multiaddress is dynamically advertised to the Mem-DHT. Other nodes on the global internet will connect to your local client through this tunnel.
							</p>
						</div>
					{:else if status.status === 'error' && status.last_error}
						<div class="p-4 bg-red-950/20 border border-red-900/30 rounded-xl text-xs font-mono text-red-400 leading-relaxed mt-2">
							<div class="flex items-center gap-1.5 font-bold mb-1">
								<Icon icon="ph:warning-octagon-bold" class="text-sm" />
								Connection Failed
							</div>
							{status.last_error}
						</div>
					{:else}
						<div class="py-6 flex flex-col items-center justify-center gap-2 text-slate-550 text-center">
							<Icon icon="ph:plugs-x-bold" class="text-2xl text-slate-700" />
							<span class="text-xs font-semibold text-slate-400">Tunnel is Closed</span>
							<p class="text-[10px] text-slate-500 max-w-xs leading-normal">Your node is currently private. Local addresses will only be advertised to peers on your local LAN.</p>
						</div>
					{/if}
				</div>

				<!-- Controls -->
				{#if status.authtoken_configured}
					<div class="mt-6 pt-6 border-t border-slate-850 flex items-center justify-between gap-4 z-10">
						<button 
							onclick={() => configureTunnel('uninstall')}
							disabled={actionPending}
							class="px-4 py-2 rounded-xl border border-slate-800 hover:border-red-900/30 hover:bg-red-950/10 text-slate-400 hover:text-red-400 text-xs font-semibold transition-all duration-300 disabled:opacity-50"
						>
							Uninstall Tunnel
						</button>

						<div class="flex gap-3">
							{#if status.enabled}
								<button 
									onclick={() => configureTunnel('stop')}
									disabled={actionPending}
									class="px-4 py-2 rounded-xl bg-slate-800 hover:bg-slate-750 text-slate-200 border border-slate-750 text-xs font-semibold transition-all duration-300 disabled:opacity-50"
								>
									Disable Tunnel
								</button>
							{:else}
								<button 
									onclick={() => configureTunnel('start')}
									disabled={actionPending}
									class="px-4 py-2 rounded-xl bg-cyan-600 hover:bg-cyan-500 text-slate-950 border border-cyan-500/20 text-xs font-bold transition-all duration-300 disabled:opacity-50 active:scale-[0.98]"
								>
									Enable Tunnel
								</button>
							{/if}
						</div>
					</div>
				{/if}
			</div>

			<!-- Configuration Bento Card -->
			<div class="bg-slate-900 border border-slate-850 rounded-2xl p-6 flex flex-col gap-4 shadow-xl backdrop-blur-sm relative overflow-hidden group">
				<h3 class="font-bold text-xs text-slate-400 font-mono uppercase tracking-wider">Configuration</h3>

				{#if !status.authtoken_configured}
					<div class="flex flex-col gap-3.5 mt-1">
						<p class="text-[11px] text-slate-450 leading-relaxed font-mono">
							Programmatic tunneling leverages ngrok's secure channels without installing any local CLI client.
						</p>
						<p class="text-[10px] text-slate-500 leading-normal">
							To register a free account and get your token, visit <a href="https://dashboard.ngrok.com" target="_blank" class="text-cyan-400 hover:underline">ngrok dashboard</a>.
						</p>

						<div class="flex flex-col gap-2 mt-2">
							<label for="authtoken" class="text-[9px] font-mono text-slate-550 uppercase tracking-wider">ngrok Authtoken</label>
							<div class="relative">
								<input 
									id="authtoken"
									type={showToken ? 'text' : 'password'}
									bind:value={authtokenInput}
									placeholder="2c9Y..."
									class="w-full pl-3 pr-10 py-2.5 rounded-xl bg-slate-950 border border-slate-850 font-mono text-xs text-slate-200 placeholder-slate-850 focus:outline-none focus:border-cyan-500 transition-colors"
								/>
								<button 
									onclick={() => showToken = !showToken}
									class="absolute right-3 top-2.5 text-slate-500 hover:text-slate-300"
									title={showToken ? 'Hide Token' : 'Show Token'}
								>
									<Icon icon={showToken ? 'ph:eye-slash-bold' : 'ph:eye-bold'} class="text-base" />
								</button>
							</div>
						</div>

						<button 
							onclick={() => configureTunnel('install')}
							disabled={actionPending || !authtokenInput.trim()}
							class="w-full py-2.5 rounded-xl bg-cyan-600 hover:bg-cyan-500 text-slate-950 border border-cyan-500/20 text-xs font-bold transition-all duration-300 disabled:opacity-40 disabled:cursor-not-allowed active:scale-[0.98] mt-2 flex items-center justify-center gap-2"
						>
							{#if actionPending}
								<Icon icon="eos-icons:loading" class="text-base" />
								Connecting...
							{:else}
								<Icon icon="ph:rocket-launch-bold" class="text-base" />
								Install & Enable
							{/if}
						</button>
					</div>
				{:else if isReplacingToken}
					<div class="flex flex-col gap-3.5 mt-1">
						<p class="text-[11px] text-slate-450 leading-relaxed font-mono">
							Enter your new ngrok Authtoken below.
						</p>

						<div class="flex flex-col gap-2 mt-2">
							<label for="new_authtoken" class="text-[9px] font-mono text-slate-555 uppercase tracking-wider">New ngrok Authtoken</label>
							<div class="relative">
								<input 
									id="new_authtoken"
									type={showToken ? 'text' : 'password'}
									bind:value={authtokenInput}
									placeholder="2c9Y..."
									class="w-full pl-3 pr-10 py-2.5 rounded-xl bg-slate-950 border border-slate-850 font-mono text-xs text-slate-200 placeholder-slate-850 focus:outline-none focus:border-cyan-500 transition-colors"
								/>
								<button 
									onclick={() => showToken = !showToken}
									class="absolute right-3 top-2.5 text-slate-500 hover:text-slate-300"
									title={showToken ? 'Hide Token' : 'Show Token'}
								>
									<Icon icon={showToken ? 'ph:eye-slash-bold' : 'ph:eye-bold'} class="text-base" />
								</button>
							</div>
						</div>

						<div class="flex gap-3 mt-2">
							<button 
								onclick={() => { isReplacingToken = false; authtokenInput = ''; }}
								class="flex-grow py-2 rounded-xl border border-slate-800 hover:border-slate-700 text-slate-400 hover:text-slate-250 text-xs font-semibold transition-all duration-300"
							>
								Cancel
							</button>
							<button 
								onclick={() => configureTunnel('install')}
								disabled={actionPending || !authtokenInput.trim()}
								class="flex-grow py-2 rounded-xl bg-cyan-600 hover:bg-cyan-500 text-slate-950 border border-cyan-500/20 text-xs font-bold transition-all duration-300 disabled:opacity-40 disabled:cursor-not-allowed flex items-center justify-center gap-1.5"
							>
								{#if actionPending}
									<Icon icon="eos-icons:loading" class="text-sm" />
									Saving...
								{:else}
									Save Token
								{/if}
							</button>
						</div>
					</div>
				{:else}
					<div class="flex flex-col gap-4 h-full justify-between">
						<div class="flex flex-col gap-3 font-mono text-xs">
							<div class="flex flex-col gap-1 border-b border-slate-850 pb-3">
								<span class="text-slate-500 text-[9px] uppercase tracking-wide">Authtoken</span>
								<div class="flex items-center justify-between">
									<span class="text-slate-200 font-bold flex items-center gap-1.5">
										<Icon icon="ph:lock-key-bold" class="text-emerald-500 text-sm" />
										Configured & Verified
									</span>
									<div class="flex gap-2">
										<button 
											onclick={() => { isReplacingToken = true; authtokenInput = ''; }}
											class="p-1 rounded bg-slate-950 border border-slate-850 hover:border-slate-700 text-slate-400 hover:text-slate-200 transition-colors"
											title="Change Token"
										>
											<Icon icon="ph:pencil-simple-bold" class="text-xs" />
										</button>
										<button 
											onclick={() => configureTunnel('uninstall')}
											class="p-1 rounded bg-slate-950 border border-slate-850 hover:border-red-900/30 text-slate-400 hover:text-red-400 transition-colors"
											title="Delete Token"
										>
											<Icon icon="ph:trash-bold" class="text-xs" />
										</button>
									</div>
								</div>
							</div>

							<div class="flex flex-col gap-1">
								<span class="text-slate-500 text-[9px] uppercase tracking-wide">Announce Target</span>
								<span class="text-slate-200 font-bold">libp2p tcp listen port</span>
							</div>
						</div>

						<div class="p-3.5 bg-slate-950/40 border border-slate-850 rounded-xl text-[10px] text-slate-500 leading-normal flex items-start gap-2">
							<Icon icon="ph:shield-check-bold" class="text-cyan-400 text-base shrink-0 mt-0.5" />
							<p>Programmatic tunnel initiates secure forwarding streams directly to your local libp2p endpoint. Sessions are encrypted end-to-end.</p>
						</div>
					</div>
				{/if}
			</div>
		</div>
	{/if}
</div>
