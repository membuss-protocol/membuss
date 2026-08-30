<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { apiFetch, formatBytes } from '$lib/api';
	import { toast } from '$lib/toast';
	import { base } from '$app/paths';
	import { goto } from '$app/navigation';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import ActionMenu from '$lib/components/ActionMenu.svelte';
	import Icon from '@iconify/svelte';
	import { uploader, type UploadTask } from '$lib/uploader';

	interface StoredMID {
		MID: string;
		Name: string;
		Sealed: boolean;
		Size: number;
		MimeType: string;
	}

	interface IndexData {
		AllFiles: StoredMID[];
	}

	// Local file cache derived from the index response
	interface LocalFile {
		mid: string;
		name: string;
		size: number;
		sealed: boolean;
		mime: string;
		type: 'file' | 'dir';
	}

	let fileList = $state<LocalFile[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);

	// Search, Filters & Sorting
	type SortKey = 'name' | 'size' | 'type' | 'status' | 'mid';
	type SortOrder = 'asc' | 'desc';
	type FilterType = 'all' | 'folders' | 'files';

	let filterStatus = $state<'all' | 'sealed' | 'unsealed'>('all');
	let filterType = $state<FilterType>('all');
	let searchQuery = $state('');
	let sortBy = $state<SortKey>('name');
	let sortOrder = $state<SortOrder>('asc');
	let foldersFirst = $state(true);

	function toggleSort(key: SortKey) {
		if (sortBy === key) {
			sortOrder = sortOrder === 'asc' ? 'desc' : 'asc';
		} else {
			sortBy = key;
			sortOrder = 'asc';
		}
	}

	let filteredFiles = $derived.by(() => {
		const filtered = fileList.filter(f => {
			const matchesStatus = 
				filterStatus === 'all' || 
				(filterStatus === 'sealed' && f.sealed) || 
				(filterStatus === 'unsealed' && !f.sealed);
			const matchesType =
				filterType === 'all' ||
				(filterType === 'folders' && f.type === 'dir') ||
				(filterType === 'files' && f.type === 'file');
			const matchesSearch = 
				!searchQuery || 
				f.name.toLowerCase().includes(searchQuery.toLowerCase()) || 
				f.mid.toLowerCase().includes(searchQuery.toLowerCase());
			return matchesStatus && matchesType && matchesSearch;
		});

		return filtered.sort((a, b) => {
			if (foldersFirst && a.type !== b.type) {
				return a.type === 'dir' ? -1 : 1;
			}

			let cmp = 0;
			if (sortBy === 'name') {
				cmp = a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });
			} else if (sortBy === 'size') {
				cmp = a.size - b.size;
			} else if (sortBy === 'type') {
				cmp = a.type.localeCompare(b.type);
			} else if (sortBy === 'status') {
				cmp = (a.sealed === b.sealed) ? 0 : a.sealed ? -1 : 1;
			} else if (sortBy === 'mid') {
				cmp = a.mid.localeCompare(b.mid);
			}

			return sortOrder === 'asc' ? cmp : -cmp;
		});
	});

	// Upload States
	let activeUploadTab = $state<'file' | 'folder' | 'descriptor'>('file');
	let folderName = $state('');
	let selectedFile = $state<File | null>(null);
	let selectedFiles = $state<FileList | null>(null);
	let descriptorFile = $state<File | null>(null);
	let descriptorStatus = $state<'idle' | 'importing' | 'fetching' | 'done' | 'error'>('idle');
	let descriptorError = $state('');
	let descriptorProgress = $state({ blocks: 0, total: 0, missing: 0 });
	
	// Upload Progress
	let uploadPercent = $state(0);
	let uploadActive = $state(false);
	let uploadStatusText = $state('');
	let loadedBytes = $state(0);
	let totalBytes = $state(0);
	let uploadFileList = $state<{ name: string; size: number }[]>([]);
	let uploadPhase = $state<'uploading' | 'sealing' | 'done'>('uploading');
	let activeXhr = $state<XMLHttpRequest | null>(null);

	// Network Fetch (Resolve MID) State
	let fetchMIDInput = $state('');
	let resolvingMIDs = $state<{ 
		mid: string; 
		statusText: string; 
		percent: number; 
		blocksResolved: number; 
		blocksTotal: number;
		eventSource: EventSource | null;
	}[]>([]);

	// Load file list from the index endpoint (all metadata included)
	async function loadFiles() {
		try {
			const indexRes: IndexData = await apiFetch('/');
			const allFiles = indexRes.AllFiles || [];
			
			const mapped: LocalFile[] = allFiles.map((item) => ({
				mid: item.MID,
				name: item.Name || 'Unnamed Record',
				sealed: item.Sealed,
				size: item.Size || 0,
				mime: item.MimeType || 'application/octet-stream',
				type: item.MimeType === 'inode/directory' ? 'dir' : 'file'
			}));

			fileList = mapped;
			loading = false;
		} catch (err) {
			error = err instanceof Error ? err.message : 'Failed to query file store';
			loading = false;
		}
	}

	// Trigger Seal / Unseal operations directly from the list
	async function toggleSeal(file: LocalFile) {
		const action = file.sealed ? 'unseal' : 'seal';
		try {
			const res = await fetch(`${base}/mid/${file.mid}/${action}`, {
				method: 'POST'
			});
			if (!res.ok) throw new Error(await res.text() || `HTTP ${res.status}`);
			
			file.sealed = !file.sealed;
			loadFiles();
		} catch (err) {
			toast.error(`Action failed: ${err instanceof Error ? err.message : err}`);
		}
	}

	let showDeleteModal = $state(false);
	let fileToDelete = $state<LocalFile | null>(null);

	function triggerDeleteFile(file: LocalFile) {
		fileToDelete = file;
		showDeleteModal = true;
	}

	async function proceedDeleteFile() {
		if (!fileToDelete) return;
		const file = fileToDelete;
		showDeleteModal = false;
		fileToDelete = null;

		try {
			const res = await fetch(`${base}/mid/${file.mid}/delete`, {
				method: 'POST'
			});
			if (!res.ok) throw new Error(await res.text() || `HTTP ${res.status}`);
			
			// Remove from local list immediately
			fileList = fileList.filter(f => f.mid !== file.mid);
			toast.success(`"${file.name}" deleted successfully.`);
		} catch (err) {
			toast.error(`Delete failed: ${err instanceof Error ? err.message : err}`);
		}
	}

	// Trigger network fetch of a remote MID
	async function fetchMID(e: Event) {
		e.preventDefault();
		const midVal = fetchMIDInput.trim().replace('/mem/', '');
		if (!midVal) return;

		// Check if already in fileList
		if (fileList.some(f => f.mid === midVal)) {
			toast.info('This Content ID is already present in your local store.');
			fetchMIDInput = '';
			return;
		}

		// Check if already resolving
		if (resolvingMIDs.some(r => r.mid === midVal)) {
			toast.info('This Content ID is already actively resolving from the DHT.');
			fetchMIDInput = '';
			return;
		}

		// Trigger background resolving by registering an EventSource stream
		const url = `${base}/mid/${midVal}/resolve-stream`;
		const es = new EventSource(url);
		
		const session = {
			mid: midVal,
			statusText: 'Connecting to DHT...',
			percent: 0,
			blocksResolved: 0,
			blocksTotal: 0,
			eventSource: es
		};

		resolvingMIDs = [...resolvingMIDs, session];
		fetchMIDInput = '';

		es.onmessage = (ev) => {
			const d = JSON.parse(ev.data);
			const idx = resolvingMIDs.findIndex(r => r.mid === midVal);
			if (idx === -1) return;

			if (d.error) {
				resolvingMIDs[idx].statusText = 'Error: ' + d.error;
				es.close();
				setTimeout(() => removeResolving(midVal), 5000);
				return;
			}

			if (d.done) {
				es.close();
				resolvingMIDs[idx].statusText = 'Pinning to local store...';
				resolvingMIDs[idx].percent = 100;
				// Seal (pin) the fetched content so it appears in the file list
				fetch(`${base}/mid/${midVal}/seal`, { method: 'POST' })
					.finally(() => {
						loadFiles();
						resolvingMIDs[idx].statusText = 'Resolved!';
						setTimeout(() => removeResolving(midVal), 2000);
					});
				return;
			}

			if (d.state === 'connecting') {
				resolvingMIDs[idx].statusText = 'Locating providers...';
			}

			const resolved = d.blocks_resolved ?? d.blocks ?? 0;
			const total = d.blocks_total ?? d.total ?? 0;
			const bytesDelivered = d.bytes_delivered ?? 0;
			const bytesTotal = d.bytes_total ?? 0;

			if (total > 0 || bytesTotal > 0 || bytesDelivered > 0 || resolved > 0) {
				resolvingMIDs[idx].blocksTotal = Math.max(resolvingMIDs[idx].blocksTotal || 0, total);
				resolvingMIDs[idx].blocksResolved = Math.max(resolvingMIDs[idx].blocksResolved || 0, resolved);

				if (bytesTotal > 0) {
					resolvingMIDs[idx].statusText = `Fetching (${formatBytes(bytesDelivered)} / ${formatBytes(bytesTotal)})...`;
					resolvingMIDs[idx].percent = Math.min(99, Math.round((bytesDelivered / bytesTotal) * 100));
				} else if (resolvingMIDs[idx].blocksTotal > 0) {
					resolvingMIDs[idx].statusText = `Downloading (${resolvingMIDs[idx].blocksResolved}/${resolvingMIDs[idx].blocksTotal} blocks)...`;
					resolvingMIDs[idx].percent = Math.min(99, Math.round((resolvingMIDs[idx].blocksResolved / resolvingMIDs[idx].blocksTotal) * 100));
				}
			}
		};

		es.onerror = () => {
			const idx = resolvingMIDs.findIndex(r => r.mid === midVal);
			if (idx !== -1) resolvingMIDs[idx].statusText = 'Lost connection, retrying...';
		};
	}

	function removeResolving(mid: string) {
		const s = resolvingMIDs.find(r => r.mid === mid);
		if (s && s.eventSource) s.eventSource.close();
		resolvingMIDs = resolvingMIDs.filter(r => r.mid !== mid);
	}

	// File Ingestion Upload Handlers
	function handleUpload(files: File[], customFolderName?: string) {
		uploader.startUpload(files, customFolderName);
		selectedFile = null;
		selectedFiles = null;
		folderName = '';
		// Auto refresh file table while uploads complete in background
		setTimeout(() => loadFiles(), 2000);
	}

	function cancelUpload() {
		if (activeXhr) {
			activeXhr.abort();
			activeXhr = null;
		}
		uploadActive = false;
	}

	function triggerUploadForm(e: Event) {
		e.preventDefault();
		if (activeUploadTab === 'file' && selectedFile) {
			handleUpload([selectedFile]);
		} else if (activeUploadTab === 'folder' && selectedFiles && selectedFiles.length > 0) {
			const filesArr: File[] = [];
			for (let i = 0; i < selectedFiles.length; i++) {
				filesArr.push(selectedFiles[i]);
			}
			handleUpload(filesArr, folderName);
		}
	}

	function handleFileChange(e: Event) {
		const target = e.target as HTMLInputElement;
		if (target.files && target.files.length > 0) selectedFile = target.files[0];
	}

	function handleFolderChange(e: Event) {
		const target = e.target as HTMLInputElement;
		if (target.files && target.files.length > 0) {
			selectedFiles = target.files;
			if (!folderName) {
				const firstPath = target.files[0].webkitRelativePath || '';
				folderName = firstPath.split('/')[0] || 'Imported Folder';
			}
		}
	}

	function handleDescriptorChange(e: Event) {
		const target = e.target as HTMLInputElement;
		if (target.files && target.files.length > 0) {
			descriptorFile = target.files[0];
			descriptorStatus = 'idle';
			descriptorError = '';
		} else {
			descriptorFile = null;
		}
	}

	async function handleDescriptorSubmit(e: Event) {
		e.preventDefault();
		if (!descriptorFile) return;
		descriptorStatus = 'importing';
		descriptorError = '';
		descriptorProgress = { blocks: 0, total: 0, missing: 0 };

		try {
			// First, upload the .mbuss file to the streaming endpoint
			const formData = new FormData();
			formData.append('file', descriptorFile);

			// Use fetch to POST, then read the SSE stream from the response
			const res = await fetch(`${base}/descriptor/import-stream`, { method: 'POST', body: formData });
			if (!res.ok) {
				const txt = await res.text();
				throw new Error(txt || `HTTP ${res.status}`);
			}

			const reader = res.body?.getReader();
			if (!reader) throw new Error('No response stream');

			const decoder = new TextDecoder();
			let buffer = '';

			while (true) {
				const { done, value } = await reader.read();
				if (done) break;

				buffer += decoder.decode(value, { stream: true });
				const lines = buffer.split('\n');
				buffer = lines.pop() || '';

				for (const line of lines) {
					if (!line.startsWith('data: ')) continue;
					const jsonStr = line.slice(6).trim();
					if (!jsonStr) continue;

					try {
						const ev = JSON.parse(jsonStr);
						if (ev.error) {
							descriptorStatus = 'error';
							descriptorError = ev.error;
							return;
						}
						if (ev.state === 'fetching') {
							descriptorStatus = 'fetching';
							descriptorProgress = { blocks: 0, total: ev.total || 0, missing: ev.missing || 0 };
						}
						if (ev.state === 'downloading') {
							descriptorStatus = 'fetching';
							descriptorProgress = { blocks: ev.blocks || 0, total: ev.total || 0, missing: descriptorProgress.missing };
						}
						if (ev.done && ev.mid) {
							descriptorStatus = 'done';
							setTimeout(() => {
								goto(`${base}/mid/${ev.mid}`);
							}, 500);
							return;
						}
					} catch {
						// skip malformed lines
					}
				}
			}

			// If we get here without a done event, something went wrong
			if (descriptorStatus !== 'done') {
				descriptorStatus = 'error';
				descriptorError = 'Stream ended without completion';
			}
		} catch (err) {
			descriptorStatus = 'error';
			descriptorError = err instanceof Error ? err.message : 'Import failed';
		}
	}

	let activeUploadTasks = $state<UploadTask[]>([]);

	onMount(() => {
		loadFiles();
		const unsub = uploader.subscribe(() => {
			activeUploadTasks = uploader.allTasks;
			if (uploader.allTasks.some((t) => t.phase === 'done')) {
				loadFiles();
			}
		});

		const pollTimer = setInterval(() => {
			if (activeUploadTasks.some((t) => t.phase === 'uploading' || t.phase === 'indexing')) {
				loadFiles();
			}
		}, 2000);

		return () => {
			unsub();
			clearInterval(pollTimer);
		};
	});

	onDestroy(() => {
		if (activeXhr) activeXhr.abort();
		resolvingMIDs.forEach(r => r.eventSource && r.eventSource.close());
	});
</script>

<div class="flex flex-col gap-6">
	<!-- Page Header -->
	<div class="border-b border-slate-800/80 pb-4">
		<h1 class="font-display text-2xl text-slate-50">Local File System</h1>
		<p class="text-xs text-slate-500 mt-1">Manage files, seal/pin redundancy parameters, and fetch Merkle DAGs from the network</p>
	</div>

	<!-- Top split actions layout -->
	<div class="grid grid-cols-1 lg:grid-cols-12 gap-6 items-stretch">
		
		<!-- Action Panel 1: Upload (merged uploader) -->
		<div class="bg-slate-900 border border-slate-800/80 rounded-xl p-5 lg:col-span-7 flex flex-col gap-4 relative overflow-hidden">
			<div class="flex border-b border-slate-700/50">
				<button 
					onclick={() => activeUploadTab = 'file'}
					class={`pb-2 px-3 text-xs font-mono font-bold tracking-wider uppercase border-b-2 -mb-[2px] transition-all ${
						activeUploadTab === 'file' ? 'border-cyan-500 text-cyan-400' : 'border-transparent text-slate-500'
					}`}
				>
					File Upload
				</button>
			<button 
				onclick={() => activeUploadTab = 'folder'}
				class={`pb-2 px-3 text-xs font-mono font-bold tracking-wider uppercase border-b-2 -mb-[2px] transition-all ${
					activeUploadTab === 'folder' ? 'border-cyan-500 text-cyan-400' : 'border-transparent text-slate-500'
				}`}
			>
				Directory Upload
			</button>
			<button 
				onclick={() => activeUploadTab = 'descriptor'}
				class={`pb-2 px-3 text-xs font-mono font-bold tracking-wider uppercase border-b-2 -mb-[2px] transition-all ${
					activeUploadTab === 'descriptor' ? 'border-cyan-500 text-cyan-400' : 'border-transparent text-slate-500'
				}`}
			>
				Import .mbuss
			</button>
			</div>

			{#if activeUploadTab === 'descriptor'}
				<form onsubmit={handleDescriptorSubmit} class="flex flex-col gap-4 flex-grow justify-between">
					<div class="group relative border border-slate-700/50 hover:border-slate-700/50 rounded-lg p-5 flex flex-col items-center text-center gap-2 select-none cursor-pointer bg-slate-950/30 py-7">
						<Icon icon="ph:file-arrow-down" class="text-4xl text-slate-500 group-hover:scale-110 transition-transform" />
						<span class="text-xs font-bold text-slate-300">
							{descriptorFile ? descriptorFile.name : 'Select a .mbuss descriptor'}
						</span>
						{#if descriptorFile}
							<span class="text-[10px] text-slate-500 font-mono">({formatBytes(descriptorFile.size)})</span>
						{/if}
						<input type="file" accept=".mbuss" required disabled={descriptorStatus === 'importing'} onchange={handleDescriptorChange} class="absolute inset-0 opacity-0 cursor-pointer w-full h-full" />
					</div>
					{#if descriptorStatus === 'error'}
						<div class="bg-red-950/20 border border-red-800/40 text-red-400 px-4 py-3 rounded-lg text-xs font-mono">
							{descriptorError}
						</div>
					{/if}
					{#if descriptorStatus === 'done'}
						<div class="bg-emerald-950/20 border border-emerald-800/40 text-emerald-400 px-4 py-3 rounded-lg text-xs font-mono">
							Imported! Redirecting...
						</div>
					{/if}
					{#if descriptorStatus === 'fetching'}
						<div class="flex flex-col gap-2">
							<div class="flex items-center justify-between text-[10px] font-mono text-slate-400">
								<span>Fetching from network...</span>
								<span>{descriptorProgress.blocks} / {descriptorProgress.total} blocks</span>
							</div>
							<div class="w-full h-1.5 rounded-full bg-slate-800 overflow-hidden">
								<div 
									class="h-full bg-cyan-500 transition-all duration-300"
									style={`width: ${descriptorProgress.total > 0 ? (descriptorProgress.blocks / descriptorProgress.total * 100) : 0}%`}
								></div>
							</div>
							<p class="text-[10px] text-slate-500">Downloading missing blocks from peers...</p>
						</div>
					{/if}
					<button 
						type="submit" 
						disabled={!descriptorFile || descriptorStatus === 'importing' || descriptorStatus === 'fetching'}
						class="w-full py-2.5 bg-cyan-500 hover:bg-cyan-400 disabled:bg-slate-800 text-slate-950 disabled:text-slate-500 text-xs font-bold rounded-lg transition-all duration-200 active:scale-[0.98] flex items-center justify-center gap-2"
					>
						{#if descriptorStatus === 'importing'}
							<div class="w-3.5 h-3.5 border-2 border-slate-950/30 border-t-slate-950 rounded-full animate-spin"></div>
							Verifying...
						{:else if descriptorStatus === 'fetching'}
							<div class="w-3.5 h-3.5 border-2 border-slate-950/30 border-t-slate-950 rounded-full animate-spin"></div>
							Fetching...
						{:else}
							<Icon icon="ph:download-simple" class="text-sm" />
							Import Descriptor
						{/if}
					</button>
				</form>
			{:else}
				<form onsubmit={triggerUploadForm} class="flex flex-col gap-4 flex-grow justify-between">
					{#if activeUploadTab === 'file'}
						<div class="group relative border border-slate-700/50 hover:border-slate-700/50 rounded-lg p-5 flex flex-col items-center text-center gap-2 select-none cursor-pointer bg-slate-950/30 py-7">
							<Icon icon="ph:upload-simple" class="text-4xl text-slate-500 group-hover:scale-110 transition-transform" />
							<span class="text-xs font-bold text-slate-300">
								{selectedFile ? selectedFile.name : 'Select or drop a file'}
							</span>
							{#if selectedFile}
								<span class="text-[10px] text-slate-500 font-mono">({formatBytes(selectedFile.size)})</span>
							{/if}
							<input type="file" required onchange={handleFileChange} class="absolute inset-0 opacity-0 cursor-pointer w-full h-full" />
						</div>
					{:else}
						<div class="group relative border border-slate-700/50 hover:border-slate-700/50 rounded-lg p-5 flex flex-col items-center text-center gap-2 select-none cursor-pointer bg-slate-950/30 py-4">
							<Icon icon="ph:folder-open" class="text-4xl text-slate-500 group-hover:scale-110 transition-transform" />
							<span class="text-xs font-bold text-slate-300">
								{selectedFiles && selectedFiles.length > 0 ? `${selectedFiles.length} files selected` : 'Select a directory to import'}
							</span>
							<input type="file" required webkitdirectory directory multiple onchange={handleFolderChange} class="absolute inset-0 opacity-0 cursor-pointer w-full h-full" />
						</div>
						<input 
							type="text" 
							bind:value={folderName} 
							placeholder="Custom root directory name (optional)" 
							class="w-full bg-slate-950/60 border border-slate-700/50 text-xs px-3.5 py-2.5 rounded-lg focus:outline-none focus:border-cyan-500" 
						/>
					{/if}

					<button 
						type="submit" 
						disabled={(activeUploadTab === 'file' ? !selectedFile : !selectedFiles) || uploadActive}
						class="w-full py-2.5 bg-cyan-500 hover:bg-cyan-400 disabled:bg-slate-800 text-slate-950 disabled:text-slate-500 text-xs font-bold rounded-lg transition-all duration-200 active:scale-[0.98]"
					>
						{uploadActive ? 'Processing Ingest...' : 'Ingest to Network'}
					</button>
				</form>
			{/if}
		</div>

		<!-- Action Panel 2: Fetch CID/MID from Swarm DHT -->
		<div class="bg-slate-900 border border-slate-800/80 rounded-xl p-5 lg:col-span-5 flex flex-col gap-4">
			<h3 class="font-bold text-xs text-slate-400 font-mono uppercase tracking-wider border-b border-slate-700/50 pb-2">
				Swarm Ingest (Fetch CID)
			</h3>
			<p class="text-[11px] text-slate-500 leading-relaxed font-sans">
				Import content by entering its Content Identifier (MID). Membuss will query Kademlia routing tables and resolve blocks via P2P Memex stream sessions.
			</p>
			
			<form onsubmit={fetchMID} class="flex flex-col gap-4 mt-auto">
				<input
					type="text"
					bind:value={fetchMIDInput}
					required
					placeholder="Enter mem1z... multihash address"
					class="w-full bg-slate-950/60 border border-slate-700/50 text-xs px-3.5 py-2.5 rounded-lg focus:outline-none focus:border-cyan-500/80 focus:ring-1 focus:ring-cyan-500/20 font-mono"
				/>
				<button 
					type="submit"
					class="w-full py-2.5 bg-slate-800 hover:bg-slate-600 border border-slate-700/50 text-slate-200 text-xs font-bold rounded-lg transition-all duration-200 active:scale-[0.98]"
				>
					Resolve & Fetch Content
				</button>
			</form>
		</div>
	</div>

	<!-- Active resolving background tasks list -->
	{#if resolvingMIDs.length > 0}
		<section class="bg-slate-900 border border-slate-800/80 rounded-xl p-5 flex flex-col gap-4">
			<h3 class="font-bold text-xs text-slate-400 font-mono uppercase tracking-wider border-b border-slate-700/50 pb-1">
				Active DHT Resolving Queue
			</h3>
			<div class="grid grid-cols-1 md:grid-cols-2 gap-4">
				{#each resolvingMIDs as res}
					<div class="bg-slate-950/60 border border-slate-700/50 rounded-lg p-3 flex flex-col gap-2 font-mono text-[10px] relative">
						<button 
							onclick={() => removeResolving(res.mid)}
							class="absolute top-2 right-3 text-slate-600 hover:text-slate-300 text-xs"
						>
							✕
						</button>
						<div class="flex flex-col">
							<span class="text-slate-500 uppercase text-[8px]">Fetching Target</span>
							<span class="text-slate-200 font-bold break-all select-all">{res.mid}</span>
						</div>
						<div class="flex items-center justify-between border-t border-slate-800/40 pt-2 text-[9px] text-slate-500">
							<span>{res.statusText}</span>
							<span class="font-bold text-cyan-400">{res.percent}%</span>
						</div>
						<div class="w-full h-1 bg-slate-900 rounded-full overflow-hidden">
							<div class="h-full bg-cyan-400 transition-all duration-300" style={`width: ${res.percent}%`}></div>
						</div>
					</div>
				{/each}
			</div>
		</section>
	{/if}

	<!-- Active backend ingest & processing queue -->
	{#if activeUploadTasks.length > 0}
		<section class="bg-slate-900 border border-cyan-900/50 rounded-xl p-5 flex flex-col gap-4 shadow-lg ring-1 ring-cyan-500/10">
			<div class="flex items-center justify-between border-b border-slate-700/50 pb-2">
				<h3 class="font-bold text-xs text-cyan-400 font-mono uppercase tracking-wider flex items-center gap-2">
					<Icon icon="ph:lightning-bold" class="w-4 h-4 text-cyan-400" />
					Active Backend Ingest & Processing Pipeline
				</h3>
				<button 
					onclick={() => uploader.clearCompleted()}
					class="text-[10px] font-mono text-slate-400 hover:text-slate-200 transition-colors"
				>
					Clear Finished Tasks
				</button>
			</div>
			<div class="grid grid-cols-1 md:grid-cols-2 gap-4">
				{#each activeUploadTasks as task}
					<div class="bg-slate-950/70 border border-slate-800 rounded-lg p-3.5 flex flex-col gap-2.5 font-mono text-[11px] relative">
						<div class="flex items-center justify-between">
							<span class="font-bold text-slate-200 truncate pr-4" title={task.title}>{task.title}</span>
							<span class={`text-[9px] px-2 py-0.5 rounded font-bold uppercase ${
								task.phase === 'done'
									? 'bg-emerald-500/20 text-emerald-400 border border-emerald-500/30'
									: task.phase === 'indexing'
										? 'bg-amber-500/20 text-amber-400 border border-amber-500/30 animate-pulse'
										: 'bg-cyan-500/20 text-cyan-400 border border-cyan-500/30'
							}`}>
								{task.phase}
							</span>
						</div>
						<div class="text-[10px] text-slate-400 truncate">{task.statusText}</div>
						<div class="w-full h-1.5 bg-slate-900 rounded-full overflow-hidden border border-slate-800">
							<div
								class={`h-full transition-all duration-300 ${
									task.phase === 'done' ? 'bg-emerald-400' : 'bg-cyan-400'
								}`}
								style={`width: ${task.percent}%`}
							></div>
						</div>
						<div class="text-[9px] text-slate-500 flex items-center justify-between">
							<span>{task.items.length} items ({formatBytes(task.totalBytes)})</span>
							{#if task.phase === 'done'}
								<button onclick={() => loadFiles()} class="text-cyan-400 hover:underline">
									Refresh Files List
								</button>
							{/if}
						</div>
					</div>
				{/each}
			</div>
		</section>
	{/if}

	<!-- File List Toolbar (Search + Filters + Sorting) -->
	<section class="bg-slate-900 border border-slate-800/80 rounded-xl p-5 flex flex-col gap-5">
		<div class="flex flex-col lg:flex-row items-stretch lg:items-center justify-between gap-4 border-b border-slate-700/50 pb-4">
			<!-- View Type Filters (All / Folders Only / Files Only) -->
			<div class="flex flex-wrap items-center gap-1.5 p-1 bg-slate-950/80 border border-slate-700/50 rounded-lg">
				<button 
					onclick={() => filterType = 'all'} 
					class={`px-3 py-1.5 rounded-md text-[10px] font-bold font-mono tracking-wider uppercase transition-colors flex items-center gap-1 ${
						filterType === 'all' ? 'bg-slate-800/60 text-cyan-400 border border-slate-800/80' : 'text-slate-500 hover:text-slate-300'
					}`}
				>
					<Icon icon="ph:squares-four-bold" class="text-xs" />
					All Items
				</button>
				<button 
					onclick={() => filterType = 'folders'} 
					class={`px-3 py-1.5 rounded-md text-[10px] font-bold font-mono tracking-wider uppercase transition-colors flex items-center gap-1 ${
						filterType === 'folders' ? 'bg-slate-800/60 text-cyan-400 border border-slate-800/80' : 'text-slate-500 hover:text-slate-300'
					}`}
				>
					<Icon icon="ph:folder-bold" class="text-xs" />
					Folders Only ({fileList.filter(f => f.type === 'dir').length})
				</button>
				<button 
					onclick={() => filterType = 'files'} 
					class={`px-3 py-1.5 rounded-md text-[10px] font-bold font-mono tracking-wider uppercase transition-colors flex items-center gap-1 ${
						filterType === 'files' ? 'bg-slate-800/60 text-cyan-400 border border-slate-800/80' : 'text-slate-500 hover:text-slate-300'
					}`}
				>
					<Icon icon="ph:file-text-bold" class="text-xs" />
					Files Only ({fileList.filter(f => f.type === 'file').length})
				</button>
			</div>

			<!-- Pin Status filters -->
			<div class="flex flex-wrap items-center gap-1.5 p-1 bg-slate-950/80 border border-slate-700/50 rounded-lg">
				<button 
					onclick={() => filterStatus = 'all'} 
					class={`px-2.5 py-1.5 rounded-md text-[10px] font-bold font-mono tracking-wider uppercase transition-colors ${
						filterStatus === 'all' ? 'bg-slate-800/60 text-cyan-400 border border-slate-800/80' : 'text-slate-500 hover:text-slate-300'
					}`}
				>
					All Status
				</button>
				<button 
					onclick={() => filterStatus = 'sealed'} 
					class={`px-2.5 py-1.5 rounded-md text-[10px] font-bold font-mono tracking-wider uppercase transition-colors ${
						filterStatus === 'sealed' ? 'bg-slate-800/60 text-cyan-400 border border-slate-800/80' : 'text-slate-500 hover:text-slate-300'
					}`}
				>
					Pinned
				</button>
				<button 
					onclick={() => filterStatus = 'unsealed'} 
					class={`px-2.5 py-1.5 rounded-md text-[10px] font-bold font-mono tracking-wider uppercase transition-colors ${
						filterStatus === 'unsealed' ? 'bg-slate-800/60 text-cyan-400 border border-slate-800/80' : 'text-slate-500 hover:text-slate-300'
					}`}
				>
					Unpinned
				</button>
			</div>

			<!-- Sort controls & Search bar -->
			<div class="flex flex-wrap items-center gap-3">
				<!-- Sort select dropdown -->
				<div class="flex items-center gap-1.5 bg-slate-950/80 border border-slate-700/50 rounded-lg px-2 py-1">
					<span class="text-[9px] font-mono text-slate-500 uppercase">Sort:</span>
					<select
						bind:value={sortBy}
						class="bg-transparent text-slate-200 text-xs font-mono focus:outline-none cursor-pointer"
					>
						<option value="name" class="bg-slate-900 text-slate-200">Name</option>
						<option value="size" class="bg-slate-900 text-slate-200">Size</option>
						<option value="type" class="bg-slate-900 text-slate-200">Type</option>
						<option value="status" class="bg-slate-900 text-slate-200">Status</option>
						<option value="mid" class="bg-slate-900 text-slate-200">MID</option>
					</select>
					<button
						onclick={() => sortOrder = sortOrder === 'asc' ? 'desc' : 'asc'}
						class="p-1 text-slate-400 hover:text-cyan-400 transition-colors"
						title={sortOrder === 'asc' ? 'Ascending' : 'Descending'}
					>
						<Icon icon={sortOrder === 'asc' ? 'ph:sort-ascending-bold' : 'ph:sort-descending-bold'} class="text-sm" />
					</button>
				</div>

				<!-- Folders First Toggle -->
				<button
					onclick={() => foldersFirst = !foldersFirst}
					class={`px-2.5 py-1.5 rounded-lg border text-[10px] font-bold font-mono transition-colors flex items-center gap-1 ${foldersFirst ? 'bg-cyan-950/40 border-cyan-800/40 text-cyan-400' : 'bg-slate-950/80 border-slate-700/50 text-slate-500'}`}
					title="Keep folders at the top of the file list"
				>
					<Icon icon="ph:folder-user-bold" class="text-xs" />
					Folders First
				</button>

				<!-- Search filter input -->
				<div class="relative w-full sm:w-60">
					<input
						type="text"
						bind:value={searchQuery}
						placeholder="Filter by name or MID..."
						class="w-full bg-slate-950/60 border border-slate-700/50 text-xs px-3.5 py-1.5 rounded-lg focus:outline-none focus:border-cyan-500"
					/>
					{#if searchQuery}
						<button onclick={() => searchQuery = ''} class="absolute right-3 top-1.5 text-slate-500 hover:text-slate-300 text-xs font-bold">✕</button>
					{/if}
				</div>
			</div>
		</div>

		<!-- File List Table -->
		{#if loading}
			<div class="flex flex-col">
				<div class="grid grid-cols-12 gap-4 border-b border-slate-800/80 py-2.5 px-4">
					<div class="col-span-3"><Skeleton width="3rem" height="0.6rem" /></div>
					<div class="col-span-4"><Skeleton width="8rem" height="0.6rem" /></div>
					<div class="col-span-2"><Skeleton width="3rem" height="0.6rem" /></div>
					<div class="col-span-1"><Skeleton width="3rem" height="0.6rem" /></div>
					<div class="col-span-2 flex justify-end"><Skeleton width="4rem" height="0.6rem" /></div>
				</div>
				{#each Array(5) as _}
					<div class="grid grid-cols-12 gap-4 items-center border-b border-slate-850/40 py-3.5 px-4">
						<div class="col-span-3 flex items-center gap-2">
							<Skeleton width="1rem" height="1rem" rounded="rounded" />
							<Skeleton width="7rem" height="0.75rem" />
						</div>
						<div class="col-span-4"><Skeleton width="90%" height="0.75rem" /></div>
						<div class="col-span-2"><Skeleton width="3.5rem" height="0.75rem" /></div>
						<div class="col-span-1 flex justify-center"><Skeleton width="3.5rem" height="1rem" rounded="rounded-md" /></div>
						<div class="col-span-2 flex justify-end gap-1.5">
							<Skeleton width="1.75rem" height="1.75rem" rounded="rounded-lg" />
							<Skeleton width="1.75rem" height="1.75rem" rounded="rounded-lg" />
						</div>
					</div>
				{/each}
			</div>
		{:else if filteredFiles && filteredFiles.length > 0}
			<div class="overflow-x-auto">
				<table class="w-full text-left border-collapse text-xs">
					<thead>
						<tr class="border-b border-slate-800/80 text-slate-500 font-mono text-[10px] uppercase bg-slate-950/20">
							<th class="py-2.5 px-4 font-semibold">
								<button onclick={() => toggleSort('name')} class="flex items-center gap-1 hover:text-slate-200 transition-colors group">
									<span>Name</span>
									<Icon icon={sortBy === 'name' ? (sortOrder === 'asc' ? 'ph:caret-up-bold' : 'ph:caret-down-bold') : 'ph:arrows-down-up-bold'} class={`text-xs ${sortBy === 'name' ? 'text-cyan-400' : 'text-slate-600 opacity-0 group-hover:opacity-100'}`} />
								</button>
							</th>
							<th class="py-2.5 px-4 font-semibold w-1/3">
								<button onclick={() => toggleSort('mid')} class="flex items-center gap-1 hover:text-slate-200 transition-colors group">
									<span>Content Address (MID)</span>
									<Icon icon={sortBy === 'mid' ? (sortOrder === 'asc' ? 'ph:caret-up-bold' : 'ph:caret-down-bold') : 'ph:arrows-down-up-bold'} class={`text-xs ${sortBy === 'mid' ? 'text-cyan-400' : 'text-slate-600 opacity-0 group-hover:opacity-100'}`} />
								</button>
							</th>
							<th class="py-2.5 px-4 font-semibold w-24">
								<button onclick={() => toggleSort('size')} class="flex items-center gap-1 hover:text-slate-200 transition-colors group">
									<span>Size</span>
									<Icon icon={sortBy === 'size' ? (sortOrder === 'asc' ? 'ph:caret-up-bold' : 'ph:caret-down-bold') : 'ph:arrows-down-up-bold'} class={`text-xs ${sortBy === 'size' ? 'text-cyan-400' : 'text-slate-600 opacity-0 group-hover:opacity-100'}`} />
								</button>
							</th>
							<th class="py-2.5 px-4 font-semibold w-24 text-center">
								<button onclick={() => toggleSort('status')} class="flex items-center gap-1 mx-auto hover:text-slate-200 transition-colors group">
									<span>Status</span>
									<Icon icon={sortBy === 'status' ? (sortOrder === 'asc' ? 'ph:caret-up-bold' : 'ph:caret-down-bold') : 'ph:arrows-down-up-bold'} class={`text-xs ${sortBy === 'status' ? 'text-cyan-400' : 'text-slate-600 opacity-0 group-hover:opacity-100'}`} />
								</button>
							</th>
							<th class="py-2.5 px-4 font-semibold text-right">Actions</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-slate-800/60">
						{#each filteredFiles as file (file.mid)}
							<tr class="hover:bg-slate-700/30 transition-colors group">
								<!-- Icon + Name -->
								<td class="py-3 px-4">
									<div class="flex items-center gap-2">
										{#if file.type === 'dir'}
											<Icon icon="ph:folder" class="w-4 h-4 text-slate-400" />
										{:else}
											<Icon icon="ph:file-text" class="w-4 h-4 text-slate-400" />
										{/if}
										<a 
											href={`${base}/mid/${file.mid}`} 
											class="font-bold text-slate-200 hover:text-cyan-400 hover:underline break-all truncate max-w-[200px]"
											title={file.name}
										>
											{file.name}
										</a>
									</div>
								</td>

								<!-- MID -->
								<td class="py-3 px-4 font-mono text-slate-500 max-w-[250px] truncate">
									<a href={`${base}/mid/${file.mid}`} class="hover:text-cyan-400 hover:underline" title={file.mid}>
										{file.mid}
									</a>
								</td>

								<!-- Size -->
								<td class="py-3 px-4 font-mono text-slate-400">
									{file.type === 'dir' ? '—' : formatBytes(file.size)}
								</td>

								<!-- Status Badges -->
								<td class="py-3 px-4 text-center">
									{#if file.sealed}
										<span class="px-2 py-0.5 rounded text-[9px] font-bold font-mono bg-emerald-500/10 text-emerald-400 border border-emerald-500/30">
											PINNED
										</span>
									{:else}
										<span class="px-2 py-0.5 rounded text-[9px] font-bold font-mono bg-slate-800 text-slate-500 border border-slate-700/50">
											UNPINNED
										</span>
									{/if}
								</td>

								<!-- In-line actions -->
								<td class="py-3 px-4 text-right">
									<div class="flex items-center justify-end">
										<ActionMenu
											target={file.mid}
											isDir={file.type === 'dir'}
											sealed={file.sealed}
											inspectHref={`${base}/mid/${file.mid}`}
											onToggleSeal={() => toggleSeal(file)}
											onDelete={() => triggerDeleteFile(file)}
										/>
									</div>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{:else}
			<div class="py-16 text-center flex flex-col items-center justify-center gap-3">
				<Icon icon="ph:files" class="text-4xl text-slate-600" />
				<div class="text-sm font-semibold text-slate-400">No Files Match Current Filters</div>
				<p class="text-xs text-slate-500 max-w-xs leading-relaxed">
					Refine your search parameters or check other tabs to locate your Content IDs.
				</p>
			</div>
		{/if}
	</section>
</div>

<!-- Upload Progress Widget Overlay -->
{#if uploadActive}
	<div class="fixed inset-0 z-50 bg-black/60 backdrop-blur-sm flex items-center justify-center p-4">
		<div class="bg-slate-900 border border-slate-800/80 rounded-xl w-full max-w-md shadow-2xl overflow-hidden flex flex-col">
			<!-- Header -->
			<div class="px-5 py-4 bg-slate-950/40 border-b border-slate-800/80 flex items-center justify-between">
				<div class="flex items-center gap-2 text-xs font-bold font-mono text-slate-300">
					{#if uploadPhase === 'uploading'}
						<div class="w-3 h-3 border border-cyan-500/35 border-t-cyan-400 rounded-full animate-spin"></div>
					{:else if uploadPhase === 'sealing'}
						<div class="w-3 h-3 rounded-full bg-cyan-400 animate-ping"></div>
					{/if}
					<span>{uploadStatusText}</span>
				</div>
				{#if uploadPhase === 'uploading'}
					<button onclick={cancelUpload} class="text-[10px] text-slate-500 hover:text-red-400 border border-slate-800/80 px-2 py-0.5 rounded bg-slate-950/40 font-mono">
						Cancel
					</button>
				{/if}
			</div>

			<!-- Body -->
			<div class="p-5 flex flex-col gap-4 font-mono text-xs">
				<!-- Big percent indicator -->
				<div class="flex items-end justify-between">
					<span class="text-3xl font-black text-cyan-400 leading-none">
						{uploadPercent}%
					</span>
					<span class="text-[10px] text-slate-500">
						{formatBytes(loadedBytes)} / {formatBytes(totalBytes)}
					</span>
				</div>

				<!-- Bar -->
				<div class="w-full h-1.5 rounded-full bg-slate-950 border border-slate-700/50 overflow-hidden">
					<div 
						class="h-full bg-gradient-to-r from-cyan-500 to-blue-500 transition-all duration-300"
						style={`width: ${uploadPercent}%`}
					></div>
				</div>

				<!-- Files list section -->
				<div class="flex flex-col gap-1.5 mt-2">
					<span class="text-[9px] text-slate-500 uppercase tracking-wide">
						Uploading {uploadFileList.length} items
					</span>
					<div class="bg-slate-950/80 border border-slate-700/50 rounded-lg max-h-24 overflow-y-auto divide-y divide-slate-800/40 p-2 text-[9px] text-slate-500">
						{#each uploadFileList as file}
							<div class="py-1 px-1 flex justify-between gap-4">
								<span class="truncate text-slate-400 select-all">{file.name}</span>
								<span class="shrink-0 text-slate-600">{formatBytes(file.size)}</span>
							</div>
						{/each}
					</div>
				</div>
			</div>
		</div>
	</div>
{/if}

{#if showDeleteModal && fileToDelete}
	<div class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm">
		<div class="bg-slate-900 border border-slate-800 rounded-2xl p-6 max-w-md w-full flex flex-col gap-4 shadow-2xl animate-in fade-in zoom-in-95 duration-200">
			<h3 class="font-bold text-base text-slate-100 flex items-center gap-2">
				<Icon icon="ph:warning-bold" class="text-red-500 text-lg" />
				Delete Content ID
			</h3>
			<p class="text-xs text-slate-400 leading-relaxed">
				Are you sure you want to delete <span class="font-bold text-slate-300">"{fileToDelete.name}"</span> and all its blocks recursively from this node? This action is permanent and cannot be undone.
			</p>
			<div class="flex items-center justify-end gap-3 mt-2">
				<button 
					onclick={() => showDeleteModal = false} 
					class="px-4 py-2 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 text-xs font-semibold transition-colors"
				>
					Cancel
				</button>
				<button 
					onclick={proceedDeleteFile} 
					class="px-4 py-2 rounded-xl bg-red-650 hover:bg-red-700 text-slate-50 text-xs font-semibold transition-colors"
				>
					Delete
				</button>
			</div>
		</div>
	</div>
{/if}
