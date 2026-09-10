// Compact duration formatting for relative solve times (T+…). Zero dependency.

export function formatDur(seconds: number): string {
	const s = Math.max(0, Math.floor(seconds));
	if (s < 60) return `${s}s`;
	const m = Math.floor(s / 60);
	if (m < 60) return `${m}m`;
	const h = Math.floor(m / 60);
	const rm = m % 60;
	if (h < 24) return rm ? `${h}h ${rm}m` : `${h}h`;
	const d = Math.floor(h / 24);
	const rh = h % 24;
	return rh ? `${d}d ${rh}h` : `${d}d`;
}

// Round, human axis ticks across a [0, max] time span (in seconds).
export function timeTicks(maxSeconds: number, count = 4): number[] {
	if (maxSeconds <= 0) return [0];
	const step = maxSeconds / count;
	const ticks: number[] = [];
	for (let i = 0; i <= count; i++) ticks.push(Math.round(step * i));
	return ticks;
}
