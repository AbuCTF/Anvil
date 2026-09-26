// stable per-team color + rank-tier accents. see DESIGN.md.
// a muted, low-chroma categorical palette - distinguishable but never rainbow on
// near-black. amber is deliberately absent; it is reserved for the accent/leader.
const SERIES = [
	'#6f9dc9', // steel blue
	'#7bb587', // sage green
	'#cf7f83', // dusty rose
	'#4faaa6', // teal
	'#a394c9', // lavender
	'#c9b46e', // sand
	'#cf9268', // terracotta
	'#79c7cf', // sky
	'#9aa657', // olive
	'#bd85b0', // mauve
	'#8f93d6', // periwinkle
	'#b39a86' // warm taupe
];

export function teamHue(key: string): number {
	let h = 0;
	for (let i = 0; i < key.length; i++) {
		h = (Math.imul(h, 31) + key.charCodeAt(i)) >>> 0;
	}
	return h;
}

// muted, stable per-team color from the categorical palette.
export function teamColor(key: string): string {
	return SERIES[teamHue(key) % SERIES.length];
}

// muted category color - consistent across scoreboard/profile/challenges. prefer this
// over any vibrant category color stored in the database.
const CATEGORY: Record<string, string> = {
	pwn: '#cf7f83',
	binary: '#cf7f83',
	web: '#6f9dc9',
	crypto: '#a394c9',
	rev: '#cf9268',
	reverse: '#cf9268',
	forensics: '#7bb587',
	misc: '#b39a86',
	osint: '#c9b46e',
	blockchain: '#4faaa6',
	ppc: '#8f93d6',
	network: '#79c7cf',
	hardware: '#9aa657',
	steg: '#bd85b0',
	sanity: '#837e75'
};

export function categoryColor(name: string | null | undefined): string {
	if (!name) return '#837e75';
	const k = name.toLowerCase();
	for (const key in CATEGORY) if (k.includes(key)) return CATEGORY[key];
	return teamColor(name);
}

// difficulty is a meaningful signal, so it earns color (a sanctioned exception to
// "muted"): easy=green, medium=yellow, hard=red, insane=purple. see DESIGN.md.
export function difficultyClass(d: string | null | undefined): string {
	switch ((d || '').toLowerCase()) {
		case 'easy':
			return 'text-green-500 border-green-500/30 bg-green-500/10';
		case 'medium':
			return 'text-yellow-500 border-yellow-500/30 bg-yellow-500/10';
		case 'hard':
			return 'text-red-500 border-red-500/30 bg-red-500/10';
		case 'insane':
			return 'text-purple-500 border-purple-500/30 bg-purple-500/10';
		default:
			return 'text-stone-400 border-stone-700 bg-stone-800/40';
	}
}

// resource-type pill: VM = purple, container = blue, static download = stone.
export function resourceClass(t: string | null | undefined): string {
	const v = (t || '').toLowerCase();
	if (v === 'vm') return 'text-purple-500 border-purple-500/30 bg-purple-500/10';
	if (v === 'static') return 'text-stone-400 border-stone-600/40 bg-stone-500/10';
	if (v === 'external') return 'text-teal-500 border-teal-500/30 bg-teal-500/10';
	return 'text-sky-500 border-sky-500/30 bg-sky-500/10';
}
export function resourceIcon(t: string | null | undefined): string {
	const v = (t || '').toLowerCase();
	if (v === 'vm') return 'mdi:desktop-classic';
	if (v === 'static') return 'mdi:file-download-outline';
	if (v === 'external') return 'mdi:open-in-new';
	return 'mdi:docker';
}
export function resourceLabel(t: string | null | undefined): string {
	const v = (t || '').toLowerCase();
	if (v === 'vm') return 'VM';
	if (v === 'static') return 'Static';
	if (v === 'external') return 'External';
	return 'Docker';
}

export function rankAccent(rank: number | null | undefined): string {
	switch (rank) {
		case 1:
			return 'text-amber-500';
		case 2:
			return 'text-stone-300';
		case 3:
			return 'text-amber-600/80';
		default:
			return 'text-stone-500';
	}
}
