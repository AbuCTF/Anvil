// Stable per-team color + rank-tier accents (matches the arena/scoreboard theme).

export function teamHue(key: string): number {
	let h = 0;
	for (let i = 0; i < key.length; i++) {
		h = (Math.imul(h, 31) + key.charCodeAt(i)) >>> 0;
	}
	return h % 360;
}

// Muted, low-chroma per-team color — distinguishable but stoic on near-black.
export function teamColor(key: string): string {
	return `hsl(${teamHue(key)} 26% 62%)`;
}

export function rankAccent(rank: number | null | undefined): string {
	switch (rank) {
		case 1:
			return 'text-yellow-400';
		case 2:
			return 'text-stone-300';
		case 3:
			return 'text-amber-600';
		default:
			return 'text-stone-500';
	}
}
