import { writable } from 'svelte/store';

export interface Toast {
	id: string;
	message: string;
	type: 'error' | 'success' | 'info';
	duration?: number;
}

export const toasts = writable<Toast[]>([]);

export function addToast(message: string, type: 'error' | 'success' | 'info' = 'info', duration = 4000) {
	const id = Math.random().toString(36).slice(2);
	toasts.update((all) => [...all, { id, message, type, duration }]);
	setTimeout(() => {
		removeToast(id);
	}, duration);
}

export function removeToast(id: string) {
	toasts.update((all) => all.filter((t) => t.id !== id));
}

export const toast = {
	info: (msg: string, dur?: number) => addToast(msg, 'info', dur),
	success: (msg: string, dur?: number) => addToast(msg, 'success', dur),
	error: (msg: string, dur?: number) => addToast(msg, 'error', dur)
};
