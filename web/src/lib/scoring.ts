// finals blend: score = 1000 x sum(weight x relative), each board median 0 to leader 1
export interface ScoreWeights {
	ad: number;
	koth: number;
	jeopardy: number;
}

// a team's score cut into what each board earned; the parts add up to the score
export interface ScoreShare {
	ad: number;
	koth: number;
	jeopardy?: number;
}

const pct = (w: number) => `${+(w * 100).toFixed(1)}%`;

export const pts = (n: number | null | undefined) =>
	typeof n === 'number' && Number.isFinite(n) ? n.toLocaleString(undefined, { maximumFractionDigits: 1 }) : '-';

// the most a board can add: what its leader gets
export const fullShare = (w: number) => Math.round(1000 * w);

export function weightsLabel(w: ScoreWeights): string {
	const parts = [`${pct(w.ad)} attack-defense`, `${pct(w.koth)} king of the hill`];
	if (w.jeopardy > 0) parts.push(`${pct(w.jeopardy)} jeopardy`);
	return `Score = ${parts.join(' + ')}, each relative to the field (leader = full share, median = 0)`;
}
