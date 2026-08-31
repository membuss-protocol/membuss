// Base English dictionary. Dot-namespaced keys, grouped by surface.
// Route-level strings migrate here incrementally; shared chrome
// (nav, search, footer) is wired through t() first.
export const en = {
	// Navigation (shared chrome)
	'nav.status': 'Status',
	'nav.files': 'Files',
	'nav.explore': 'Explore',
	'nav.memns': 'MemNS',
	'nav.edge': 'Edge',
	'nav.peers': 'Peers',
	'nav.tunnel': 'Tunnel',
	'nav.vpn': 'MemVPN',
	'nav.node': 'Node Info'
} as const;
