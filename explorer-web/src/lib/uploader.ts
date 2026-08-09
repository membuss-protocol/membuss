import { base } from '$app/paths';
import { toast } from '$lib/toast';

export interface UploadItem {
	name: string;
	size: number;
	status: 'queued' | 'uploading' | 'indexing' | 'done' | 'error';
	mid?: string;
	error?: string;
}

export interface UploadTask {
	id: string;
	title: string;
	items: UploadItem[];
	totalBytes: number;
	loadedBytes: number;
	percent: number;
	phase: 'uploading' | 'indexing' | 'done' | 'error';
	statusText: string;
	minimized: boolean;
	createdAt: number;
}

class UploaderStore {
	tasks: UploadTask[] = [];
	subscribers = new Set<() => void>();

	constructor() {
		this.loadFromStorage();
	}

	private loadFromStorage() {
		if (typeof window === 'undefined') return;
		try {
			const saved = localStorage.getItem('membuss_upload_tasks');
			if (saved) {
				const parsed = JSON.parse(saved);
				const now = Date.now();
				// Keep only tasks from last 2 hours
				this.tasks = parsed.filter((t: UploadTask) => now - t.createdAt < 2 * 60 * 60 * 1000);
			}
		} catch (e) {
			console.error('Failed to load upload tasks:', e);
		}
	}

	private saveToStorage() {
		if (typeof window === 'undefined') return;
		try {
			const serializable = this.tasks.slice(-10).map((t) => ({
				id: t.id,
				title: t.title,
				items: t.items,
				totalBytes: t.totalBytes,
				loadedBytes: t.loadedBytes,
				percent: t.percent,
				phase: t.phase,
				statusText: t.statusText,
				minimized: t.minimized,
				createdAt: t.createdAt
			}));
			localStorage.setItem('membuss_upload_tasks', JSON.stringify(serializable));
		} catch (e) {
			console.error('Failed to save upload tasks:', e);
		}
	}

	subscribe(fn: () => void) {
		this.subscribers.add(fn);
		fn();
		return () => this.subscribers.delete(fn);
	}

	private notify() {
		this.saveToStorage();
		for (const fn of this.subscribers) {
			fn();
		}
	}

	get activeTask(): UploadTask | undefined {
		return this.tasks.find((t) => t.phase === 'uploading' || t.phase === 'indexing');
	}

	get allTasks(): UploadTask[] {
		return [...this.tasks].reverse();
	}

	toggleMinimize(id: string) {
		const task = this.tasks.find((t) => t.id === id);
		if (task) {
			task.minimized = !task.minimized;
			this.notify();
		}
	}

	removeTask(id: string) {
		this.tasks = this.tasks.filter((t) => t.id !== id);
		this.notify();
	}

	clearCompleted() {
		this.tasks = this.tasks.filter((t) => t.phase === 'uploading' || t.phase === 'indexing');
		this.notify();
	}

	startUpload(files: File[], customFolderName?: string) {
		if (!files || files.length === 0) return;

		const id = 'task_' + Date.now() + '_' + Math.random().toString(36).substring(2, 7);
		const isFolder = files.length > 1 || !!customFolderName || !!files[0].webkitRelativePath;

		let folderName = customFolderName || '';
		if (!folderName && files[0].webkitRelativePath) {
			folderName = files[0].webkitRelativePath.split('/')[0];
		}
		if (!folderName) {
			folderName = isFolder ? 'Uploaded Folder' : files[0].name;
		}

		const totalBytes = files.reduce((sum, f) => sum + f.size, 0);
		const items: UploadItem[] = files.map((f) => ({
			name: f.name,
			size: f.size,
			status: 'uploading'
		}));

		const task: UploadTask = {
			id,
			title: folderName,
			items,
			totalBytes,
			loadedBytes: 0,
			percent: 0,
			phase: 'uploading',
			statusText: 'Streaming blocks to network...',
			minimized: false,
			createdAt: Date.now()
		};

		this.tasks.push(task);
		this.notify();

		const formData = new FormData();
		if (files.length === 1 && !customFolderName) {
			formData.append('file', files[0]);
		} else {
			for (let i = 0; i < files.length; i++) {
				formData.append('files', files[i]);
				formData.append('paths', files[i].webkitRelativePath || files[i].name);
			}
			if (folderName) {
				formData.append('folder_name', folderName);
			}
		}

		const xhr = new XMLHttpRequest();

		xhr.upload.addEventListener('progress', (e) => {
			if (e.lengthComputable) {
				task.loadedBytes = e.loaded;
				task.totalBytes = e.total;
				task.percent = Math.round((e.loaded / e.total) * 100);

				if (task.percent >= 100) {
					task.phase = 'indexing';
					task.statusText = 'Network Upload Complete! Finalizing Merkle DAG & Indexing...';
					task.items.forEach((it) => (it.status = 'indexing'));
				} else {
					task.statusText = `Uploading network blocks (${task.percent}%)...`;
				}
				this.notify();
			}
		});

		xhr.upload.addEventListener('load', () => {
			task.percent = 100;
			task.phase = 'indexing';
			task.statusText = 'Network Upload Complete! Finalizing Merkle DAG & Indexing...';
			task.items.forEach((it) => (it.status = 'indexing'));
			this.notify();
		});

		xhr.addEventListener('load', () => {
			if (xhr.status >= 200 && xhr.status < 300) {
				task.percent = 100;
				task.phase = 'done';
				task.statusText = 'Ingest Complete & Sealed!';
				task.items.forEach((it) => {
					it.status = 'done';
				});

				const redirectUrl = xhr.responseURL || xhr.getResponseHeader('Location');
				if (redirectUrl) {
					const parts = redirectUrl.split('/mid/');
					if (parts.length > 1) {
						const rootMID = parts[1].split('?')[0];
						if (task.items.length === 1) {
							task.items[0].mid = rootMID;
						}
					}
				}
				toast.success(`Ingest complete for "${folderName}"!`);

				// Auto-dismiss completed task after 6 seconds to keep UI clean
				setTimeout(() => {
					this.removeTask(task.id);
				}, 6000);
			} else {
				task.phase = 'error';
				task.statusText = 'Ingest failed: ' + xhr.responseText;
				task.items.forEach((it) => (it.status = 'error'));
				toast.error('Upload failed: ' + xhr.responseText);
			}
			this.notify();
		});

		xhr.addEventListener('error', () => {
			task.phase = 'error';
			task.statusText = 'Network connection error during upload.';
			task.items.forEach((it) => (it.status = 'error'));
			toast.error('Network error during upload.');
			this.notify();
		});

		xhr.open('POST', `${base}/upload`);
		xhr.send(formData);
	}
}

export const uploader = new UploaderStore();
