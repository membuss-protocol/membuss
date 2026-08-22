// Minimal i18n layer (finding.txt XC-010). Synchronous dictionary
// lookup backed by svelte stores: `t` is a derived store returning a
// translate function, so components use it as {$t('nav.status')}.
//
// Adding a locale: create locales/<code>.ts exporting the same key
// set, then register it below. Missing keys fall back to English,
// then to the raw key.
import { derived, writable } from 'svelte/store';
import { en } from './locales/en';

const dictionaries: Record<string, Record<string, string>> = { en };

export type Locale = keyof typeof dictionaries;
export const locale = writable<Locale>('en');

export const t = derived(locale, (l) => (key: string): string =>
	dictionaries[l]?.[key] ?? dictionaries.en[key] ?? key
);
