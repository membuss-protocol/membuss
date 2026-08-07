import { base } from '$app/paths';

export async function apiFetch(path: string, init?: RequestInit) {
	const sep = path.includes('?') ? '&' : '?';
	const url = `${base}${path}${sep}format=json`;
	const headers: Record<string, string> = {
		'Accept': 'application/json',
		...Object.fromEntries(new Headers(init?.headers).entries())
	};
	let res = await fetch(url, { ...init, headers });
	// Respect Retry-After on 429 instead of throwing immediately
	if (res.status === 429) {
		const retryAfter = parseInt(res.headers.get('Retry-After') || '2', 10);
		await new Promise(r => setTimeout(r, Math.min(retryAfter, 5) * 1000));
		res = await fetch(url, { ...init, headers: { 'Accept': 'application/json', ...Object.fromEntries(new Headers(init?.headers).entries()) } });
	}
	if (!res.ok) {
		throw new Error(await res.text() || `HTTP ${res.status}`);
	}
	const text = await res.text();
	return text ? JSON.parse(text) : null;
}

export function formatBytes(bytes: number): string {
	if (bytes === 0) return '0 B';
	const k = 1024;
	const sizes = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
	const i = Math.floor(Math.log(bytes) / Math.log(k));
	return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

export function validateMIDFormat(input: string): { valid: boolean; reason?: string } {
	if (!input) return { valid: false, reason: 'empty query' };
	const str = input.trim();
	const lower = str.toLowerCase();

	// 1. Detect concatenated MIDs (e.g. "membaf...membaf...")
	const memMatches = str.match(/mem[a-z0-9]{30,}/gi);
	if (memMatches && memMatches.length > 1) {
		return { valid: false, reason: 'bad mid: concatenated mid strings detected' };
	}

	// 2. If string starts with "mem", validate MID format
	if (lower.startsWith('mem')) {
		if (str.length < 25 || str.length > 110) {
			return { valid: false, reason: `bad mid: invalid mid length (${str.length} chars)` };
		}
		if (!/^mem[a-z0-9]+$/i.test(str)) {
			return { valid: false, reason: 'bad mid: invalid characters in multihash' };
		}
		return { valid: true };
	}

	// 3. MemNS handle, key, or name (e.g. "k51...", "k3...", "memns/...", "alice")
	if (lower.startsWith('k51') || lower.startsWith('k3') || lower.startsWith('memns') || !str.includes('/')) {
		if (str.length > 150) {
			return { valid: false, reason: 'bad query: query string too long' };
		}
		return { valid: true };
	}

	// 4. MemLink domain (e.g. "example.com")
	if (str.includes('.')) {
		if (str.length > 150) {
			return { valid: false, reason: 'bad domain: domain string too long' };
		}
		return { valid: true };
	}

	return { valid: true };
}
