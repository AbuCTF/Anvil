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

export const pts = (n: number | null | undefined) =>
	typeof n === 'number' && Number.isFinite(n) ? n.toLocaleString(undefined, { maximumFractionDigits: 1 }) : '-';
