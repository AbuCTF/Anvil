// Stable per-team color + rank-tier accents. See DESIGN.md.
// A muted, low-chroma categorical palette — distinguishable but never rainbow on
// near-black. Amber is deliberately absent; it is reserved for the accent/leader.
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

// Muted, stable per-team color from the categorical palette.
export function teamColor(key: string): string {
	return SERIES[teamHue(key) % SERIES.length];
}

// Muted category color — consistent across scoreboard/profile/challenges. Prefer this
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
