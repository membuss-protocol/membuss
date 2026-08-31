<script lang="ts">
	import { onMount } from 'svelte';
	import Icon from '@iconify/svelte';
	import { apiFetch } from '$lib/api';
	import { addToast } from '$lib/toast';
	import QRCode from 'qrcode';

	let loading = $state(true);
	let activeTab = $state<'setup' | 'exit' | 'services' | 'peers'>('setup');
	let status = $state<any>(null);

	// WireGuard Setup State
	let devices = $state<any[]>([]);
	let selectedDevice = $state('');
	let currentProfile = $state<any>(null);
	let customHost = $state('');
	let qrDataURL = $state('');
	let newDeviceName = $state('');
	let showAddDevice = $state(false);

	// Service Exposition State
	let newServiceName = $state('');
	let newServiceTarget = $state('127.0.0.1:3000');
	let newServiceDesc = $state('');
	let showExposeModal = $state(false);

	// Service Forwarding State
	let fwdLocalAddr = $state('127.0.0.1:8888');
	let fwdRemotePeer = $state('');
	let fwdRemoteService = $state('');
	let showForwardModal = $state(false);

	// Polling timer
	let pollInterval: any = null;

	async function loadStatus() {
		try {
			const res = await apiFetch<any>('/vpn/status');
			status = res;
		} catch (err: any) {
			console.error('Failed to load VPN status:', err);
		}
	}

	async function loadDevices() {
		try {
			const res = await apiFetch<any[]>('/vpn/wg/devices');
			devices = res || [];
			if (devices.length > 0) {
				const exists = devices.some(d => d.name === selectedDevice);
				if (!exists || selectedDevice === 'default' || !selectedDevice) {
					selectedDevice = devices[0].name;
					await loadProfile(selectedDevice);
				}
			} else {
				selectedDevice = '';
			}
		} catch (err: any) {
			console.error('Failed to load devices:', err);
		}
	}

	async function loadProfile(deviceName: string) {
		try {
			const res = await apiFetch<any>(`/vpn/wg/profile?device=${encodeURIComponent(deviceName)}`);
			currentProfile = res;
			if (!customHost) {
				if (typeof window !== 'undefined' && window.location.hostname !== 'localhost' && window.location.hostname !== '127.0.0.1') {
					customHost = window.location.hostname;
				} else if (res?.server_endpoint) {
					const parts = res.server_endpoint.split(':');
					customHost = parts[0] || '127.0.0.1';
				}
			}
			await renderQR();
		} catch (err: any) {
			console.error('Failed to load profile:', err);
		}
	}

	// Computed effective config text incorporating custom host/endpoint
	let effectiveConfig = $derived.by(() => {
		if (!currentProfile) return '';
		let text = currentProfile.config_text || '';
		if (customHost && currentProfile.server_endpoint) {
			const parts = currentProfile.server_endpoint.split(':');
			const port = parts.length > 1 ? parts[parts.length - 1] : '51820';
			const newEndpoint = `${customHost}:${port}`;
			text = text.replace(/Endpoint\s*=\s*.*/g, `Endpoint = ${newEndpoint}`);
		}
		return text;
	});

	$effect(() => {
		if (effectiveConfig) {
			renderQR();
		}
	});

	async function renderQR() {
		if (!effectiveConfig) return;
		try {
			qrDataURL = await QRCode.toDataURL(effectiveConfig, {
				width: 320,
				margin: 2,
				color: {
					dark: '#0c1416',
					light: '#f4efe2'
				}
			});
		} catch (err: any) {
			console.error('Failed to render QR code:', err);
		}
	}

	async function handleAddDevice() {
		if (!newDeviceName.trim()) {
			addToast('Please enter a device name', 'error');
			return;
		}
		try {
			const res = await apiFetch<any>('/vpn/wg/devices', {
				method: 'POST',
				body: JSON.stringify({ name: newDeviceName.trim() })
			});
			addToast(`Device "${res.device_name}" registered successfully!`, 'success');
			newDeviceName = '';
			showAddDevice = false;
			selectedDevice = res.device_name;
			await loadDevices();
			await loadProfile(selectedDevice);
		} catch (err: any) {
			addToast(`Failed to add device: ${err.message}`, 'error');
		}
	}

	async function handleDeleteDevice(dev: any) {
		if (!confirm(`Delete client device "${dev.name}" (${dev.virtual_ip})?`)) return;
		try {
			await apiFetch(`/vpn/wg/device?id=${encodeURIComponent(dev.id || dev.name)}`, {
				method: 'DELETE'
			});
			addToast(`Device "${dev.name}" removed.`, 'success');
			await loadDevices();
		} catch (err: any) {
			addToast(`Failed to delete device: ${err.message}`, 'error');
		}
	}

	async function handleToggleExitProvider() {
		try {
			const newState = !status?.is_exit_node;
			if (status) status.is_exit_node = newState;
			await apiFetch('/vpn/exit/toggle', {
				method: 'POST',
				body: JSON.stringify({ enabled: newState, allow_all: true })
			});
			addToast(newState ? 'Exit Node Provider enabled' : 'Exit Node Provider disabled', 'success');
			await loadStatus();
		} catch (err: any) {
			if (status) status.is_exit_node = !status.is_exit_node;
			addToast(`Failed to toggle exit provider: ${err.message}`, 'error');
		}
	}

	async function handleSelectExit(peerID: string) {
		try {
			if (status) status.selected_exit_node = peerID;
			await apiFetch('/vpn/exit/select', {
				method: 'POST',
				body: JSON.stringify({ peer_id: peerID, exit_peer_id: peerID })
			});
			addToast(peerID ? (peerID === 'auto' ? 'Auto Swarm internet egress enabled!' : `Swarm internet egress set to ${peerID.slice(0, 12)}...`) : 'Swarm routing disabled (Direct egress)', 'success');
			await loadStatus();
		} catch (err: any) {
			addToast(`Failed to set exit node: ${err.message}`, 'error');
		}
	}

	async function handleExposeService() {
		if (!newServiceName.trim() || !newServiceTarget.trim()) {
			addToast('Service name and target address are required', 'error');
			return;
		}
		try {
			await apiFetch('/vpn/services/expose', {
				method: 'POST',
				body: JSON.stringify({
					name: newServiceName.trim(),
					target_addr: newServiceTarget.trim(),
					description: newServiceDesc.trim()
				})
			});
			addToast(`Service "${newServiceName}" exposed to mesh!`, 'success');
			newServiceName = '';
			newServiceDesc = '';
			showExposeModal = false;
			await loadStatus();
		} catch (err: any) {
			addToast(`Failed to expose service: ${err.message}`, 'error');
		}
	}

	async function handleUnexposeService(name: string) {
		try {
			await apiFetch(`/vpn/services/expose?name=${encodeURIComponent(name)}`, {
				method: 'DELETE'
			});
			addToast(`Service "${name}" unexposed.`, 'success');
			await loadStatus();
		} catch (err: any) {
			addToast(`Failed to unexpose service: ${err.message}`, 'error');
		}
	}

	async function handleForwardService() {
		if (!fwdLocalAddr.trim() || !fwdRemotePeer.trim() || !fwdRemoteService.trim()) {
			addToast('Local address, remote peer ID, and service name are required', 'error');
			return;
		}
		try {
			await apiFetch('/vpn/services/forward', {
				method: 'POST',
				body: JSON.stringify({
					local_addr: fwdLocalAddr.trim(),
					remote_peer_id: fwdRemotePeer.trim(),
					remote_service: fwdRemoteService.trim()
				})
			});
			addToast(`Forwarded local port ${fwdLocalAddr} to ${fwdRemoteService}!`, 'success');
			showForwardModal = false;
			await loadStatus();
		} catch (err: any) {
			addToast(`Failed to forward service: ${err.message}`, 'error');
		}
	}

	async function handleUnforward(localAddr: string) {
		try {
			await apiFetch(`/vpn/services/forward?local_addr=${encodeURIComponent(localAddr)}`, {
				method: 'DELETE'
			});
			addToast(`Port forwarder on ${localAddr} stopped.`, 'success');
			await loadStatus();
		} catch (err: any) {
			addToast(`Failed to remove port forward: ${err.message}`, 'error');
		}
	}

	function downloadConfigFile() {
		if (!effectiveConfig) return;
		const blob = new Blob([effectiveConfig], { type: 'text/plain;charset=utf-8' });
		const url = URL.createObjectURL(blob);
		const a = document.createElement('a');
		a.href = url;
		a.download = `membuss-${selectedDevice || 'device'}.conf`;
		a.click();
		URL.revokeObjectURL(url);
		addToast('WireGuard configuration downloaded!', 'success');
	}

	async function copyConfigText() {
		if (!effectiveConfig) return;
		try {
			await navigator.clipboard.writeText(effectiveConfig);
			addToast('WireGuard configuration copied to clipboard!', 'success');
		} catch (_) {
			addToast('Failed to copy to clipboard', 'error');
		}
	}

	function formatBytes(bytes: number): string {
		if (!bytes || bytes === 0) return '0 B';
		const k = 1024;
		const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
		const i = Math.floor(Math.log(bytes) / Math.log(k));
		return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
	}

	function formatSpeed(bps: number): string {
		if (!bps || bps <= 0) return '0 B/s';
		const k = 1024;
		const sizes = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
		const i = Math.floor(Math.log(bps) / Math.log(k));
		return `${(bps / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
	}

	onMount(async () => {
		loading = true;
		await Promise.all([loadStatus(), loadDevices(), loadProfile('default')]);
		loading = false;

		pollInterval = setInterval(() => {
			loadStatus();
			loadDevices();
		}, 1500);

		return () => {
			if (pollInterval) clearInterval(pollInterval);
		};
	});
</script>

<div class="space-y-6 pb-12">
	<!-- Hero Header (Membuss Industrial Rack Bezel) -->
	<section class="animate-fade-in-up double-bezel" style="animation-delay: 0ms">
		<div class="double-bezel-inner flex flex-col items-stretch gap-6 relative overflow-hidden">
			<span class="absolute right-0 top-0 h-full w-1 bg-gradient-to-b from-cyan-400/50 to-transparent pointer-events-none"></span>

			<div class="flex flex-col gap-8 w-full lg:flex-row lg:items-center lg:justify-between">
				<div class="flex flex-col gap-2.5 max-w-xl">
					<span class="eyebrow flex items-center gap-2.5">
						<span class="relative flex h-2 w-2">
							<span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-60"></span>
							<span class="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
						</span>
						WireGuard Engine Active · Userspace Mesh Overlay
					</span>
					<h1 class="font-display text-3xl md:text-[2.4rem] leading-[1.02] text-slate-50">
						MemVPN & Decentralized<br />Mesh Tunnel
					</h1>
					<p class="text-sm text-slate-400 leading-relaxed mt-1">
						Pure-Go Userspace WireGuard VPN, Noise IK cryptographic encapsulation,
						and decentralized P2P egress routing over libp2p streams.
					</p>
					<div class="pt-2">
						<button
							onclick={() => { showAddDevice = true; }}
							class="inline-flex items-center gap-1.5 rounded-[4px] bg-cyan-400 px-3.5 py-2 text-xs font-mono font-bold text-slate-950 hover:bg-cyan-300 transition active:scale-95 cursor-pointer shadow-lg shadow-cyan-400/20"
						>
							<Icon icon="ph:plus-bold" class="h-3.5 w-3.5" />
							Add Client Device
						</button>
					</div>
				</div>

				<!-- Live node readout grid -->
				<div class="shrink-0 grid grid-cols-2 gap-px bg-white/[0.06] border border-white/[0.06] rounded-[4px] overflow-hidden">
					<div class="flex flex-col gap-1 bg-slate-900 px-5 py-4 min-w-[9.5rem]">
						<span class="eyebrow !text-[9px]">Virtual IP</span>
						<span class="font-display text-xl text-cyan-400 leading-none font-mono">{status?.virtual_ip || '10.42.0.1'}</span>
					</div>
					<div class="flex flex-col gap-1 bg-slate-900 px-5 py-4 min-w-[9.5rem]">
						<span class="eyebrow !text-[9px]">Client Devices</span>
						<span class="font-display text-xl text-slate-100 leading-none tabular-nums">{devices.length}</span>
					</div>
					<div class="flex flex-col gap-1 bg-slate-900 px-5 py-4">
						<span class="eyebrow !text-[9px]">Swarm Contribution</span>
						<span class="font-display text-xl text-emerald-400 leading-none tabular-nums">
							{formatBytes((status?.stats?.contributed_bytes_sent || 0) + (status?.stats?.contributed_bytes_recv || 0))}
						</span>
					</div>
					<div class="flex flex-col gap-1 bg-slate-900 px-5 py-4">
						<span class="eyebrow !text-[9px]">WG Endpoint</span>
						<span class="font-display text-xs text-slate-200 leading-none font-mono truncate">{status?.wg_server_endpoint || '0.0.0.0:51820'}</span>
					</div>
				</div>
			</div>

			<div class="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-2 text-[10px] font-mono text-slate-500 border-t border-white/[0.06] pt-4 w-full">
				<div>SERVER PUBLIC KEY: <span class="text-slate-300 select-all font-bold">{status?.wg_server_public_key || 'Loading...'}</span></div>
				<div>SWARM EGRESS: <span class="text-slate-300 font-bold">{status?.selected_exit_node ? `Routed via Exit (${status.selected_exit_node.slice(0, 16)}...)` : 'Local Direct Egress'}</span></div>
			</div>
		</div>
	</section>

	<!-- Bandwidth & Swarm Contribution Telemetry Dashboard -->
	<div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
		<!-- Card 1: Swarm Contribution -->
		<div class="animate-fade-in-up double-bezel" style="animation-delay: 50ms">
			<div class="double-bezel-inner flex flex-col gap-3">
				<div class="flex items-center justify-between border-b border-white/[0.04] pb-2.5">
					<span class="eyebrow flex items-center gap-1.5 !text-[9px]">
						<Icon icon="ph:globe-hemisphere-east-fill" class="h-3.5 w-3.5 text-cyan-400" />
						Swarm Contribution
					</span>
					<span class="rounded-[2px] px-1.5 py-0.5 text-[9px] font-mono font-bold bg-cyan-400/10 text-cyan-400 border border-cyan-400/20">
						{(status?.stats?.contribution_ratio || 0).toFixed(2)}x
					</span>
				</div>
				<div>
					<div class="font-display text-2xl text-cyan-400 font-mono tabular-nums leading-none">
						{formatBytes((status?.stats?.contributed_bytes_sent || 0) + (status?.stats?.contributed_bytes_recv || 0))}
					</div>
					<div class="flex items-center gap-3 text-[11px] text-slate-400 font-mono mt-2">
						<span class="text-emerald-400">&uarr; {formatBytes(status?.stats?.contributed_bytes_sent || 0)}</span>
						<span class="text-slate-300">&darr; {formatBytes(status?.stats?.contributed_bytes_recv || 0)}</span>
					</div>
				</div>
				<p class="text-[10px] font-mono text-slate-500">
					{status?.stats?.contributed_conns || 0} swarm relay requests served
				</p>
			</div>
		</div>

		<!-- Card 2: Connected Devices -->
		<div class="animate-fade-in-up double-bezel" style="animation-delay: 100ms">
			<div class="double-bezel-inner flex flex-col gap-3">
				<div class="flex items-center justify-between border-b border-white/[0.04] pb-2.5">
					<span class="eyebrow flex items-center gap-1.5 !text-[9px]">
						<Icon icon="ph:device-mobile-camera-fill" class="h-3.5 w-3.5 text-slate-300" />
						Client Devices
					</span>
					<span class="rounded-[2px] px-1.5 py-0.5 text-[9px] font-mono font-bold bg-white/[0.06] text-slate-200 border border-white/[0.08]">
						{devices.length} Paired
					</span>
				</div>
				<div>
					<div class="font-display text-2xl text-slate-100 font-mono tabular-nums leading-none">
						{formatBytes((status?.stats?.client_bytes_sent || 0) + (status?.stats?.client_bytes_recv || 0))}
					</div>
					<div class="flex items-center gap-3 text-[11px] text-slate-400 font-mono mt-2">
						<span class="text-cyan-400">&uarr; {formatBytes(status?.stats?.client_bytes_sent || 0)}</span>
						<span class="text-emerald-400">&darr; {formatBytes(status?.stats?.client_bytes_recv || 0)}</span>
					</div>
				</div>
				<p class="text-[10px] font-mono text-slate-500">
					{status?.stats?.client_conns || 0} active local WireGuard flows
				</p>
			</div>
		</div>

		<!-- Card 3: Live Throughput (Clean Rack Meter) -->
		<div class="animate-fade-in-up double-bezel" style="animation-delay: 150ms">
			<div class="double-bezel-inner flex flex-col gap-3">
				<div class="flex items-center justify-between border-b border-white/[0.04] pb-2.5">
					<span class="eyebrow flex items-center gap-2 !text-[9px]">
						<span class="relative flex h-1.5 w-1.5">
							<span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-60"></span>
							<span class="relative inline-flex rounded-full h-1.5 w-1.5 bg-emerald-500"></span>
						</span>
						Live Throughput
					</span>
					<span class="text-[9px] font-mono text-slate-500 uppercase tracking-widest">Rate</span>
				</div>
				<div>
					<div class="flex items-baseline justify-between font-mono">
						<div class="flex items-baseline gap-1">
							<span class="text-[10px] text-cyan-400 font-bold">&darr;</span>
							<span class="font-display text-lg text-slate-100 tabular-nums">{formatSpeed(status?.stats?.current_download_bps || 0)}</span>
						</div>
						<div class="flex items-baseline gap-1">
							<span class="text-[10px] text-emerald-400 font-bold">&uarr;</span>
							<span class="font-display text-lg text-slate-100 tabular-nums">{formatSpeed(status?.stats?.current_upload_bps || 0)}</span>
						</div>
					</div>
					<div class="w-full bg-white/[0.06] h-1.5 rounded-[2px] mt-2.5 overflow-hidden">
						<div
							class="bg-gradient-to-r from-cyan-400 to-emerald-400 h-full rounded-[2px] transition-all duration-300"
							style="width: {Math.min(100, Math.max(4, ((status?.stats?.current_download_bps || 0) + (status?.stats?.current_upload_bps || 0)) / 1024 / 50))}%"
						></div>
					</div>
				</div>
				<p class="text-[10px] font-mono text-slate-500">
					Real-time wire transfer rate over stack
				</p>
			</div>
		</div>

		<!-- Card 4: DNS & Protocol Inspector -->
		<div class="animate-fade-in-up double-bezel" style="animation-delay: 200ms">
			<div class="double-bezel-inner flex flex-col gap-3">
				<div class="flex items-center justify-between border-b border-white/[0.04] pb-2.5">
					<span class="eyebrow flex items-center gap-1.5 !text-[9px]">
						<Icon icon="ph:shield-check-fill" class="h-3.5 w-3.5 text-emerald-400" />
						Protocol Inspector
					</span>
					<span class="rounded-[2px] px-1.5 py-0.5 text-[9px] font-mono font-bold bg-emerald-400/10 text-emerald-400 border border-emerald-400/20">
						Active
					</span>
				</div>
				<div>
					<div class="font-display text-2xl text-slate-100 font-mono tabular-nums leading-none">
						{status?.stats?.dns_queries_count || 0} <span class="text-xs font-normal text-slate-400 font-sans">DNS Queries</span>
					</div>
					<div class="flex items-center gap-3 text-[11px] text-slate-400 font-mono mt-2">
						<span>TCP: <strong class="text-slate-200">{status?.stats?.tcp_conns_count || 0}</strong></span>
						<span>UDP: <strong class="text-slate-200">{status?.stats?.udp_flows_count || 0}</strong></span>
					</div>
				</div>
				<p class="text-[10px] font-mono text-slate-500">
					Anti-leak DNS & packet inspection active
				</p>
			</div>
		</div>
	</div>

	<!-- Navigation Tabs (Membuss Rack Console Tabs) -->
	<div class="flex border-b border-white/[0.06] space-x-1">
		<button
			onclick={() => activeTab = 'setup'}
			class="flex items-center gap-2 px-4 py-3 text-xs font-mono font-semibold uppercase tracking-wider border-b-2 transition cursor-pointer {activeTab === 'setup' ? 'border-cyan-400 text-cyan-400' : 'border-transparent text-slate-400 hover:text-slate-200'}"
		>
			<Icon icon="ph:device-mobile-camera-bold" class="h-4 w-4" />
			WireGuard Setup
		</button>
		<button
			onclick={() => activeTab = 'exit'}
			class="flex items-center gap-2 px-4 py-3 text-xs font-mono font-semibold uppercase tracking-wider border-b-2 transition cursor-pointer {activeTab === 'exit' ? 'border-cyan-400 text-cyan-400' : 'border-transparent text-slate-400 hover:text-slate-200'}"
		>
			<Icon icon="ph:globe-hemisphere-west-bold" class="h-4 w-4" />
			Exit Swarm
		</button>
		<button
			onclick={() => activeTab = 'services'}
			class="flex items-center gap-2 px-4 py-3 text-xs font-mono font-semibold uppercase tracking-wider border-b-2 transition cursor-pointer {activeTab === 'services' ? 'border-cyan-400 text-cyan-400' : 'border-transparent text-slate-400 hover:text-slate-200'}"
		>
			<Icon icon="ph:plugs-connected-bold" class="h-4 w-4" />
			P2P Service Mesh
		</button>
		<button
			onclick={() => activeTab = 'peers'}
			class="flex items-center gap-2 px-4 py-3 text-xs font-mono font-semibold uppercase tracking-wider border-b-2 transition cursor-pointer {activeTab === 'peers' ? 'border-cyan-400 text-cyan-400' : 'border-transparent text-slate-400 hover:text-slate-200'}"
		>
			<Icon icon="ph:users-three-bold" class="h-4 w-4" />
			Mesh Peers
		</button>
	</div>

	<!-- Tab 1: WireGuard 1-Click Setup -->
	{#if activeTab === 'setup'}
		<div class="grid grid-cols-1 lg:grid-cols-12 gap-6">
			<!-- Left: Big QR Code Card (Rack Bezel) -->
			<div class="animate-fade-in-up double-bezel lg:col-span-5" style="animation-delay: 50ms">
				<div class="double-bezel-inner flex flex-col items-center justify-center text-center gap-5">
					<div class="w-full flex items-center justify-between border-b border-white/[0.04] pb-3">
						<div class="text-left">
							<h3 class="font-bold text-xs text-slate-200 font-mono uppercase tracking-wider flex items-center gap-2">
								<Icon icon="ph:qr-code-bold" class="text-cyan-400 h-4 w-4" />
								WireGuard QR Code
							</h3>
							<p class="text-[10px] text-slate-400 mt-0.5">Scan with official WireGuard app on iOS/Android</p>
						</div>

						<!-- Device Selector Dropdown + Quick Add -->
						<div class="flex items-center gap-1.5">
							<select
								bind:value={selectedDevice}
								onchange={() => { if (selectedDevice) loadProfile(selectedDevice); }}
								class="rounded-[4px] bg-slate-900 border border-white/[0.1] px-2.5 py-1 text-xs font-mono font-semibold text-slate-200 focus:outline-none focus:border-cyan-400 shadow-inner cursor-pointer"
							>
								{#if devices.length === 0}
									<option value="" disabled selected>No devices registered</option>
								{:else}
									{#each devices as dev}
										<option value={dev.name}>
											{dev.name} ({dev.virtual_ip}) {dev.connected ? '● Online' : '○ Standby'}
										</option>
									{/each}
								{/if}
							</select>
							<button
								onclick={() => { showAddDevice = true; }}
								title="Add new client device"
								class="inline-flex items-center gap-1 rounded-[4px] bg-cyan-400/10 border border-cyan-400/30 px-2 py-1 text-xs font-mono font-bold text-cyan-400 hover:bg-cyan-400/20 transition cursor-pointer"
							>
								<Icon icon="ph:plus-bold" class="h-3 w-3" />
								New
							</button>
						</div>
					</div>

					<!-- QR Code Container -->
					<div class="p-3 rounded-[4px] bg-[#f4efe2] shadow-2xl border border-white/[0.1]">
						{#if qrDataURL}
							<img src={qrDataURL} alt="WireGuard Setup QR Code" class="w-60 h-60 object-contain rounded-[2px]" />
						{:else}
							<div class="w-60 h-60 flex items-center justify-center text-slate-900 text-xs font-mono">
								Generating QR...
							</div>
						{/if}
					</div>

					<div class="w-full flex items-center gap-2">
						<button
							onclick={downloadConfigFile}
							class="flex-1 inline-flex items-center justify-center gap-2 rounded-[4px] bg-cyan-400/10 border border-cyan-400/30 px-3 py-2 text-xs font-mono font-semibold text-cyan-400 hover:bg-cyan-400/20 transition active:scale-95 cursor-pointer"
						>
							<Icon icon="ph:download-simple-bold" class="h-3.5 w-3.5" />
							Download .conf
						</button>
						<button
							onclick={copyConfigText}
							class="inline-flex items-center justify-center gap-2 rounded-[4px] bg-slate-900 border border-white/[0.08] px-3 py-2 text-xs font-mono font-semibold text-slate-300 hover:bg-white/[0.04] transition active:scale-95 cursor-pointer"
						>
							<Icon icon="ph:copy-bold" class="h-3.5 w-3.5" />
							Copy Config
						</button>
					</div>
				</div>
			</div>

			<!-- Right: 3-Step Setup Instructions & Endpoint Config -->
			<div class="lg:col-span-7 space-y-6">
				<!-- Step by Step Card -->
				<div class="animate-fade-in-up double-bezel" style="animation-delay: 100ms">
					<div class="double-bezel-inner flex flex-col gap-4">
						<h3 class="font-bold text-xs text-slate-200 font-mono uppercase tracking-wider flex items-center gap-2 border-b border-white/[0.04] pb-3">
							<Icon icon="ph:lightning-bold" class="text-cyan-400 h-4 w-4" />
							3-Step Instant Mobile & Desktop Connection
						</h3>

						<div class="space-y-3 text-sm">
							<div class="flex items-start gap-3 p-3 rounded-[4px] bg-slate-900 border border-white/[0.06]">
								<div class="flex h-6 w-6 shrink-0 items-center justify-center rounded-[2px] bg-cyan-400/10 text-cyan-400 font-mono font-bold text-xs border border-cyan-400/20">
									1
								</div>
								<div>
									<p class="font-semibold text-xs text-slate-200 font-mono">Install the official WireGuard App</p>
									<p class="text-xs text-slate-400 mt-0.5">
										Download free on <strong class="text-slate-300">iOS App Store</strong>, <strong class="text-slate-300">Google Play Store</strong>, or desktop.
									</p>
								</div>
							</div>

							<div class="flex items-start gap-3 p-3 rounded-[4px] bg-slate-900 border border-white/[0.06]">
								<div class="flex h-6 w-6 shrink-0 items-center justify-center rounded-[2px] bg-cyan-400/10 text-cyan-400 font-mono font-bold text-xs border border-cyan-400/20">
									2
								</div>
								<div>
									<p class="font-semibold text-xs text-slate-200 font-mono">Scan QR Code or Import Tunnel</p>
									<p class="text-xs text-slate-400 mt-0.5">
										Open the WireGuard app, tap <code class="text-cyan-400 font-mono text-[11px] bg-white/[0.06] px-1 py-0.5 rounded-[2px]">+</code> &rarr; <strong class="text-slate-300">Create from QR code</strong>, and scan the QR code on the left.
									</p>
								</div>
							</div>

							<div class="flex items-start gap-3 p-3 rounded-[4px] bg-slate-900 border border-white/[0.06]">
								<div class="flex h-6 w-6 shrink-0 items-center justify-center rounded-[2px] bg-cyan-400/10 text-cyan-400 font-mono font-bold text-xs border border-cyan-400/20">
									3
								</div>
								<div>
									<p class="font-semibold text-xs text-slate-200 font-mono">Turn VPN On & Enjoy Total Protection</p>
									<p class="text-xs text-slate-400 mt-0.5">
										Activate the tunnel toggle. 100% of all apps, browsers, streaming, and DNS are instantly routed!
									</p>
								</div>
							</div>
						</div>

						<!-- Endpoint Host Override -->
						<div class="pt-2 border-t border-white/[0.06]">
							<label class="block text-[11px] font-mono text-slate-400 mb-1">
								Endpoint Host / LAN IP (Auto-detected for Wi-Fi devices):
							</label>
							<div class="flex items-center gap-2">
								<input
									type="text"
									bind:value={customHost}
									placeholder="e.g. 192.168.1.104 or mynode.local"
									class="flex-1 rounded-[4px] bg-slate-950 border border-white/[0.1] px-3 py-1.5 text-xs font-mono text-cyan-400 focus:outline-none focus:border-cyan-400"
								/>
								<span class="text-xs text-slate-500 font-mono">:51820</span>
							</div>
						</div>
					</div>
				</div>

				<!-- Connected Devices Table (Rack Console Table) -->
				<div class="animate-fade-in-up double-bezel" style="animation-delay: 150ms">
					<div class="double-bezel-inner flex flex-col gap-3">
						<div class="flex items-center justify-between border-b border-white/[0.04] pb-3">
							<h4 class="font-bold text-xs text-slate-200 font-mono uppercase tracking-wider flex items-center gap-2">
								<Icon icon="ph:devices-light" class="h-4 w-4 text-cyan-400" />
								Registered Client Devices ({devices.length})
							</h4>
							<button
								onclick={() => { showAddDevice = true; }}
								class="text-xs font-mono font-semibold text-cyan-400 hover:text-cyan-300 flex items-center gap-1 cursor-pointer"
							>
								<Icon icon="ph:plus-bold" class="h-3 w-3" />
								New Device
							</button>
						</div>

						<div class="overflow-x-auto">
							<table class="w-full text-left text-xs font-mono">
								<thead>
									<tr class="border-b border-white/[0.06] text-slate-500 text-[10px] uppercase tracking-wider">
										<th class="py-2">Device Name</th>
										<th class="py-2">Virtual IP</th>
										<th class="py-2">Status</th>
										<th class="py-2">Transferred</th>
										<th class="py-2 text-right">Actions</th>
									</tr>
								</thead>
								<tbody class="divide-y divide-white/[0.04] text-slate-300">
									{#each devices as dev}
										<tr class="hover:bg-white/[0.02] transition">
											<td class="py-2.5 font-bold text-slate-100 flex items-center gap-2">
												<Icon icon={dev.connected ? 'ph:device-mobile-camera-fill' : 'ph:device-mobile-camera-light'} class="h-4 w-4 {dev.connected ? 'text-emerald-400' : 'text-slate-600'}" />
												{dev.name}
											</td>
											<td class="py-2.5 text-cyan-400">{dev.virtual_ip}</td>
											<td class="py-2.5">
												{#if dev.connected}
													<span class="inline-flex items-center gap-1 rounded-[2px] px-1.5 py-0.5 text-[9px] font-bold bg-emerald-400/10 text-emerald-400 border border-emerald-400/20">
														<span class="h-1.5 w-1.5 rounded-full bg-emerald-400"></span>
														Connected
													</span>
												{:else}
													<span class="inline-flex items-center rounded-[2px] px-1.5 py-0.5 text-[9px] font-medium bg-white/[0.04] text-slate-500">
														Offline / Standby
													</span>
												{/if}
											</td>
											<td class="py-2.5 text-slate-400 text-[11px] tabular-nums">
												&uarr; {formatBytes(dev.bytes_sent)} &middot; &darr; {formatBytes(dev.bytes_recv)}
											</td>
											<td class="py-2.5 text-right space-x-2 text-[11px]">
												<button
													onclick={() => { selectedDevice = dev.name; loadProfile(dev.name); }}
													class="text-cyan-400 hover:text-cyan-300 font-semibold cursor-pointer"
												>
													View QR
												</button>
												<button
													onclick={() => handleDeleteDevice(dev)}
													class="text-red-400 hover:text-red-300 font-semibold cursor-pointer"
												>
													Delete
												</button>
											</td>
										</tr>
									{/each}
									{#if devices.length === 0}
										<tr>
											<td colspan="5" class="py-4 text-center text-slate-500">
												No devices registered yet. Click "New Device" above!
											</td>
										</tr>
									{/if}
								</tbody>
							</table>
						</div>
					</div>
				</div>
			</div>
		</div>
	{/if}

	<!-- Tab 2: Swarm Exit Nodes -->
	{#if activeTab === 'exit'}
		<div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
			<div class="lg:col-span-2 space-y-6">
				<div class="animate-fade-in-up double-bezel" style="animation-delay: 50ms">
					<div class="double-bezel-inner flex flex-col gap-4">
						<div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-white/[0.04] pb-3">
							<div>
								<div class="flex items-center gap-2">
									<h3 class="font-bold text-xs text-slate-200 font-mono uppercase tracking-wider flex items-center gap-2">
										<Icon icon="ph:globe-hemisphere-west-bold" class="text-cyan-400 h-4 w-4" />
										Decentralized Internet Exit Swarm
									</h3>
									{#if status?.selected_exit_node}
										<span class="rounded-[2px] bg-cyan-400/20 text-cyan-400 border border-cyan-400/30 px-1.5 py-0.5 text-[9px] font-mono font-bold flex items-center gap-1">
											<span class="h-1.5 w-1.5 rounded-full bg-cyan-400 animate-ping"></span>
											{status.selected_exit_node === 'auto' ? 'Auto Swarm Active' : 'Exit Active'}
										</span>
									{/if}
								</div>
								<p class="text-[10px] text-slate-400 mt-0.5">Route all internet traffic through trusted or community exit nodes</p>
							</div>

							<button
								onclick={() => handleSelectExit(status?.selected_exit_node ? '' : 'auto')}
								class="inline-flex items-center gap-1.5 rounded-[4px] px-3.5 py-2 text-xs font-mono font-bold transition cursor-pointer {status?.selected_exit_node ? 'bg-cyan-400 text-slate-950 hover:bg-cyan-300 shadow-md shadow-cyan-400/20' : 'bg-slate-900 border border-white/[0.12] text-slate-200 hover:bg-white/[0.06]'}"
							>
								<Icon icon="ph:power-bold" class="h-3.5 w-3.5" />
								{status?.selected_exit_node ? 'Disable Swarm Routing' : 'Enable Auto Exit Swarm'}
							</button>
						</div>

						{#if status?.selected_exit_node === 'auto'}
							<div class="flex items-start gap-2.5 p-3 rounded-[4px] bg-cyan-400/10 border border-cyan-400/30 text-cyan-400 text-xs font-mono">
								<span class="relative flex h-2 w-2 mt-0.5">
									<span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-cyan-400 opacity-75"></span>
									<span class="relative inline-flex rounded-full h-2 w-2 bg-cyan-400"></span>
								</span>
								<div>
									<span class="font-bold">Auto Swarm Routing Active</span>
									<p class="text-[10px] text-slate-300 mt-0.5 font-sans">
										Egress traffic from all your paired WireGuard devices is dynamically routed through the fastest available decentralized exit nodes on the mesh.
									</p>
								</div>
							</div>
						{/if}

						<div class="space-y-2.5">
							{#each (status?.exit_nodes || []) as exit}
								<div class="flex items-center justify-between p-3.5 rounded-[4px] bg-slate-900 border {exit.selected ? 'border-cyan-400/50 bg-cyan-400/5' : 'border-white/[0.06]'}">
									<div class="space-y-1">
										<div class="flex items-center gap-2">
											<span class="font-bold text-xs text-slate-100 font-mono">{exit.node_name || 'Exit Gateway'}</span>
											{#if exit.selected}
												<span class="rounded-[2px] bg-cyan-400/20 text-cyan-400 border border-cyan-400/30 px-1.5 py-0.5 text-[9px] font-mono font-bold">
													Active Egress
												</span>
											{/if}
										</div>
										<div class="flex items-center gap-3 text-[11px] text-slate-400 font-mono">
											<span>{exit.peer_id.slice(0, 16)}...</span>
											<span>VIP: {exit.virtual_ip}</span>
											<span class="text-emerald-400 font-semibold">{exit.latency_ms}ms</span>
										</div>
									</div>

									<button
										onclick={() => handleSelectExit(exit.selected ? '' : exit.peer_id)}
										class="rounded-[4px] px-3 py-1.5 text-xs font-mono font-semibold transition cursor-pointer {exit.selected ? 'bg-red-400/20 text-red-300 hover:bg-red-400/30 border border-red-400/30' : 'bg-cyan-400 text-slate-950 font-bold hover:bg-cyan-300'}"
									>
										{exit.selected ? 'Disconnect' : 'Route via this Exit'}
									</button>
								</div>
							{/each}

							{#if (!status?.exit_nodes || status.exit_nodes.length === 0)}
								<div class="p-8 text-center rounded-[4px] bg-slate-900 border border-dashed border-white/[0.08] text-slate-400 space-y-2">
									<Icon icon="ph:broadcast-light" class="h-6 w-6 mx-auto text-slate-500" />
									<p class="text-xs font-mono font-semibold text-slate-300">
										{status?.selected_exit_node === 'auto' ? 'Scanning Mesh for Exit Provider Nodes...' : 'No remote exit nodes currently online in this mesh'}
									</p>
									<p class="text-[11px] text-slate-500">
										{status?.selected_exit_node === 'auto' ? 'Your node is standing by and will seamlessly tunnel traffic as soon as a provider appears.' : 'Enable provider mode on another node to route traffic through it.'}
									</p>
								</div>
							{/if}
						</div>
					</div>
				</div>
			</div>

			<!-- Exit Node Provider Settings -->
			<div class="space-y-6">
				<div class="animate-fade-in-up double-bezel" style="animation-delay: 100ms">
					<div class="double-bezel-inner flex flex-col gap-4">
						<div class="flex items-center justify-between border-b border-white/[0.04] pb-3">
							<h3 class="font-bold text-xs text-slate-200 font-mono uppercase tracking-wider flex items-center gap-2">
								<Icon icon="ph:shield-chevron-bold" class="text-emerald-400 h-4 w-4" />
								Exit Provider Settings
							</h3>
							{#if status?.is_exit_node}
								<span class="rounded-[2px] bg-emerald-400/20 text-emerald-400 border border-emerald-400/30 px-1.5 py-0.5 text-[9px] font-mono font-bold flex items-center gap-1">
									<span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-ping"></span>
									Active Provider
								</span>
							{/if}
						</div>
						<p class="text-xs text-slate-400">Share your node's internet connection with your mesh devices.</p>

						{#if status?.is_exit_node}
							<div class="p-3 rounded-[4px] bg-emerald-400/10 border border-emerald-400/30 space-y-1.5">
								<div class="flex items-center justify-between">
									<div class="font-mono font-bold text-xs text-emerald-400 flex items-center gap-2">
										<span class="relative flex h-2 w-2">
											<span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
											<span class="relative inline-flex rounded-full h-2 w-2 bg-emerald-400"></span>
										</span>
										Exit Node Provider Active
									</div>
									<span class="text-[9px] font-mono text-emerald-400/80 uppercase">Broadcasting</span>
								</div>
								<p class="text-[10px] text-slate-300 font-mono leading-relaxed">
									Your node is broadcasting its egress capability to the mesh. Other nodes can securely tunnel outbound traffic through your connection.
								</p>
							</div>
						{/if}

						<div class="flex items-center justify-between p-3 rounded-[4px] bg-slate-900 border border-white/[0.06]">
							<div>
								<p class="font-semibold text-xs text-slate-200 font-mono">Act as Exit Node Provider</p>
								<p class="text-[11px] text-slate-400">Forward traffic to external internet</p>
							</div>
							<input
								type="checkbox"
								checked={status?.is_exit_node}
								onchange={handleToggleExitProvider}
								class="h-4 w-4 rounded-[2px] border-white/[0.2] bg-slate-950 text-cyan-400 focus:ring-cyan-400 cursor-pointer"
							/>
						</div>

						<div class="p-3 rounded-[4px] bg-emerald-400/10 border border-emerald-400/20 text-xs text-emerald-400 space-y-1">
							<div class="font-mono font-bold flex items-center gap-1.5">
								<Icon icon="ph:lock-key-bold" />
								Private IP Firewall Active
							</div>
							<p class="text-[10px] text-emerald-400/80 leading-relaxed font-mono">
								RFC1918 private LAN ranges and loopback destinations are strictly blocked to protect local networks.
							</p>
						</div>
					</div>
				</div>
			</div>
		</div>
	{/if}

	<!-- Tab 3: P2P Service Mesh -->
	{#if activeTab === 'services'}
		<div class="space-y-6">
			<div class="flex items-center justify-between">
				<div>
					<h3 class="font-display text-xl text-slate-100">Exposed Mesh Services & Port Forwards</h3>
					<p class="text-xs text-slate-400 font-mono mt-0.5">Expose local web servers, databases, and APIs directly over encrypted P2P streams</p>
				</div>
				<div class="flex items-center gap-2">
					<button
						onclick={() => { showExposeModal = true; }}
						class="inline-flex items-center gap-1.5 rounded-[4px] bg-cyan-400 px-3 py-1.5 text-xs font-mono font-bold text-slate-950 hover:bg-cyan-300 transition cursor-pointer"
					>
						<Icon icon="ph:plus-bold" class="h-3 w-3" />
						Expose Local Port
					</button>
					<button
						onclick={() => { showForwardModal = true; }}
						class="inline-flex items-center gap-1.5 rounded-[4px] bg-slate-900 border border-white/[0.08] px-3 py-1.5 text-xs font-mono font-semibold text-slate-200 hover:bg-white/[0.04] transition cursor-pointer"
					>
						<Icon icon="ph:arrow-square-out-bold" class="h-3 w-3" />
						Forward Remote Port
					</button>
				</div>
			</div>

			<div class="grid grid-cols-1 md:grid-cols-2 gap-6">
				<!-- Exposed Services -->
				<div class="animate-fade-in-up double-bezel" style="animation-delay: 50ms">
					<div class="double-bezel-inner flex flex-col gap-4">
						<h4 class="font-bold text-xs text-slate-200 font-mono uppercase tracking-wider flex items-center gap-2 border-b border-white/[0.04] pb-3">
							<Icon icon="ph:broadcast-bold" class="text-cyan-400 h-4 w-4" />
							Local Services Exposed to Mesh ({status?.services?.length || 0})
						</h4>

						<div class="space-y-2.5">
							{#each (status?.services || []) as svc}
								<div class="flex items-center justify-between p-3 rounded-[4px] bg-slate-900 border border-white/[0.06]">
									<div>
										<div class="flex items-center gap-2 font-mono">
											<span class="font-bold text-xs text-slate-100">{svc.name}</span>
											<span class="text-[11px] text-cyan-400">&rarr; {svc.target_addr}</span>
										</div>
										{#if svc.description}
											<p class="text-[11px] text-slate-400 mt-0.5">{svc.description}</p>
										{/if}
									</div>
									<button
										onclick={() => handleUnexposeService(svc.name)}
										class="text-xs font-mono font-semibold text-red-400 hover:text-red-300 cursor-pointer"
									>
										Unexpose
									</button>
								</div>
							{/each}
							{#if (!status?.services || status.services.length === 0)}
								<p class="text-xs font-mono text-slate-500 py-4 text-center">No local services exposed yet.</p>
							{/if}
						</div>
					</div>
				</div>

				<!-- Port Forwards -->
				<div class="animate-fade-in-up double-bezel" style="animation-delay: 100ms">
					<div class="double-bezel-inner flex flex-col gap-4">
						<h4 class="font-bold text-xs text-slate-200 font-mono uppercase tracking-wider flex items-center gap-2 border-b border-white/[0.04] pb-3">
							<Icon icon="ph:arrows-left-right-bold" class="text-emerald-400 h-4 w-4" />
							Active Port Forwards ({status?.forwards?.length || 0})
						</h4>

						<div class="space-y-2.5">
							{#each (status?.forwards || []) as fwd}
								<div class="flex items-center justify-between p-3 rounded-[4px] bg-slate-900 border border-white/[0.06]">
									<div>
										<div class="flex items-center gap-2 font-mono">
											<span class="font-bold text-xs text-slate-100">{fwd.local_addr}</span>
											<span class="text-slate-400 text-xs">&rarr; {fwd.remote_service}</span>
										</div>
										<p class="font-mono text-[10px] text-slate-500 mt-0.5">Peer: {fwd.remote_peer_id.slice(0, 16)}...</p>
									</div>
									<button
										onclick={() => handleUnforward(fwd.local_addr)}
										class="text-xs font-mono font-semibold text-red-400 hover:text-red-300 cursor-pointer"
									>
										Stop
									</button>
								</div>
							{/each}
							{#if (!status?.forwards || status.forwards.length === 0)}
								<p class="text-xs font-mono text-slate-500 py-4 text-center">No active port forwarders.</p>
							{/if}
						</div>
					</div>
				</div>
			</div>
		</div>
	{/if}

	<!-- Tab 4: Mesh Peers & Topology -->
	{#if activeTab === 'peers'}
		<div class="animate-fade-in-up double-bezel" style="animation-delay: 50ms">
			<div class="double-bezel-inner flex flex-col gap-4">
				<h3 class="font-bold text-xs text-slate-200 font-mono uppercase tracking-wider flex items-center gap-2 border-b border-white/[0.04] pb-3">
					<Icon icon="ph:tree-structure-bold" class="text-cyan-400 h-4 w-4" />
					Connected Mesh Peers ({status?.peer_count || 0})
				</h3>

				<div class="overflow-x-auto">
					<table class="w-full text-left text-xs font-mono">
						<thead>
							<tr class="border-b border-white/[0.06] text-slate-500 text-[10px] uppercase tracking-wider">
								<th class="py-2.5">Node Name</th>
								<th class="py-2.5">Virtual IP</th>
								<th class="py-2.5">Peer ID</th>
								<th class="py-2.5">Services</th>
								<th class="py-2.5">Status</th>
							</tr>
						</thead>
						<tbody class="divide-y divide-white/[0.04] text-slate-300">
							{#each (status?.peers || []) as p}
								<tr class="hover:bg-white/[0.02] transition">
									<td class="py-2.5 font-bold text-slate-100">{p.node_name || 'Node'}</td>
									<td class="py-2.5 text-cyan-400">{p.virtual_ip}</td>
									<td class="py-2.5 text-slate-400">{p.peer_id}</td>
									<td class="py-2.5">
										<div class="flex flex-wrap gap-1">
											{#each (p.services || []) as svc}
												<span class="rounded-[2px] bg-white/[0.06] px-1.5 py-0.5 text-[10px] text-slate-300">
													{svc}
												</span>
											{/each}
										</div>
									</td>
									<td class="py-2.5">
										<span class="inline-flex items-center gap-1 rounded-[2px] px-1.5 py-0.5 text-[9px] font-bold bg-emerald-400/10 text-emerald-400 border border-emerald-400/20">
											<span class="h-1.5 w-1.5 rounded-full bg-emerald-400"></span>
											Online
										</span>
									</td>
								</tr>
							{/each}
							{#if (!status?.peers || status.peers.length === 0)}
								<tr>
									<td colspan="5" class="py-6 text-center text-slate-500">
										No mesh peers currently connected. Bootnodes and DHT discovery are actively synchronizing.
									</td>
								</tr>
							{/if}
						</tbody>
					</table>
				</div>
			</div>
		</div>
	{/if}
</div>

<!-- Modal: Add Device -->
{#if showAddDevice}
	<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm p-4">
		<div class="w-full max-w-md double-bezel">
			<div class="double-bezel-inner flex flex-col gap-4">
				<h3 class="font-bold text-xs text-slate-200 font-mono uppercase tracking-wider flex items-center gap-2 border-b border-white/[0.04] pb-3">
					<Icon icon="ph:plus-circle-bold" class="text-cyan-400" />
					Add WireGuard Device Profile
				</h3>
				<p class="text-xs text-slate-400 font-mono">Give your phone, tablet, or laptop a unique profile name.</p>

				<div>
					<label class="block text-[11px] font-mono text-slate-400 mb-1">Device Name</label>
					<input
						type="text"
						bind:value={newDeviceName}
						placeholder="e.g. My iPhone 15, Work Laptop, Android"
						class="w-full rounded-[4px] bg-slate-950 border border-white/[0.1] px-3 py-2 text-xs font-mono text-slate-100 focus:outline-none focus:border-cyan-400"
					/>
				</div>

				<div class="flex items-center justify-end gap-2 pt-2 border-t border-white/[0.06]">
					<button
						onclick={() => { showAddDevice = false; }}
						class="rounded-[4px] px-3 py-1.5 text-xs font-mono font-semibold text-slate-400 hover:text-white cursor-pointer"
					>
						Cancel
					</button>
					<button
						onclick={handleAddDevice}
						class="rounded-[4px] bg-cyan-400 px-3 py-1.5 text-xs font-mono font-bold text-slate-950 hover:bg-cyan-300 cursor-pointer"
					>
						Create Profile
					</button>
				</div>
			</div>
		</div>
	</div>
{/if}

<!-- Modal: Expose Service -->
{#if showExposeModal}
	<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm p-4">
		<div class="w-full max-w-md double-bezel">
			<div class="double-bezel-inner flex flex-col gap-4">
				<h3 class="font-bold text-xs text-slate-200 font-mono uppercase tracking-wider flex items-center gap-2 border-b border-white/[0.04] pb-3">
					<Icon icon="ph:broadcast-bold" class="text-cyan-400" />
					Expose Local Service to Mesh
				</h3>

				<div class="space-y-3">
					<div>
						<label class="block text-[11px] font-mono text-slate-400 mb-1">Service Name</label>
						<input
							type="text"
							bind:value={newServiceName}
							placeholder="e.g. webapp, postgres, ollama"
							class="w-full rounded-[4px] bg-slate-950 border border-white/[0.1] px-3 py-2 text-xs font-mono text-slate-100 focus:outline-none focus:border-cyan-400"
						/>
					</div>
					<div>
						<label class="block text-[11px] font-mono text-slate-400 mb-1">Target Address</label>
						<input
							type="text"
							bind:value={newServiceTarget}
							placeholder="127.0.0.1:3000"
							class="w-full rounded-[4px] bg-slate-950 border border-white/[0.1] px-3 py-2 text-xs font-mono text-cyan-400 focus:outline-none focus:border-cyan-400"
						/>
					</div>
					<div>
						<label class="block text-[11px] font-mono text-slate-400 mb-1">Description (Optional)</label>
						<input
							type="text"
							bind:value={newServiceDesc}
							placeholder="Local web dashboard"
							class="w-full rounded-[4px] bg-slate-950 border border-white/[0.1] px-3 py-2 text-xs font-mono text-slate-100 focus:outline-none focus:border-cyan-400"
						/>
					</div>
				</div>

				<div class="flex items-center justify-end gap-2 pt-2 border-t border-white/[0.06]">
					<button
						onclick={() => { showExposeModal = false; }}
						class="rounded-[4px] px-3 py-1.5 text-xs font-mono font-semibold text-slate-400 hover:text-white cursor-pointer"
					>
						Cancel
					</button>
					<button
						onclick={handleExposeService}
						class="rounded-[4px] bg-cyan-400 px-3 py-1.5 text-xs font-mono font-bold text-slate-950 hover:bg-cyan-300 cursor-pointer"
					>
						Expose Service
					</button>
				</div>
			</div>
		</div>
	</div>
{/if}

<!-- Modal: Forward Service -->
{#if showForwardModal}
	<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm p-4">
		<div class="w-full max-w-md double-bezel">
			<div class="double-bezel-inner flex flex-col gap-4">
				<h3 class="font-bold text-xs text-slate-200 font-mono uppercase tracking-wider flex items-center gap-2 border-b border-white/[0.04] pb-3">
					<Icon icon="ph:arrows-left-right-bold" class="text-emerald-400" />
					Forward Remote Service
				</h3>

				<div class="space-y-3">
					<div>
						<label class="block text-[11px] font-mono text-slate-400 mb-1">Local Listen Address</label>
						<input
							type="text"
							bind:value={fwdLocalAddr}
							placeholder="127.0.0.1:8888"
							class="w-full rounded-[4px] bg-slate-950 border border-white/[0.1] px-3 py-2 text-xs font-mono text-cyan-400 focus:outline-none focus:border-cyan-400"
						/>
					</div>
					<div>
						<label class="block text-[11px] font-mono text-slate-400 mb-1">Remote Peer ID</label>
						<input
							type="text"
							bind:value={fwdRemotePeer}
							placeholder="12D3KooW..."
							class="w-full rounded-[4px] bg-slate-950 border border-white/[0.1] px-3 py-2 text-xs font-mono text-slate-100 focus:outline-none focus:border-cyan-400"
						/>
					</div>
					<div>
						<label class="block text-[11px] font-mono text-slate-400 mb-1">Remote Service Name</label>
						<input
							type="text"
							bind:value={fwdRemoteService}
							placeholder="webapp"
							class="w-full rounded-[4px] bg-slate-950 border border-white/[0.1] px-3 py-2 text-xs font-mono text-slate-100 focus:outline-none focus:border-cyan-400"
						/>
					</div>
				</div>

				<div class="flex items-center justify-end gap-2 pt-2 border-t border-white/[0.06]">
					<button
						onclick={() => { showForwardModal = false; }}
						class="rounded-[4px] px-3 py-1.5 text-xs font-mono font-semibold text-slate-400 hover:text-white cursor-pointer"
					>
						Cancel
					</button>
					<button
						onclick={handleForwardService}
						class="rounded-[4px] bg-emerald-400 px-3 py-1.5 text-xs font-mono font-bold text-slate-950 hover:bg-emerald-300 cursor-pointer"
					>
						Start Forwarder
					</button>
				</div>
			</div>
		</div>
	</div>
{/if}
