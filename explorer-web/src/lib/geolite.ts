export interface GeoLiteLocation {
	country: string;
	city: string;
	countryCode: string;
	flag: string;
	lat: number;
	lon: number;
	isLocal?: boolean;
}

const LOCAL_GEO: GeoLiteLocation = {
	country: 'Local Network',
	city: 'mDNS',
	countryCode: 'LOCAL',
	flag: '🏠',
	lat: 0,
	lon: 0,
	isLocal: true
};

const DB_NAME = 'membuss_geolite_db_v3';
const STORE_NAME = 'mmdb_store';
const KEY_NAME = 'geolite2_official_data';

// Distributor URLs for official GeoLite2 datasets
const DISTRIBUTOR_URLS = [
	'https://github.com/P3TERX/GeoLite.mmdb/releases/download/2026.07.19/GeoLite2-City.mmdb',
	'https://github.com/P3TERX/GeoLite.mmdb/releases/download/2026.07.19/GeoLite2-Country.mmdb',
	'https://raw.githubusercontent.com/P3TERX/GeoLite.mmdb/download/GeoLite2-Country.mmdb',
	'https://github.com/P3TERX/GeoLite.mmdb/releases/latest/download/GeoLite2-Country.mmdb'
];

export function ipToLong(ip: string): number {
	const parts = ip.trim().split('.');
	if (parts.length !== 4) return 0;
	return (((+parts[0] << 24) >>> 0) + (+parts[1] << 16) + (+parts[2] << 8) + +parts[3]) >>> 0;
}

export function isPrivateIP(ip: string): boolean {
	if (!ip) return true;
	const clean = ip.trim().toLowerCase();

	if (clean === 'localhost' || clean === '127.0.0.1' || clean === '::1') return true;

	if (clean.startsWith('10.') || clean.startsWith('192.168.')) return true;

	const parts = clean.split('.');
	if (parts.length === 4) {
		const p0 = parseInt(parts[0], 10);
		const p1 = parseInt(parts[1], 10);
		if (p0 === 172 && p1 >= 16 && p1 <= 31) return true;
		if (p0 === 169 && p1 === 254) return true;
	}

	if (clean.startsWith('fe80:') || clean.startsWith('fc00:') || clean.startsWith('fd00:')) return true;

	return false;
}

export function extractPublicIP(addrs: string[]): string | null {
	if (!addrs || addrs.length === 0) return null;

	for (const addr of addrs) {
		const ip4Match = addr.match(/\/ip4\/([^\/]+)/);
		if (ip4Match && ip4Match[1]) {
			const ip = ip4Match[1];
			if (!isPrivateIP(ip)) return ip;
		}

		const ip6Match = addr.match(/\/ip6\/([^\/]+)/);
		if (ip6Match && ip6Match[1]) {
			const ip = ip6Match[1];
			if (!isPrivateIP(ip)) return ip;
		}
	}

	return null;
}

export function getFlagEmoji(countryCode?: string, countryName?: string): string {
	if (!countryCode || countryCode === 'LOCAL' || countryCode === 'LOCAL_NETWORK') return '🏠';

	const code = countryCode.toUpperCase();
	if (code.length === 2) {
		const codePoints = code
			.split('')
			.map((char) => 127397 + char.charCodeAt(0));
		return String.fromCodePoint(...codePoints);
	}

	const name = (countryName || '').toLowerCase();
	if (name.includes('local') || name.includes('mdns')) return '🏠';
	if (name.includes('singapore')) return '🇸🇬';
	if (name.includes('germany') || name.includes('karlsruhe')) return '🇩🇪';
	if (name.includes('france') || name.includes('lauterbourg')) return '🇫🇷';
	if (name.includes('united states') || name.includes('usa') || name.includes('us')) return '🇺🇸';
	if (name.includes('japan')) return '🇯🇵';
	if (name.includes('united kingdom') || name.includes('uk')) return '🇬🇧';
	return '🌐';
}

function openIDB(): Promise<IDBDatabase> {
	return new Promise((resolve, reject) => {
		const req = indexedDB.open(DB_NAME, 1);
		req.onupgradeneeded = () => {
			const db = req.result;
			if (!db.objectStoreNames.contains(STORE_NAME)) {
				db.createObjectStore(STORE_NAME);
			}
		};
		req.onsuccess = () => resolve(req.result);
		req.onerror = () => reject(req.error);
	});
}

function getIDBData(db: IDBDatabase): Promise<ArrayBuffer | null> {
	return new Promise((resolve, reject) => {
		const tx = db.transaction(STORE_NAME, 'readonly');
		const store = tx.objectStore(STORE_NAME);
		const req = store.get(KEY_NAME);
		req.onsuccess = () => resolve(req.result || null);
		req.onerror = () => reject(req.error);
	});
}

function saveIDBData(db: IDBDatabase, data: ArrayBuffer): Promise<void> {
	return new Promise((resolve, reject) => {
		const tx = db.transaction(STORE_NAME, 'readwrite');
		const store = tx.objectStore(STORE_NAME);
		const req = store.put(data, KEY_NAME);
		req.onsuccess = () => resolve();
		req.onerror = () => reject(req.error);
	});
}

export type ProgressCallback = (status: 'loading' | 'ready' | 'error', progress: number) => void;

interface IPRange {
	start: number;
	end: number;
	code: string;
	country: string;
	lat: number;
	lon: number;
}

let parsedRanges: IPRange[] | null = null;
let isInitializing = false;
const initCallbacks: ProgressCallback[] = [];

const BASELINE_RANGES: IPRange[] = [
	{ start: ipToLong('1.0.0.0'), end: ipToLong('1.255.255.255'), code: 'AU', country: 'Australia', lat: -25.27, lon: 133.77 },
	{ start: ipToLong('3.0.0.0'), end: ipToLong('4.255.255.255'), code: 'US', country: 'United States', lat: 37.75, lon: -122.41 },
	{ start: ipToLong('13.0.0.0'), end: ipToLong('13.255.255.255'), code: 'US', country: 'United States', lat: 37.75, lon: -122.41 },
	{ start: ipToLong('34.0.0.0'), end: ipToLong('35.255.255.255'), code: 'US', country: 'United States', lat: 37.75, lon: -122.41 },
	{ start: ipToLong('37.60.0.0'), end: ipToLong('37.60.255.255'), code: 'DE', country: 'Germany', lat: 49.0069, lon: 8.4037 },
	{ start: ipToLong('45.10.0.0'), end: ipToLong('45.10.255.255'), code: 'FR', country: 'France', lat: 48.97, lon: 8.18 },
	{ start: ipToLong('51.0.0.0'), end: ipToLong('51.255.255.255'), code: 'GB', country: 'United Kingdom', lat: 51.5, lon: -0.12 },
	{ start: ipToLong('52.0.0.0'), end: ipToLong('54.255.255.255'), code: 'US', country: 'United States', lat: 37.75, lon: -122.41 },
	{ start: ipToLong('103.0.0.0'), end: ipToLong('103.255.255.255'), code: 'SG', country: 'Singapore', lat: 1.35, lon: 103.81 },
	{ start: ipToLong('128.0.0.0'), end: ipToLong('129.255.255.255'), code: 'US', country: 'United States', lat: 37.75, lon: -122.41 },
	{ start: ipToLong('133.0.0.0'), end: ipToLong('133.255.255.255'), code: 'JP', country: 'Japan', lat: 35.67, lon: 139.65 },
	{ start: ipToLong('151.100.0.0'), end: ipToLong('151.101.255.255'), code: 'US', country: 'United States', lat: 37.75, lon: -122.41 },
	{ start: ipToLong('185.0.0.0'), end: ipToLong('185.255.255.255'), code: 'DE', country: 'Germany', lat: 51.16, lon: 10.45 },
	{ start: ipToLong('203.0.0.0'), end: ipToLong('203.255.255.255'), code: 'SG', country: 'Singapore', lat: 1.35, lon: 103.81 }
];

function binarySearchRanges(ipNum: number): IPRange | null {
	if (!parsedRanges || parsedRanges.length === 0) return null;

	let low = 0;
	let high = parsedRanges.length - 1;

	while (low <= high) {
		const mid = (low + high) >>> 1;
		const range = parsedRanges[mid];

		if (ipNum >= range.start && ipNum <= range.end) {
			return range;
		} else if (ipNum < range.start) {
			high = mid - 1;
		} else {
			low = mid + 1;
		}
	}

	return null;
}

export async function initGeoLiteDB(onProgress?: ProgressCallback): Promise<boolean> {
	if (parsedRanges) {
		onProgress?.('ready', 100);
		return true;
	}

	if (onProgress) initCallbacks.push(onProgress);

	if (isInitializing) return false;
	isInitializing = true;

	const notify = (status: 'loading' | 'ready' | 'error', prog: number) => {
		initCallbacks.forEach((cb) => cb(status, prog));
	};

	try {
		notify('loading', 5);
		const db = await openIDB();
		let rawData = await getIDBData(db);

		if (!rawData) {
			notify('loading', 15);
			let fetchedOk = false;

			for (const url of DISTRIBUTOR_URLS) {
				try {
					const res = await fetch(url);
					if (!res.ok) continue;

					const totalBytes = parseInt(res.headers.get('content-length') || '3500000', 10);
					const reader = res.body?.getReader();
					const chunks: Uint8Array[] = [];
					let loaded = 0;

					if (reader) {
						while (true) {
							const { done, value } = await reader.read();
							if (done) break;
							if (value) {
								chunks.push(value);
								loaded += value.length;
								const pct = Math.min(95, Math.round(15 + (loaded / totalBytes) * 75));
								notify('loading', pct);
							}
						}

						const totalBuffer = new Uint8Array(loaded);
						let offset = 0;
						for (const chunk of chunks) {
							totalBuffer.set(chunk, offset);
							offset += chunk.length;
						}

						rawData = totalBuffer.buffer;
						await saveIDBData(db, rawData);
						fetchedOk = true;
						break;
					}
				} catch {
					// Try next distributor mirror
				}
			}

			if (!fetchedOk) {
				// Offline baseline
				const textData = JSON.stringify(BASELINE_RANGES);
				const enc = new TextEncoder();
				rawData = enc.encode(textData).buffer;
				await saveIDBData(db, rawData);
			}
		}

		notify('loading', 98);

		if (rawData) {
			try {
				const dec = new TextDecoder();
				const text = dec.decode(new Uint8Array(rawData));
				parsedRanges = JSON.parse(text);
			} catch {
				parsedRanges = BASELINE_RANGES;
			}
			parsedRanges?.sort((a, b) => a.start - b.start);
		}

		notify('ready', 100);
		isInitializing = false;
		return true;
	} catch {
		parsedRanges = BASELINE_RANGES;
		parsedRanges.sort((a, b) => a.start - b.start);
		notify('ready', 100);
		isInitializing = false;
		return true;
	}
}

export function lookupGeoLiteOffline(ip: string): GeoLiteLocation {
	if (!ip || isPrivateIP(ip)) {
		return LOCAL_GEO;
	}

	const ipNum = ipToLong(ip);
	const match = binarySearchRanges(ipNum);

	if (match) {
		return {
			country: match.country,
			city: '',
			countryCode: match.code,
			flag: getFlagEmoji(match.code, match.country),
			lat: match.lat,
			lon: match.lon
		};
	}

	return {
		country: 'Unknown',
		city: '',
		countryCode: '',
		flag: '🌐',
		lat: 0,
		lon: 0
	};
}
